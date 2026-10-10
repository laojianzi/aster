#!/usr/bin/env python3
"""Temporary bounded bootstrap probe; no runtime OAuth responses are read."""
import re
import subprocess
import sys
import with_oidc_cluster as fixture

original = fixture.command

def checked(*args, **kwargs):
    try:
        return original(*args, **kwargs)
    except subprocess.CalledProcessError as failure:
        if args[:3] == ('kind', 'create', 'cluster'):
            # kind invokes kubeadm with --skip-token-print. Still scrub secret
            # formats and URL values from this pre-login diagnostic tail.
            summary = (failure.stderr or b'')[-10000:].decode(errors='replace')
            summary = re.sub(r'-----BEGIN[^-]*PRIVATE KEY-----.*?-----END[^-]*PRIVATE KEY-----', '[private material removed]', summary, flags=re.S)
            summary = re.sub(r'[a-z0-9]{6}\.[a-z0-9]{16}|[a-fA-F0-9]{64}', '[redacted]', summary)
            summary = re.sub(r'https?://\S+', '[endpoint]', summary)
            print('OIDC bootstrap diagnostic:\n' + summary, file=sys.stderr)
        raise

if __name__ == '__main__':
    fixture.command = checked
    try:
        fixture.main(sys.argv[1:])
    except Exception:
        sys.exit('OIDC setup probe failed; private fixtures remain suppressed')
