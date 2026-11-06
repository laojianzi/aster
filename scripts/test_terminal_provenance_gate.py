#!/usr/bin/env python3
"""Self-contained regression tests for scripts/check_terminal_provenance.py.

These build a minimal fake repository from scratch in a temp directory, so the
suite does not depend on a prepared .aster-native/ tree, a downloaded native
library, network access, or any product source. CI already runs this file via
`python -m unittest discover -s scripts -p 'test_*.py'`.
"""
import hashlib
import json
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

GATE = Path(__file__).resolve().parent / 'check_terminal_provenance.py'
MODULE = 'github.com/egoist/mygo'
VERSION = 'v0.7.3'
LIB_BYTES = b'fake native library payload\n'


def digest(data):
    return hashlib.sha256(data).hexdigest()


def build_fixture(root, lib_name='libghostty-vt.so', version=VERSION):
    meta = root / 'third_party' / 'native-terminal'
    meta.mkdir(parents=True, exist_ok=True)
    patch = meta / 'hardening.patch'
    patch.write_bytes(b'dummy hardening patch\n')
    (meta / 'UPSTREAM.json').write_text(json.dumps(
        {'module': MODULE, 'version': version, 'files': []}, indent=2), encoding='utf-8')

    (root / 'go.mod').write_text(
        'module aster\n\ngo 1.27.3\n\nrequire (\n\t' + MODULE + ' v0.7.3\n)\n',
        encoding='utf-8')

    generated = root / 'internal' / 'nativeterm' / 'upstream' / 'only.go'
    generated.parent.mkdir(parents=True, exist_ok=True)
    generated.write_bytes(b'package upstream\n')

    live_dir = root / '.aster-native'
    dist_dir = root / 'dist'
    live_dir.mkdir(exist_ok=True)
    dist_dir.mkdir(exist_ok=True)
    (live_dir / lib_name).write_bytes(LIB_BYTES)
    (dist_dir / lib_name).write_bytes(LIB_BYTES)

    live = {
        'module': MODULE,
        'version': version,
        'platform': 'linux-amd64',
        'patch_sha256': digest(patch.read_bytes()),
        'generated_files': {'only.go': digest(generated.read_bytes())},
        'library': {
            'name': lib_name,
            'sha256': digest(LIB_BYTES),
            'url': 'https://example.invalid/native-library',
        },
    }
    text = json.dumps(live, indent=2) + '\n'
    (live_dir / 'provenance.json').write_text(text, encoding='utf-8')
    (dist_dir / 'terminal-provenance.json').write_text(text, encoding='utf-8')
    return live


def remove_dist(root):
    for p in (root / 'dist').iterdir():
        p.unlink()
    (root / 'dist').rmdir()


def rewrite(root, rel, mutate):
    p = root / rel
    data = json.loads(p.read_text(encoding='utf-8'))
    mutate(data)
    p.write_text(json.dumps(data, indent=2) + '\n', encoding='utf-8')


class TerminalProvenanceGateTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        build_fixture(self.root)

    def tearDown(self):
        self._tmp.cleanup()

    def run_gate(self, *extra):
        return subprocess.run(
            [sys.executable, str(GATE), '--root', str(self.root), *extra],
            capture_output=True, text=True)

    def assert_pass(self, result):
        self.assertEqual(result.returncode, 0,
                         'gate should pass\n' + result.stdout + result.stderr)

    def assert_fail(self, result, marker):
        self.assertEqual(result.returncode, 1,
                         'gate should fail\n' + result.stdout + result.stderr)
        self.assertIn(marker, result.stdout)

    # --- positive paths -------------------------------------------------
    def test_clean_repo_passes_with_dist(self):
        result = self.run_gate('--require-dist')
        self.assert_pass(result)
        self.assertIn('23/23 passed', result.stdout)
        self.assertIn('dist/libghostty-vt.so', result.stdout)

    def test_source_only_scope_passes_without_dist(self):
        remove_dist(self.root)
        result = self.run_gate()
        self.assert_pass(result)
        self.assertIn('source-only scope', result.stdout)
        self.assertIn('[SKIP]', result.stdout)

    def run_fresh_repo(self, lib_name):
        """Build a standalone repo for a different native library suffix."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            build_fixture(root, lib_name=lib_name)
            result = subprocess.run(
                [sys.executable, str(GATE), '--root', str(root), '--require-dist'],
                capture_output=True, text=True)
        return result

    def test_macos_dylib_name_is_honoured(self):
        result = self.run_fresh_repo('libghostty-vt.dylib')
        self.assert_pass(result)
        self.assertIn('dist/libghostty-vt.dylib', result.stdout)

    def test_windows_dll_name_is_honoured(self):
        result = self.run_fresh_repo('ghostty-vt.dll')
        self.assert_pass(result)
        self.assertIn('dist/ghostty-vt.dll', result.stdout)

    # --- negative paths -------------------------------------------------
    def test_require_dist_fails_when_dist_absent(self):
        remove_dist(self.root)
        result = self.run_gate('--require-dist')
        self.assert_fail(result, 'dist provenance present')

    def test_stale_dist_version_fails(self):
        rewrite(self.root, 'dist/terminal-provenance.json',
                lambda d: d.update(version='v0.5.5'))
        result = self.run_gate('--require-dist')
        self.assert_fail(result, 'dist provenance == pinned version')
        self.assertIn('dist provenance identical to live', result.stdout)

    def test_wrong_patch_hash_fails(self):
        rewrite(self.root, '.aster-native/provenance.json',
                lambda d: d.update(patch_sha256='0' * 64))
        result = self.run_gate()
        self.assert_fail(result, 'live patch_sha256 == on-disk patch')

    def test_unsafe_library_name_rejected(self):
        for rel in ('.aster-native/provenance.json', 'dist/terminal-provenance.json'):
            rewrite(self.root, rel, lambda d: d['library'].update(name='../escape.so'))
        result = self.run_gate('--require-dist')
        self.assert_fail(result, 'library name safe')

    def test_go_mod_pin_mismatch_fails(self):
        rewrite(self.root, 'third_party/native-terminal/UPSTREAM.json',
                lambda d: d.update(version='v0.5.5'))
        result = self.run_gate()
        self.assert_fail(result, 'go.mod == UPSTREAM version')

    def test_replace_directive_rejected(self):
        p = self.root / 'go.mod'
        p.write_text(p.read_text(encoding='utf-8')
                     + '\nreplace ' + MODULE + ' => ../mygo\n', encoding='utf-8')
        result = self.run_gate()
        self.assert_fail(result, 'no mygo replace directive')

    def test_unrecorded_generated_file_rejected(self):
        (self.root / 'internal' / 'nativeterm' / 'upstream' / 'extra.go').write_bytes(
            b'package upstream\n')
        result = self.run_gate()
        self.assert_fail(result, 'no unrecorded generated file')

    def test_tampered_generated_file_rejected(self):
        (self.root / 'internal' / 'nativeterm' / 'upstream' / 'only.go').write_bytes(
            b'package upstream // tampered\n')
        result = self.run_gate()
        self.assert_fail(result, 'all generated file hashes match')


class RealRepositoryGateTest(unittest.TestCase):
    """Wire the gate into CI without editing any workflow.

    ci.yml already runs prepare_terminal.py and then
    `python -m unittest discover -s scripts -p 'test_*.py'`, so these checks
    exercise the real checkout on every CI run. Workflow files are deliberately
    left untouched because their exact bytes are frozen by earlier reviewed
    scopes (see scripts/go_toolchain_policy.py and test_helm_install_scope.py).
    """
    REAL_ROOT = Path(__file__).resolve().parents[1]

    def run_real(self, *extra):
        # No --root on purpose: this also verifies the gate resolves its own
        # repository root regardless of the caller's working directory.
        return subprocess.run([sys.executable, str(GATE), *extra],
                              capture_output=True, text=True)

    @unittest.skipUnless(
        (Path(__file__).resolve().parents[1] / '.aster-native/provenance.json').is_file(),
        'prepare_terminal.py has not run in this checkout')
    def test_real_repository_source_provenance_is_consistent(self):
        result = self.run_real()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('all generated file hashes match', result.stdout)
        self.assertIn('live patch_sha256 == on-disk patch', result.stdout)

    @unittest.skipUnless(
        (Path(__file__).resolve().parents[1] / 'dist/terminal-provenance.json').is_file(),
        'this job does not produce dist artifacts')
    def test_real_repository_dist_copy_is_consistent(self):
        result = self.run_real('--require-dist')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn('dist provenance identical to live', result.stdout)

    def test_workflow_provenance_copies_come_from_live_source(self):
        """A2 root cause: a dist/evidence copy must never drift from live.

        Static contract on the existing copy chain. If a future change replaces
        `cp .aster-native/provenance.json <dest>` with a hardcoded artifact or a
        different source, this fails in the ordinary unit-test job.
        """
        copies = {
            '.github/workflows/ci.yml': 'dist/terminal-provenance.json',
            '.github/workflows/native-migration.yml': 'dist/terminal-provenance.json',
            '.github/workflows/source-evidence.yml': 'evidence/terminal-provenance.json',
        }
        for rel, destination in copies.items():
            with self.subTest(workflow=rel, destination=destination):
                text = (self.REAL_ROOT / rel).read_text(encoding='utf-8')
                self.assertRegex(
                    text,
                    r'cp\s+\.aster-native/provenance\.json\s+' + re.escape(destination),
                    'provenance must always be copied from the freshly generated live file')
                self.assertIn('scripts/prepare_terminal.py', text)

if __name__ == '__main__':
    unittest.main()
