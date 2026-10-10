#!/usr/bin/env python3
"""Temporary bounded setup probe; never prints raw commands or login responses."""
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
            text = (failure.stderr or b'')[:8192].decode(errors='replace')
            lines = [line for line in text.splitlines() if line.startswith('ERROR:')]
            # Bootstrap material is disposable but is not diagnostic output.
            summary = '\n'.join(lines)[:1500]
            summary = re.sub(r'[a-z0-9]{6}\.[a-z0-9]{16}|[a-fA-F0-9]{64}', '[redacted]', summary)
            summary = re.sub(r'https?://\S+', '[endpoint]', summary)
            print('OIDC bootstrap summary: ' + summary, file=sys.stderr)
        raise

if __name__ == '__main__':
    fixture.command = checked
    try:
        fixture.main(sys.argv[1:])
    except Exception:
        sys.exit('OIDC setup probe failed; private fixtures remain suppressed')
