#!/usr/bin/env python3
"""Disposable TLS Dex + OIDC-enabled kind; never reads a user's kubeconfig."""
import json
import os
from pathlib import Path
import ssl
import subprocess
import sys
import tempfile
import time
import urllib.request

DEX = 'ghcr.io/dexidp/dex:v2.46.0@sha256:933fcd3f523338c847b88ef84a7145fb2fcb7b9917f08418612afeaaa2001777'
NODE = 'kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5'
ROOT = Path(__file__).resolve().parents[1]


def command(*args, **kw):
    return subprocess.run(args, check=True, timeout=180, stdout=subprocess.PIPE, stderr=subprocess.PIPE, **kw).stdout.decode()


def main(args):
    if os.environ.get('ASTER_OIDC_E2E') != '1' or not args:
        raise ValueError('explicit OIDC fixture opt-in and test command required')
    output = Path(os.environ['ASTER_TEST_ARTIFACTS']).resolve()
    output.mkdir(parents=True, exist_ok=True)
    name = 'aster-e2e-oidc-' + str(os.getpid())
    with tempfile.TemporaryDirectory(prefix='aster-oidc-fixture-') as tmp:
        tmp = Path(tmp)
        stage = 'network'
        ready = False
        try:
            if not command('docker', 'network', 'ls', '--filter', 'name=^kind$', '--format', '{{.Name}}').strip():
                command('docker', 'network', 'create', 'kind')
            network = json.loads(command('docker', 'network', 'inspect', 'kind'))[0]
            gateway = next(x['Gateway'] for x in network['IPAM']['Config'] if ':' not in x.get('Gateway', ''))
            issuer = 'https://' + gateway + ':15556/dex'
            stage = 'certificate'
            command('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=Aster disposable OIDC',
                '-addext', 'subjectAltName=IP:' + gateway + ',IP:127.0.0.1', '-keyout', str(tmp/'key.pem'), '-out', str(tmp/'cert.pem'))
            config = {'issuer': issuer, 'storage': {'type': 'memory'},
                'web': {'https': gateway + ':15556', 'tlsCert': '/fixture/cert.pem', 'tlsKey': '/fixture/key.pem'},
                'expiry': {'idTokens': '30s'},
                'oauth2': {'responseTypes': ['code'], 'skipApprovalScreen': False, 'pkce': {'enforce': True, 'codeChallengeMethodsSupported': ['S256']}},
                'staticClients': [{'id': 'aster-test', 'name': 'Aster disposable test', 'public': True, 'redirectURIs': ['http://127.0.0.1:17111/oidc/callback']}],
                'enablePasswordDB': True,
                'staticPasswords': [{'email': 'engineer@example.test', 'username': 'engineer', 'userID': 'fixture-engineer', 'emailVerified': True,
                    'hash': '$2a$10$2b2cU8CPhOTaGrs1HRQuAueS7JTT5ZHsHSzYiFPm1leZck7Mc8T4W'}]}
            (tmp/'dex.json').write_text(json.dumps(config))
            stage = 'provider-image'
            command('docker', 'pull', DEX)
            digest = json.loads(command('docker', 'image', 'inspect', DEX))[0]['RepoDigests']
            (output/'provider.json').write_text(json.dumps({'requested_image': DEX, 'resolved_digests': digest, 'node': NODE, 'user_agent': 'bounded HTML form driver; not rendered browser'}, indent=2))
            stage = 'provider-start'
            command('docker', 'run', '-d', '--name', name, '--network', 'host', '--user', '0:0', '-v', str(tmp)+':/fixture:ro', DEX, 'dex', 'serve', '/fixture/dex.json')
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(tmp/'cert.pem'))))
            stage = 'provider-readiness'
            deadline = time.monotonic()+40
            while True:
                try:
                    with opener.open(issuer+'/.well-known/openid-configuration', timeout=2) as res:
                        if res.status == 200:
                            ready = True
                            break
                except Exception:
                    if time.monotonic() > deadline:
                        raise ValueError('disposable Dex did not become ready')
                    time.sleep(.3)
            stage = 'cluster-configuration'
            # The extra file is outside kubeadm's read-only PKI directory mount.
            patch = {'apiVersion': 'kubeadm.k8s.io/v1beta4', 'kind': 'ClusterConfiguration', 'apiServer': {
                'extraArgs': [{'name': k, 'value': v} for k,v in {
                    'oidc-issuer-url': issuer, 'oidc-client-id': 'aster-test', 'oidc-ca-file': '/etc/aster-oidc.pem',
                    'oidc-username-claim': 'email', 'oidc-username-prefix': 'aster:'}.items()],
                'extraVolumes': [{'name': 'aster-oidc-ca', 'hostPath': '/aster-oidc.pem', 'mountPath': '/etc/aster-oidc.pem', 'readOnly': True, 'pathType': 'File'}]}}
            kind = {'kind': 'Cluster', 'apiVersion': 'kind.x-k8s.io/v1alpha4', 'nodes': [{'role': 'control-plane',
                'extraMounts': [{'hostPath': str(tmp/'cert.pem'), 'containerPath': '/aster-oidc.pem', 'readOnly': True}], 'kubeadmConfigPatches': [json.dumps(patch)]}]}
            (tmp/'kind.json').write_text(json.dumps(kind))
            stage = 'cluster-start'
            command('kind', 'create', 'cluster', '--retain', '--name', name, '--image', NODE, '--config', str(tmp/'kind.json'), '--kubeconfig', str(tmp/'kubeconfig'), '--wait', '120s')
            env = dict(os.environ, KUBECONFIG=str(tmp/'kubeconfig'), ASTER_E2E_CONTEXT='kind-'+name, ASTER_E2E_ALLOW_DESTRUCTIVE='1',
                ASTER_OIDC_ISSUER=issuer, ASTER_OIDC_CA=str(tmp/'cert.pem'), ASTER_OIDC_PORT='17111')
            stage = 'test-execution'
            result = subprocess.run(args, cwd=ROOT, env=env, timeout=300)
            if result.returncode:
                raise ValueError('OIDC test command failed')
        except Exception as failure:
            (output/'setup-failure.json').write_text(json.dumps({'phase': stage, 'error_type': type(failure).__name__,
                'returncode': getattr(failure, 'returncode', None)}, indent=2))
            if not ready:
                # No login has run, and there are no authorization codes/tokens.
                startup = subprocess.run(['docker', 'logs', '--tail', '20', name],
                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=10).stdout[:8192].decode(errors='replace')
                safe = [line[:1000] for line in startup.splitlines()
                        if any(word in line.lower() for word in ('error', 'unknown', 'invalid', 'failed', 'fatal'))]
                (output/'provider-startup-errors.txt').write_text('\n'.join(safe))
            raise
        finally:
            # --retain permits bounded diagnostics only; every path deletes it.
            subprocess.run(['kind','delete','cluster','--name',name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=60)
            subprocess.run(['docker','rm','-f',name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)


if __name__ == '__main__':
    try:
        main(sys.argv[1:])
    except Exception:
        sys.exit('Disposable OIDC qualification failed; private fixtures and request URLs are not printed')
