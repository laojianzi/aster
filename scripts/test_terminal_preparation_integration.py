"""Real offline preparation, Git discovery isolation and failed-postimage tests."""
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
POLICY = 'terminal_hardening_postimages.json'
PATCHED = ('internal/vt/open_windows.go', 'library.go', 'paint.go',
           'paste.go', 'terminal.go', 'view.go')

# The test-only child shim runs the original prepare CLI. It changes only the
# simulated result of applying the patch; no production fault hook is added.
FAULT_CHILD = r'''
from pathlib import Path
import os, runpy, shutil, subprocess, sys
original_run = subprocess.run
def fault_run(argv, *args, **kwargs):
    if argv[0] == 'git' and os.environ.get('ASTER_TEST_GIT_CALLS'):
        with open(os.environ['ASTER_TEST_GIT_CALLS'], 'a', encoding='utf-8') as calls:
            calls.write(repr(argv) + '\n')
    applying = argv[0] == 'git' and 'apply' in argv and '--check' not in argv
    fault = os.environ['ASTER_TEST_PATCH_FAULT']
    if applying and fault == 'noop':
        return subprocess.CompletedProcess(argv, 0)
    result = original_run(argv, *args, **kwargs)
    if applying and result.returncode == 0:
        source = Path(os.environ['ASTER_TEST_PATCH_SOURCE'])
        stage = Path(kwargs['cwd'])
        if fault == 'trace-only':
            pass
        elif fault == 'unexpected-file':
            (stage / 'unexpected.go').write_bytes(b'package unexpected\n')
        elif fault == 'modified-unpatched-file':
            (stage / 'box.go').write_bytes(b'package changed\n')
        elif fault == 'missing-source-file':
            (stage / 'box.go').unlink()
        else:
            shutil.copyfile(source / fault, stage / fault)
    return result
subprocess.run = fault_run
sys.argv = sys.argv[1:]
runpy.run_path(sys.argv[0], run_name='__main__')
'''

# Exercise the real no --source branch without requiring the public network.
# The returned module directory contains the same 31 hash-verified real sources.
DOWNLOAD_CHILD = r'''
import json, os, runpy, subprocess, sys
from pathlib import Path
original_check_output = subprocess.check_output
def capture_download(argv, *args, **kwargs):
    if argv[:3] == ['go', 'mod', 'download']:
        env = kwargs['env']
        record = {'argv': argv,
                  'git_keys': sorted(k for k in env if k.upper().startswith('GIT_')),
                  'preserved': {k: env.get(k) for k in (
                      'GOPROXY', 'GOSUMDB', 'GOFLAGS', 'ASTER_TEST_ENV_SENTINEL')}}
        Path(os.environ['ASTER_TEST_DOWNLOAD_ENV_CAPTURE']).write_text(json.dumps(record))
        return json.dumps({'Dir': os.environ['ASTER_TEST_SOURCE_MODULE_DIR']})
    return original_check_output(argv, *args, **kwargs)
subprocess.check_output = capture_download
sys.argv = sys.argv[1:]
runpy.run_path(sys.argv[0], run_name='__main__')
'''


def sha(data):
    return hashlib.sha256(data).hexdigest()


def inventory(path):
    if not path.exists():
        return {}
    return {str(p.relative_to(path)).replace('\\', '/'): sha(p.read_bytes())
            for p in sorted(path.rglob('*')) if p.is_file()}


class RealTerminalPreparationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.spec = json.loads((ROOT/'third_party/native-terminal/UPSTREAM.json').read_text())
        cache = subprocess.check_output(['go', 'env', 'GOMODCACHE'], text=True).strip()
        cls.source = Path(cache)/(cls.spec['module']+'@'+cls.spec['version'])/'plugins/terminal'
        for name, expected in cls.spec['files'].items():
            if sha((cls.source/name).read_bytes()) != expected:
                raise AssertionError('verified cached terminal source required: '+name)
        systems = {'Linux': 'linux', 'Darwin': 'darwin', 'Windows': 'windows'}
        arches = {'x86_64': 'amd64', 'AMD64': 'amd64', 'arm64': 'arm64', 'aarch64': 'arm64'}
        cls.target = systems[platform.system()]+'-'+arches[platform.machine()]
        plugin = json.loads((cls.source/'mygo-plugin.json').read_text())
        cls.library = plugin['libraries'][0]['files'][cls.target]
        library_path = Path(os.environ.get('ASTER_TEST_LIBRARY') or ROOT/'.aster-native'/cls.library['name'])
        cls.library_bytes = library_path.read_bytes()
        if sha(cls.library_bytes) != cls.library['sha256']:
            raise AssertionError('verified local native library required')

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='aster-real-prepare-')
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.project = self.base/'project'
        self.project.mkdir()
        for path in ('.gitattributes', 'go.mod', 'scripts/dev.py', 'scripts/prepare_terminal.py',
                     'third_party/native-terminal/UPSTREAM.json',
                     'third_party/native-terminal/hardening.patch',
                     'third_party/native-terminal/LICENSE.mygo'):
            target = self.project/path
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT/path, target)
        if (ROOT/'scripts'/POLICY).exists():
            shutil.copyfile(ROOT/'scripts'/POLICY, self.project/'scripts'/POLICY)
        self.outside = self.base/'outside-tmp'
        self.inside = self.project/'artifacts/inside-tmp'
        self.outside.mkdir()
        self.inside.mkdir(parents=True)
        self.global_config = self.base/'global-git-config'
        self.global_config.write_text('[core]\n\tautocrlf = true\n', encoding='utf-8')
        self.env = {k: v for k, v in os.environ.items() if not k.startswith('GIT_')}
        self.env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=str(self.global_config),
                        GOTOOLCHAIN='local', GOPROXY='off', GOSUMDB='off')
        subprocess.run(['git', 'init', '-q'], cwd=self.project, env=self.env,
                       check=True, capture_output=True, timeout=30)
        self.libroot = self.base/'libraries'
        libpath = self.libroot/self.target/self.library['name']
        libpath.parent.mkdir(parents=True)
        libpath.write_bytes(self.library_bytes)

    def prepare(self, temp, *, injected=None, fault=None, dev=False, download_capture=None):
        env = dict(self.env, TMPDIR=str(temp), TMP=str(temp), TEMP=str(temp))
        if injected:
            env.update(injected)
        script = self.project/'scripts'/('dev.py' if dev else 'prepare_terminal.py')
        argv = [sys.executable, str(script)]
        if dev:
            argv.append('prepare')
        if fault:
            self.assertFalse(dev)
            env.update(ASTER_TEST_PATCH_FAULT=fault, ASTER_TEST_PATCH_SOURCE=str(self.source))
            argv = [sys.executable, '-c', FAULT_CHILD, str(script)]
        if download_capture:
            self.assertFalse(dev or fault)
            env.update(ASTER_TEST_DOWNLOAD_ENV_CAPTURE=str(download_capture),
                       ASTER_TEST_SOURCE_MODULE_DIR=str(self.source.parent.parent))
            argv = [sys.executable, '-c', DOWNLOAD_CHILD, str(script)]
        else:
            argv += ['--source', str(self.source)]
        argv += ['--library-source', str(self.libroot)]
        return subprocess.run(argv, cwd=self.project, env=env, capture_output=True,
                              text=True, timeout=90)

    def publication(self):
        return (inventory(self.project/'internal/nativeterm/upstream'),
                inventory(self.project/'.aster-native'))

    def success(self, result):
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('Prepared 31 verified upstream files', result.stdout)
        generated, native = self.publication()
        self.assertEqual(set(generated), set(self.spec['files']) | {'LICENSE', '.aster-generated'})
        self.assertIn('provenance.json', native)
        self.assertEqual(native[self.library['name']], self.library['sha256'])
        return generated

    def test_real_prepare_inside_and_outside_tmpdir_are_identical(self):
        first = self.success(self.prepare(self.outside, dev=True))
        receipt = (self.project/'.aster-native/provenance.json').read_bytes()
        second = self.success(self.prepare(self.inside, dev=True))
        self.assertEqual(second, first, 'TMPDIR changed the actual hardened generated files')
        self.assertEqual((self.project/'.aster-native/provenance.json').read_bytes(), receipt)
        provenance = json.loads(receipt)
        policy = (self.project/'scripts'/POLICY).read_bytes()
        self.assertEqual(provenance['hardening_postimages_sha256'], sha(policy))
        self.assertEqual(provenance['patch_sha256'], sha((self.project/'third_party/native-terminal/hardening.patch').read_bytes()))

    def test_inherited_git_environment_cannot_target_another_repository(self):
        expected = self.success(self.prepare(self.outside))
        foreign = self.base/'foreign'
        foreign.mkdir()
        subprocess.run(['git', 'init', '-q'], cwd=foreign, env=self.env,
                       check=True, capture_output=True, timeout=30)
        for path in self.spec['files']:
            target = foreign/path
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(self.source/path, target)
        (foreign/'index-sentinel').write_bytes(b'do not change this external file\n')
        before = inventory(foreign)
        result = self.prepare(self.inside, injected={
            'GIT_DIR': str(foreign/'.git'), 'GIT_WORK_TREE': str(foreign),
            'GIT_COMMON_DIR': str(foreign/'.git'), 'GIT_INDEX_FILE': str(foreign/'index-sentinel'),
            'GIT_PREFIX': 'unrelated-prefix/', 'GIT_CEILING_DIRECTORIES': str(foreign),
            'GIT_CONFIG_COUNT': '1', 'GIT_CONFIG_KEY_0': 'core.worktree',
            'GIT_CONFIG_VALUE_0': str(foreign), 'GIT_TRACE': str(foreign/'unexpected-trace'),
        })
        self.assertEqual(self.success(result), expected)
        self.assertEqual(inventory(foreign), before, 'inherited Git environment changed an external repository')

    def test_ancestor_git_config_and_global_attributes_are_not_read(self):
        expected = self.success(self.prepare(self.outside))
        config = self.project/'.git/config'
        original = config.read_bytes()
        config.write_bytes(b'not a valid Git config\n')
        attributes = self.base/'xdg/git/attributes'
        attributes.parent.mkdir(parents=True)
        attributes.write_text('* text eol=crlf\n', encoding='utf-8')
        try:
            result = self.prepare(self.inside, injected={'XDG_CONFIG_HOME': str(self.base/'xdg')})
            self.assertEqual(self.success(result), expected)
            self.assertEqual(config.read_bytes(), b'not a valid Git config\n')
        finally:
            config.write_bytes(original)

    def test_module_download_does_not_inherit_git_repository_overrides(self):
        capture = self.base/'module-download-env.json'
        result = self.prepare(self.inside, download_capture=capture, injected={
            'GIT_DIR': str(self.project/'.git'), 'GIT_WORK_TREE': str(self.project),
            'GIT_INDEX_FILE': str(self.base/'unexpected-index'),
            'GIT_TRACE': str(self.base/'unexpected-trace'),
            'GIT_CONFIG_COUNT': '1', 'GIT_CONFIG_KEY_0': 'core.worktree',
            'GIT_CONFIG_VALUE_0': str(self.project),
            'ASTER_TEST_ENV_SENTINEL': 'preserve-non-git-settings',
        })
        self.success(result)
        record = json.loads(capture.read_text())
        self.assertEqual(record['argv'], ['go', 'mod', 'download', '-json',
                                         self.spec['module']+'@'+self.spec['version']])
        self.assertEqual(record['git_keys'], [])
        self.assertEqual(record['preserved'], {'GOPROXY': 'off', 'GOSUMDB': 'off',
                         'GOFLAGS': '-mod=mod',
                         'ASTER_TEST_ENV_SENTINEL': 'preserve-non-git-settings'})
        self.assertFalse((self.base/'unexpected-index').exists())
        self.assertFalse((self.base/'unexpected-trace').exists())

    def test_unrepresentable_ceiling_fails_before_git_or_publication(self):
        temp = self.project/('artifacts/tmp'+os.pathsep+'unsafe')
        temp.mkdir(parents=True)
        calls = self.base/'unexpected-git-calls'
        result = self.prepare(temp, fault='trace-only',
                              injected={'ASTER_TEST_GIT_CALLS': str(calls)})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('temporary directory cannot safely isolate Git discovery', result.stderr)
        self.assertNotIn('Prepared ', result.stdout)
        self.assertFalse(calls.exists(), 'Git ran before validating the isolation boundary')
        self.assertFalse((self.project/'internal/nativeterm/upstream').exists())
        self.assertFalse((self.project/'.aster-native').exists())

    def test_each_missing_postimage_fails_before_publication(self):
        self.success(self.prepare(self.outside))
        expected = self.publication()
        for path in (*PATCHED, 'noop'):
            with self.subTest(path=path):
                result = self.prepare(self.inside, fault=path)
                self.assertNotEqual(result.returncode, 0, 'unapplied hardening was reported successful: '+path)
                self.assertNotIn('Prepared ', result.stdout)
                self.assertEqual(self.publication(), expected, 'failed preparation changed earlier verified output')

    def test_complete_source_inventory_is_required_before_publication(self):
        self.success(self.prepare(self.outside))
        expected = self.publication()
        for fault in ('unexpected-file', 'modified-unpatched-file', 'missing-source-file'):
            with self.subTest(fault=fault):
                result = self.prepare(self.inside, fault=fault)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertNotIn('Prepared ', result.stdout)
                self.assertEqual(self.publication(), expected)

    def test_changed_or_incomplete_policy_cannot_publish(self):
        self.success(self.prepare(self.outside))
        expected = self.publication()
        path = self.project/'scripts'/POLICY
        original = path.read_bytes()
        try:
            for fault in ('patch-hash', 'missing-path', 'unchanged-postimage',
                          'extra-path', 'version', 'wrong-postimage'):
                with self.subTest(fault=fault):
                    policy = json.loads(original)
                    if fault == 'patch-hash':
                        policy['patch_sha256'] = '0'*64
                    elif fault == 'missing-path':
                        del policy['postimages']['library.go']
                    elif fault == 'unchanged-postimage':
                        policy['postimages']['library.go'] = self.spec['files']['library.go']
                    elif fault == 'extra-path':
                        policy['postimages']['box.go'] = '0'*64
                    elif fault == 'version':
                        policy['version'] = 'v0.0.0'
                    else:
                        policy['postimages']['library.go'] = '0'*64
                    path.write_text(json.dumps(policy), encoding='utf-8', newline='\n')
                    result = self.prepare(self.inside)
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertNotIn('Prepared ', result.stdout)
                    self.assertEqual(self.publication(), expected)
        finally:
            path.write_bytes(original)

    def test_first_noop_cannot_publish_a_success_receipt(self):
        result = self.prepare(self.inside, fault='noop')
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('Prepared ', result.stdout)
        self.assertFalse((self.project/'internal/nativeterm/upstream').exists())
        self.assertFalse((self.project/'.aster-native').exists())


if __name__ == '__main__':
    unittest.main()
