#!/usr/bin/env python3
"""Require the pinned MyGo source migration to be a no-op, including generated Go."""
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def snapshot(root=ROOT):
    result = {}
    for directory in ('cmd', 'internal', 'e2e'):
        for path in sorted((root / directory).rglob('*.go')):
            if path.is_symlink():
                raise ValueError('source symlink is not allowed')
            result[str(path.relative_to(root))] = hashlib.sha256(path.read_bytes()).hexdigest()
    if not result:
        raise ValueError('no Go source inspected')
    return result


if __name__ == '__main__':
    spec = json.loads((ROOT / 'third_party/native-terminal/UPSTREAM.json').read_text())
    version = subprocess.check_output(
        ['go', 'list', '-m', '-f', '{{.Version}}', 'github.com/egoist/mygo'],
        cwd=ROOT, text=True).strip()
    if version != spec['version'] or not version.startswith('v0.3.'):
        raise SystemExit('framework/native-source pins differ from the audited 0.3 contract')
    before = snapshot()
    subprocess.run(['go', 'run', 'github.com/egoist/mygo/cmd/mygo@' + version,
                    'migrate-ui', '-write', '.'], cwd=ROOT, check=True)
    after = snapshot()
    if before != after:
        changed = sorted(p for p in before.keys() | after.keys() if before.get(p) != after.get(p))
        raise SystemExit('migration still changes source: ' + ', '.join(changed))
    print(f'MYGO_MIGRATION_NOOP: {version}, {len(before)} Go files (including generated adapter)')
