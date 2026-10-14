#!/usr/bin/env python3
"""Fetch only the embedded approved Helm distribution for test/development use.

The desktop never downloads tools. It independently rechecks these bytes before
execution. This helper does not execute Helm or fetch a chart/values file.
"""
import argparse
import hashlib
import json
from pathlib import Path
import platform
import re
import shutil
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
LIMIT = 64 << 20


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError('Helm distribution redirect refused')


def verified(data, digest):
    return 0 < len(data) <= LIMIT and hashlib.sha256(data).hexdigest() == digest


def prepare(target=None):
    manifest = json.loads((ROOT / 'internal/helmrender/distributions.json').read_text())
    system = {'Linux': 'linux', 'Darwin': 'darwin', 'Windows': 'windows'}[platform.system()]
    machine = {'x86_64': 'amd64', 'AMD64': 'amd64', 'arm64': 'arm64', 'aarch64': 'arm64'}[platform.machine()]
    item = manifest['platforms'][system + '-' + machine]
    extension = '.zip' if system == 'windows' else '.tar.gz'
    expected = 'helm-' + manifest['version'] + '-' + system + '-' + machine + extension
    if (manifest['version'] != 'v4.3.0' or item['file'] != expected
            or not re.fullmatch(r'[0-9a-f]{64}', item['sha256'])):
        raise ValueError('invalid approved Helm distribution specification')
    path = Path(target) if target else ROOT / '.aster-helm' / item['file']
    path.parent.mkdir(parents=True, exist_ok=True)
    if not path.is_file() or path.stat().st_size > LIMIT or not verified(path.read_bytes(), item['sha256']):
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        request = urllib.request.Request('https://get.helm.sh/' + item['file'])
        with opener.open(request, timeout=60) as response:
            data = response.read(LIMIT + 1)
        if not verified(data, item['sha256']):
            raise ValueError('Helm distribution digest/size mismatch')
        path.write_bytes(data)
    return path.resolve(), manifest, item


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--github-env')
    parser.add_argument('--evidence-dir')
    parser.add_argument('--archive-evidence', action='store_true')
    args = parser.parse_args()
    path, manifest, item = prepare()
    receipt = dict(version=manifest['version'], file=item['file'], sha256=item['sha256'], size=path.stat().st_size)
    (path.parent / 'provenance.json').write_text(json.dumps(receipt, indent=2) + '\n')
    if args.github_env:
        if '\n' in str(path) or '\r' in str(path):
            raise ValueError('invalid output path')
        with open(args.github_env, 'a', encoding='utf-8') as stream:
            stream.write('ASTER_HELM_ARCHIVE=' + str(path) + '\n')
    if args.evidence_dir:
        evidence = Path(args.evidence_dir)
        evidence.mkdir(parents=True, exist_ok=True)
        (evidence / 'provenance.json').write_text(json.dumps(receipt, indent=2) + '\n')
        if args.archive_evidence:
            shutil.copyfile(path, evidence / item['file'])
    print(json.dumps(receipt))


if __name__ == '__main__':
    main()
