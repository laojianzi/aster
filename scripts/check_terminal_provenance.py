#!/usr/bin/env python3
"""Offline gate for A2: shipped terminal provenance must describe the pinned build.

Read-only. No network, no source generation, no product source changes.
Exit 0 when every invariant holds, 1 with an explicit violation list otherwise.

Usage:
    python scripts/check_terminal_provenance.py
    python scripts/check_terminal_provenance.py --require-dist
    python scripts/check_terminal_provenance.py --root /path/to/aster

The default scope always checks the source side (go.mod pin, UPSTREAM,
hardening patch digest, generated tree, live provenance, native library) and
also checks dist artifacts whenever they exist. --require-dist additionally
fails when dist artifacts are absent; build jobs should use it, while
source-only jobs should not.

The native library file name is read from provenance['library']['name'] because
it differs per platform (libghostty-vt.so / libghostty-vt.dylib /
ghostty-vt.dll). Never hardcode a single platform suffix.

Wired into:
    .github/workflows/ci.yml                 desktop-build
    .github/workflows/native-migration.yml   source-audit, native-regressions
"""
import argparse
import hashlib
import json
import re
import sys
from pathlib import Path

META_SPEC = 'third_party/native-terminal/UPSTREAM.json'
META_PATCH = 'third_party/native-terminal/hardening.patch'
LIVE = '.aster-native/provenance.json'
DIST = 'dist/terminal-provenance.json'
DEST = 'internal/nativeterm/upstream'
MODULE = 'github.com/egoist/mygo'


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def main():
    # scripts/check_terminal_provenance.py -> parents[1] is the repository root.
    default_root = Path(__file__).resolve().parents[1]
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--root', type=Path, default=default_root,
                    help='repository root (defaults to the parent of scripts/)')
    ap.add_argument('--require-dist', action='store_true',
                    help='fail when dist/terminal-provenance.json or the dist library is absent')
    args = ap.parse_args()
    root = args.root.resolve()

    errors = []
    checks = []

    def check(name, ok, detail=''):
        checks.append((name, bool(ok), detail))
        if not ok:
            errors.append(name + (': ' + detail if detail else ''))

    def load(rel, required=True):
        p = root / rel
        if not p.is_file():
            if required:
                check('exists ' + rel, False, 'missing')
            return None
        try:
            return json.loads(p.read_text(encoding='utf-8'))
        except (ValueError, OSError) as exc:
            check('parse ' + rel, False, str(exc))
            return None

    dist_present = (root / DIST).is_file()
    spec = load(META_SPEC)
    live = load(LIVE)
    dist = load(DIST, required=False) if dist_present else None
    if args.require_dist:
        check('dist provenance present', dist_present,
              'missing; run prepare_terminal.py then copy the live provenance into dist/')

    if spec is None or live is None or (dist_present and dist is None):
        print('FAIL: required JSON inputs missing or unparseable')
        for e in errors:
            print('  - ' + e)
        return 1

    # 1. The pinned version must be identical across go.mod, UPSTREAM and live.
    gomod_path = root / 'go.mod'
    check('go.mod present', gomod_path.is_file())
    gomod = gomod_path.read_text(encoding='utf-8') if gomod_path.is_file() else ''
    # Same shape as scripts/prepare_terminal.py:validate_module_pin. Handles both
    # `require X vN` and the `require ( ... )` block, and rejects `replace`.
    declared = re.findall(
        r'(?m)^\s*(?:require\s+)?' + re.escape(MODULE) +
        r'\s+(v[^\s]+)\s*(?://[^\n]*)?$', gomod)
    replaced = re.search(
        r'(?m)^\s*(?:replace\s+)?' + re.escape(MODULE) + r'(?:\s+[^\s]+)?\s*=>', gomod)
    check('go.mod declares exactly one mygo pin', len(declared) == 1, repr(declared))
    check('go.mod has no mygo replace directive', replaced is None)
    version = spec.get('version')
    check('UPSTREAM version present', isinstance(version, str), repr(version))
    if declared and isinstance(version, str):
        check('go.mod == UPSTREAM version', declared[0] == version,
              f'{declared[0]} vs {version}')
        check('live provenance == pinned version', live.get('version') == version,
              f'{live.get("version")} vs {version}')
    check('UPSTREAM module identity', spec.get('module') == MODULE, repr(spec.get('module')))
    check('live module identity', live.get('module') == MODULE, repr(live.get('module')))

    # 2. The hardening patch digest must describe the patch actually on disk.
    patch = root / META_PATCH
    check('hardening.patch present', patch.is_file())
    patch_hash = sha(patch) if patch.is_file() else None
    if patch_hash:
        check('live patch_sha256 == on-disk patch', live.get('patch_sha256') == patch_hash,
              f'{live.get("patch_sha256")} vs {patch_hash}')

    # 3. Every recorded generated file matches the on-disk tree, with no extras.
    generated = live.get('generated_files') or {}
    check('generated_files non-empty', bool(generated), f'{len(generated)} entries')
    bad, missing = [], []
    for name, digest in generated.items():
        p = root / DEST / name
        if not p.is_file():
            missing.append(name)
        elif sha(p) != digest:
            bad.append(name)
    check('all generated file hashes match', not bad, ','.join(bad[:6]))
    check('no generated file missing', not missing, ','.join(missing[:6]))
    on_disk = {
        str(p.relative_to(root / DEST)).replace('\\', '/')
        for p in (root / DEST).rglob('*') if p.is_file()
    } if (root / DEST).is_dir() else set()
    extra = sorted(on_disk - set(generated))
    check('no unrecorded generated file', not extra, ','.join(extra[:6]))

    # 4. Native library identity. The file name is platform specific and comes
    #    from the provenance itself, which prepare_terminal.py writes from the
    #    pinned mygo-plugin.json manifest.
    lib = live.get('library') or {}
    lib_digest = lib.get('sha256')
    lib_name = lib.get('name')
    name_ok = (isinstance(lib_name, str) and bool(lib_name)
               and lib_name not in ('.', '..')
               and Path(lib_name).name == lib_name)
    check('library sha256 declared', isinstance(lib_digest, str), repr(lib_digest))
    check('library name safe', name_ok, repr(lib_name))
    if isinstance(lib_digest, str) and name_ok:
        live_lib = root / '.aster-native' / lib_name
        check(f'.aster-native/{lib_name} matches declared library hash',
              live_lib.is_file() and sha(live_lib) == lib_digest,
              'missing' if not live_lib.is_file() else sha(live_lib))

    # 5. The dist copy is never a stale description of another build.
    if args.require_dist or dist_present:
        if dist is not None:
            if isinstance(version, str):
                check('dist provenance == pinned version', dist.get('version') == version,
                      f'{dist.get("version")} vs {version}')
            check('dist module identity', dist.get('module') == MODULE,
                  repr(dist.get('module')))
            if patch_hash:
                check('dist patch_sha256 == on-disk patch', dist.get('patch_sha256') == patch_hash,
                      f'{dist.get("patch_sha256")} vs {patch_hash}')
            check('dist provenance identical to live', dist == live,
                  're-run prepare_terminal.py and copy .aster-native/provenance.json')
            if isinstance(lib_digest, str) and name_ok:
                dist_lib = root / 'dist' / lib_name
                check(f'dist/{lib_name} matches declared library hash',
                      dist_lib.is_file() and sha(dist_lib) == lib_digest,
                      'missing' if not dist_lib.is_file() else sha(dist_lib))
        else:
            check('dist provenance readable', False, 'unparseable')
    else:
        print('  [SKIP] dist artifacts not present (source-only scope)')

    passed = sum(1 for _, ok, _ in checks if ok)
    for name, ok, detail in checks:
        print(f'  [{"PASS" if ok else "FAIL"}] {name}'
              + (f'  ({detail})' if detail and not ok else ''))
    print(f'\nA2 provenance gate: {passed}/{len(checks)} passed'
          f'  | scope={"require-dist" if args.require_dist else "source+optional-dist"}'
          f'  | root={root}')
    if errors:
        print('VIOLATIONS:')
        for e in errors:
            print('  - ' + e)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
