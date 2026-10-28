#!/usr/bin/env python3
"""Rebuild Aster's small MyGo terminal adaptation from hash-pinned source.

Network access occurs only during this explicit build step. The product never
uses environment overrides or downloads executable code at runtime.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
META = ROOT / 'third_party/native-terminal'
HARDENING_POSTIMAGES = Path(__file__).with_name('terminal_hardening_postimages.json')
DEST = ROOT / 'internal/nativeterm/upstream'
PREFIX = 'github.com/egoist/mygo/plugins/terminal'
REPLACEMENT = 'github.com/laojianzi/aster/internal/nativeterm/upstream'
MARKER = '.aster-generated'


def sha(data):
    return hashlib.sha256(data).hexdigest()


def validate_module_pin(go_mod, spec):
    """The generated native adapter must use the framework linked by the app."""
    module = spec.get('module')
    version = spec.get('version')
    if module != 'github.com/egoist/mygo' or not isinstance(version, str):
        raise ValueError('unexpected terminal module pin')
    name = re.escape(module)
    declared = re.findall(r'(?m)^\s*(?:require\s+)?' + name +
                          r'\s+(v[^\s]+)\s*(?://[^\n]*)?$', go_mod)
    replaced = re.search(r'(?m)^\s*(?:replace\s+)?' + name +
                         r'(?:\s+[^\s]+)?\s*=>', go_mod)
    if declared != [version] or replaced:
        raise ValueError('MyGo module and terminal source pin differ; review both together')


def source_files(source, spec):
    result = {}
    for name, digest in spec['files'].items():
        relative = Path(name)
        if relative.is_absolute() or '..' in relative.parts:
            raise ValueError('unsafe upstream source path')
        with (source / relative).open("rb") as handle:
            data = handle.read((1 << 20) + 1)
        if len(data) > 1 << 20:
            raise ValueError("upstream source exceeds size budget")
        if sha(data) != digest:
            raise ValueError(f'pinned upstream source mismatch: {name}')
        result[name] = data
    return result


def hardening_inputs(spec):
    """Load reviewed patch outputs independently of the files we are preparing."""
    with HARDENING_POSTIMAGES.open('rb') as handle:
        policy_bytes = handle.read((32 << 10) + 1)
    with (META / 'hardening.patch').open('rb') as handle:
        patch_bytes = handle.read((1 << 20) + 1)
    if len(policy_bytes) > 32 << 10 or len(patch_bytes) > 1 << 20:
        raise ValueError('terminal hardening input exceeds size budget')
    policy = json.loads(policy_bytes)
    if (not isinstance(policy, dict) or
            set(policy) != {'module', 'version', 'patch_sha256', 'postimages'} or
            policy['module'] != spec['module'] or policy['version'] != spec['version'] or
            policy['patch_sha256'] != sha(patch_bytes)):
        raise ValueError('terminal hardening policy does not match the pinned patch')
    paths = []
    for line in patch_bytes.splitlines():
        if line.startswith(b'diff --git '):
            match = re.fullmatch(rb'diff --git a/(\S+) b/\1', line)
            if not match:
                raise ValueError('unsupported terminal hardening patch path')
            paths.append(match[1].decode('utf-8'))
    postimages = policy['postimages']
    if (not paths or len(paths) != len(set(paths)) or not isinstance(postimages, dict) or
            set(paths) != set(postimages) or not set(paths) <= set(spec['files'])):
        raise ValueError('terminal hardening postimages do not cover the exact patch')
    for name, digest in postimages.items():
        if (not isinstance(digest, str) or not re.fullmatch(r'[0-9a-f]{64}', digest) or
                digest == spec['files'][name]):
            raise ValueError('invalid terminal hardening postimage')
    return patch_bytes, policy_bytes, postimages


def without_inherited_git_environment():
    return {key: value for key, value in os.environ.items()
            if not key.upper().startswith('GIT_')}


def isolated_git_environment(workspace):
    """Do not discover a containing checkout or inherit a caller's Git target."""
    ceiling = str(workspace.resolve())
    if os.pathsep in ceiling:
        raise ValueError('temporary directory cannot safely isolate Git discovery')
    config = workspace / 'empty-git-config'
    config.write_bytes(b'')
    env = without_inherited_git_environment()
    env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=str(config),
               GIT_ATTR_NOSYSTEM='1', GIT_CEILING_DIRECTORIES=ceiling)
    return env


def verify_hardened_sources(stage, spec, postimages):
    """An exit-zero git apply can skip every path; require actual reviewed bytes."""
    actual = set()
    for path in stage.rglob('*'):
        if path.is_symlink() or not (path.is_file() or path.is_dir()):
            raise ValueError('unexpected terminal hardening output entry')
        if path.is_file():
            actual.add(path.relative_to(stage).as_posix())
    if actual != set(spec['files']):
        raise ValueError('terminal hardening output file inventory mismatch')
    expected = {**spec['files'], **postimages}
    source_files(stage, {'files': expected})


def format_generated_sources(stage):
    """Canonicalize after import rewriting, using this Go toolchain's formatter."""
    goroot = subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip()
    formatter = Path(goroot) / 'bin' / ('gofmt.exe' if platform.system() == 'Windows' else 'gofmt')
    paths = sorted(stage.rglob('*.go'))
    if not paths:
        raise ValueError('no generated Go source to format')
    subprocess.run([str(formatter), '-w', *map(str, paths)], check=True, timeout=60)


def host_platform():
    system = {'Linux': 'linux', 'Darwin': 'darwin', 'Windows': 'windows'}[platform.system()]
    machine = {'x86_64': 'amd64', 'AMD64': 'amd64', 'arm64': 'arm64', 'aarch64': 'arm64'}[platform.machine()]
    return system + '-' + machine


def prepare(args):
    spec = json.loads((META / 'UPSTREAM.json').read_text())
    validate_module_pin((ROOT / 'go.mod').read_text(), spec)
    patch_bytes, policy_bytes, postimages = hardening_inputs(spec)
    if args.source:
        source = args.source.resolve()
    else:
        env = without_inherited_git_environment()
        env['GOFLAGS'] = '-mod=mod'
        module = json.loads(subprocess.check_output(
            ['go', 'mod', 'download', '-json', spec['module'] + '@' + spec['version']], env=env, text=True))
        if module.get('Error') or not module.get('Dir'):
            raise ValueError('pinned Go module download failed')
        source = Path(module['Dir']) / 'plugins/terminal'
    files = source_files(source, spec)
    with tempfile.TemporaryDirectory(prefix='aster-terminal-') as temp:
        workspace = Path(temp).resolve()
        git_env = isolated_git_environment(workspace)
        stage = workspace / 'source'
        stage.mkdir()
        for name, data in files.items():
            target = stage / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
        # Snapshot the verified patch outside the generated tree. The ceiling
        # keeps even a repository-local TMPDIR outside its ancestor Git context.
        patch = workspace / 'hardening.patch'
        patch.write_bytes(patch_bytes)
        git = ['git', '-c', 'core.autocrlf=false',
               '-c', 'core.attributesFile=' + git_env['GIT_CONFIG_GLOBAL'], 'apply']
        subprocess.run([*git, '--check', str(patch)], cwd=stage, env=git_env,
                       check=True, timeout=60)
        subprocess.run([*git, str(patch)], cwd=stage, env=git_env,
                       check=True, timeout=60)
        verify_hardened_sources(stage, spec, postimages)
        for target in stage.rglob('*.go'):
            target.write_text(target.read_text(encoding='utf-8').replace(PREFIX, REPLACEMENT), encoding='utf-8', newline='\n')
        format_generated_sources(stage)
        shutil.copyfile(META / 'LICENSE.mygo', stage / 'LICENSE')
        (stage / MARKER).write_text('Generated by scripts/prepare_terminal.py; do not edit.\n', encoding='utf-8', newline='\n')
        generated = {str(p.relative_to(stage)).replace('\\', '/'): sha(p.read_bytes())
                     for p in sorted(stage.rglob('*')) if p.is_file()}
        DEST.parent.mkdir(parents=True, exist_ok=True)
        if DEST.exists():
            if DEST.is_symlink() or not (DEST / MARKER).is_file():
                raise ValueError('refusing to replace a non-generated directory')
            shutil.rmtree(DEST)
        shutil.copytree(stage, DEST)
    output = ROOT / '.aster-native'
    output.mkdir(exist_ok=True)
    manifest = json.loads(files['mygo-plugin.json'])
    target = args.platform or host_platform()
    lib = manifest['libraries'][0]['files'][target]
    name = lib['name']
    if Path(name).name != name or not lib['url'].startswith(
            'https://github.com/egoist/mygo/releases/download/libghostty-vt-befcdfd2c3a1/'):
        raise ValueError('unexpected native library manifest')
    if args.library_source:
        with (args.library_source / target / name).open("rb") as handle:
            data = handle.read((32 << 20) + 1)
    else:
        with urllib.request.urlopen(lib['url'], timeout=60) as response:
            data = response.read((32 << 20) + 1)
    if len(data) > 32 << 20 or sha(data) != lib['sha256']:
        raise ValueError('native library checksum or size mismatch')
    path = output / name
    path.write_bytes(data)
    (output / 'provenance.json').write_text(json.dumps({
        'module': spec['module'], 'version': spec['version'], 'platform': target,
        'patch_sha256': sha(patch_bytes),
        'hardening_postimages_sha256': sha(policy_bytes),
        'generated_files': generated, 'library': lib,
    }, indent=2) + '\n')
    if args.github_env:
        with open(args.github_env, 'a', encoding='utf-8') as f:
            f.write(f'ASTER_TEST_LIBRARY={path.resolve()}\n')
    print(f'Prepared {len(files)} verified upstream files and {target} library: {path}')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, help='explicit offline pinned terminal source')
    parser.add_argument('--library-source', type=Path, help='offline library root with platform subdirectories')
    parser.add_argument('--platform', help='explicit target for cross builds, otherwise this host')
    parser.add_argument('--github-env', help='GitHub Actions environment file')
    prepare(parser.parse_args())
