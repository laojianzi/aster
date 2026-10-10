#!/usr/bin/env python3
"""Temporary bounded pre-login setup probe; never reads runtime OAuth logs."""
import json
import re
import subprocess
import sys
import with_oidc_cluster as fixture

original = fixture.command

def scrub(text):
    text = re.sub(r'-----BEGIN[^-]*PRIVATE KEY-----.*?-----END[^-]*PRIVATE KEY-----', '[private material removed]', text, flags=re.S)
    text = re.sub(r'[a-z0-9]{6}\.[a-z0-9]{16}|[a-fA-F0-9]{64}', '[redacted]', text)
    return re.sub(r'https?://\S+', '[endpoint]', text)

def checked(*args, **kwargs):
    try:
        return original(*args, **kwargs)
    except subprocess.CalledProcessError as failure:
        if args[:3] == ('kind', 'create', 'cluster'):
            node = args[args.index('--name')+1] + '-control-plane'
            print('OIDC bootstrap diagnostic:\n' + scrub((failure.stderr or b'')[-3000:].decode(errors='replace')), file=sys.stderr)
            result = subprocess.run(['docker','exec',node,'crictl','ps','-a','-o','json'],capture_output=True,timeout=15)
            if result.returncode == 0:
                containers = json.loads(result.stdout)['containers']
                api = next((c['id'] for c in containers if c['metadata']['name'] == 'kube-apiserver'), None)
                if api:
                    logs = subprocess.run(['docker','exec',node,'crictl','logs','--tail=20',api],capture_output=True,timeout=15)
                    print('Pre-login API startup:\n' + scrub(logs.stdout[-8000:].decode(errors='replace')), file=sys.stderr)
            journal = subprocess.run(['docker','exec',node,'journalctl','-u','kubelet','--no-pager','-n','40'],capture_output=True,timeout=15)
            lines = [l for l in journal.stdout.decode(errors='replace').splitlines() if any(k in l.lower() for k in ('error','failed','invalid'))]
            print('Pre-login kubelet errors:\n' + scrub('\n'.join(lines)[-8000:]), file=sys.stderr)
        raise

if __name__ == '__main__':
    fixture.command = checked
    try:
        fixture.main(sys.argv[1:])
    except Exception:
        sys.exit('OIDC setup probe failed; private fixtures remain suppressed')
