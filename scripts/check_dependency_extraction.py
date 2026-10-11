#!/usr/bin/env python3
"""Check actual pinned Renovate JSONL extraction; never infer from a green exit."""
import json
from pathlib import Path
import sys

MODULE = 'github.com/egoist/mygo'
SPEC = 'third_party/native-terminal/UPSTREAM.json'


def check(records, version):
    snapshots = []
    for record in records:
        if not isinstance(record, dict):
            raise ValueError('invalid Renovate log record')
        if isinstance(record.get('level'), int) and record['level'] >= 50:
            raise ValueError('Renovate reported an error')
        # Renovate logs the extracted packageFiles map. A config string or a
        # mention of a module in prose is deliberately not accepted as evidence.
        files = record.get('packageFiles')
        if isinstance(files, dict):
            snapshots.append(files)
    if not snapshots:
        raise ValueError('no actual packageFiles extraction snapshot')
    found = []
    for manager, files in snapshots[-1].items():
        for package in files:
            for dep in package.get('deps', []):
                if dep.get('depName') == MODULE:
                    if dep.get('skipReason') or dep.get('enabled') is False:
                        raise ValueError('MyGo extraction is skipped or disabled')
                    found.append((manager, package.get('packageFile'), dep.get('currentValue')))
    expected = [('gomod', 'go.mod', version), ('regex', SPEC, version)]
    if sorted(found) != sorted(expected):
        raise ValueError('expected exactly the two enabled, matching MyGo pins')
    return {'mygo': version, 'extractions': [dict(manager=m, path=p, version=v) for m,p,v in found],
            'scope': 'actual Renovate extract-only; no hosted lookup or write'}


def main():
    path = Path(sys.argv[1])
    if path.stat().st_size > 32 << 20:
        raise ValueError('Renovate log budget exceeded')
    records = [json.loads(line) for line in path.read_text().splitlines() if line.strip()]
    version = json.loads((Path(__file__).resolve().parents[1] / SPEC).read_text())['version']
    print(json.dumps(check(records, version), indent=2))


if __name__ == '__main__':
    main()
