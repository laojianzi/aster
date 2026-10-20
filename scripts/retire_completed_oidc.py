#!/usr/bin/env python3
"""Retire an explicitly registered completion branch with an exact atomic lease.

One existing workflow handles reviewed scopes; unregistered branches are never
inferred from names, deleted by age, or accepted from workflow inputs.
"""
import base64
import json
import os
from pathlib import Path
import re
import subprocess
import urllib.request
from branch_cleanup import NoRedirect

REPO = 'laojianzi/aster'
BRANCH = 'feat/native-oidc-login'
PR = 25
APPROVED = {25: BRANCH, 28: 'feat/native-oidc-renewal', 30: 'feat/native-diagnostic-navigation', 31: 'fix/dependency-dashboard-mygo', 33: 'renovate/mygo-native-framework', 34: 'feat/native-manifest-find', 36: 'feat/native-manifest-buffer', 38: 'feat/native-oidc-signout', 39: 'renovate/mygo-native-framework', 40: 'fix/dependency-dashboard-autoclose', 41: 'renovate/mygo-native-framework', 43: 'feat/native-schema-constraints', 45: 'feat/reviewed-manifest-delivery', 47: 'feat/native-schema-multipleof', 49: 'feat/native-schema-pattern', 51: 'feat/native-schema-format', 53: 'feat/native-schema-structured-enum', 55: 'feat/native-schema-list-map', 57: 'feat/native-schema-list-set', 58: 'renovate/mygo-native-framework', 60: 'feat/native-schema-integer-map-keys', 62: 'feat/native-schema-integer-set', 64: 'feat/manifest-clipboard-preflight', 66: 'feat/native-schema-boolean-collections', 68: 'feat/local-helm-render-delivery', 70: 'feat/helm-release-metadata', 72: 'feat/helm-revision-inspection', 73: 'renovate/github.com-go-jose-go-jose-v4-4.x', 74: 'renovate/mygo-native-framework', 75: 'renovate/mygo-native-framework', 77: 'feat/helm-uninstall-scope-preview', 79: 'feat/helm-live-observation', 81: 'test/helm-uninstall-contract', 82: 'fix/helm-uninstall-absence', 83: 'test/helm-uninstall-contract', 85: 'feat/helm-release-status-transition', 87: 'feat/helm-uninstall-preparation', 89: 'feat/helm-uninstall-dry-run', 91: 'feat/helm-uninstall-execution', 93: 'feat/helm-uninstall-recovery', 95: 'feat/native-helm-uninstall'}
DEPENDENCY_SCOPES = (31, 33, 39, 40, 41, 58, 73, 74, 75, 85)
REMOTE = 'https://github.com/' + REPO + '.git'
ROOT = Path(__file__).resolve().parents[1]
REQUIRED = ('CI', 'Source evidence', 'Native migration verification', 'OIDC qualification')


def select_scope(prs, head):
    matches = [p for p in prs if p.get('number') in APPROVED and p.get('merged')
               and p.get('state') == 'closed' and p.get('merge_commit_sha') == head]
    if len(matches) > 1:
        raise ValueError('ambiguous completion merge')
    return matches[0]['number'] if matches else None


def candidate(pr, head, branches, active, ancestor, *, number=PR, branch=BRANCH):
    if APPROVED.get(number) != branch:
        raise ValueError('unregistered retirement scope')
    h, b = pr.get('head', {}), pr.get('base', {})
    sha = h.get('sha', '')
    if (pr.get('number') != number or pr.get('state') != 'closed' or not pr.get('merged')
            or pr.get('merge_commit_sha') != head or h.get('ref') != branch
            or b.get('ref') != 'main' or (h.get('repo') or {}).get('full_name') != REPO
            or (b.get('repo') or {}).get('full_name') != REPO
            or re.fullmatch('[a-f0-9]{40}', sha) is None):
        raise ValueError('not the exact reviewed completion merge')
    current = branches.get(branch)
    if current is None:
        return None
    if current.get('protected') or current.get('commit', {}).get('sha') != sha:
        raise ValueError('branch moved or is protected')
    if any(p[s].get('ref') == branch and (p[s].get('repo') or {}).get('full_name') == REPO
           for p in active for s in ('head', 'base')):
        raise ValueError('branch has an active PR')
    if not ancestor(sha):
        raise ValueError('branch history not reachable from main')
    return sha


def workflows_pass(runs, head, *, extra=()):
    for name in (*REQUIRED, *extra):
        matches = sorted((r for r in runs if r.get('name') == name and r.get('head_sha') == head
                          and r.get('event') == 'push' and r.get('head_branch') == 'main'),
                         key=lambda r: r['id'], reverse=True)
        if not matches or matches[0].get('status') != 'completed' or matches[0].get('conclusion') != 'success':
            return False
    return True


def run(report):
    report['phase'] = 'initialize'
    report['phases'] = ['initialize']
    head, token = os.environ.get('ASTER_CLEANUP_HEAD', ''), os.environ.get('GH_TOKEN', '')
    if re.fullmatch('[a-f0-9]{40}', head) is None or not token:
        raise ValueError('trusted exact head and token required')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    def api(path):
        req = urllib.request.Request('https://api.github.com/repos/' + REPO + '/' + path,
              headers={'Authorization': 'Bearer ' + token, 'Accept': 'application/vnd.github+json',
                       'X-GitHub-Api-Version': '2022-11-28'})
        with opener.open(req, timeout=20) as res:
            data = res.read((4 << 20) + 1)
        if len(data) > 4 << 20:
            raise ValueError('metadata budget exceeded')
        return json.loads(data)
    def collection(path):
        data = api(path + ('&' if '?' in path else '?') + 'per_page=100')
        if len(data) >= 100:
            raise ValueError('collection exceeds this scoped cleanup budget')
        return data
    env = {k:v for k,v in os.environ.items() if not k.startswith(('GIT_CONFIG_', 'GIT_TRACE', 'GIT_CURL'))}
    basic = base64.b64encode(('x-access-token:' + token).encode()).decode()
    env.update(GIT_TERMINAL_PROMPT='0', GIT_CONFIG_COUNT='1',
               GIT_CONFIG_KEY_0='http.https://github.com/.extraheader',
               GIT_CONFIG_VALUE_0='AUTHORIZATION: basic ' + basic)
    def git(*args, predicate=False):
        p = subprocess.run(['git', *args], cwd=ROOT, env=env, capture_output=True, text=True, timeout=120)
        if predicate and p.returncode == 1:
            return False
        if p.returncode:
            raise ValueError('Git operation failed')
        return True if predicate else p.stdout.strip()
    retire_registered(report, head, api, collection, git, ROOT)


def retire_registered(report, head, api, collection, git, root):
    """Run one registered current-merge retirement; adapters allow offline tests.

    All phase names and errors are fixed public diagnostics. Never put transport
    exception text, subprocess output, environment or credentials in the report.
    """
    def phase(name):
        report['phase'] = name
        report.setdefault('phases', []).append(name)

    def same_main():
        if git('rev-parse', 'HEAD') != head or api('git/ref/heads/main')['object']['sha'] != head:
            raise ValueError('main moved')

    phase('verify-main')
    same_main()
    report['head'] = head
    phase('select-scope')
    number = select_scope([api('pulls/' + str(n)) for n in APPROVED], head)
    if number is None:
        report['status'] = 'no-registered-completion'
        return
    branch = APPROVED[number]
    receipt = 'refs/tags/maintenance/completed-pr-' + str(number)
    report.update(pr=number, branch=branch, receipt=receipt)

    def verified_gates():
        runs = api('actions/runs?head_sha=' + head + '&event=push&per_page=100')['workflow_runs']
        return workflows_pass(runs, head, extra=('Renovate configuration',) if number in DEPENDENCY_SCOPES else ())

    def preflight():
        pr = api('pulls/' + str(number))
        branches = {b['name']: b for b in collection('branches')}
        active = collection('pulls?state=open')
        ancestor = lambda sha: git('merge-base', '--is-ancestor', sha, head, predicate=True)
        tip = candidate(pr, head, branches, active, ancestor, number=number, branch=branch)
        # candidate validates metadata even for an absent branch. Completed
        # receipt reconciliation also requires ancestry and no active reference.
        if tip is None:
            if any(p[s].get('ref') == branch and (p[s].get('repo') or {}).get('full_name') == REPO
                   for p in active for s in ('head', 'base')):
                raise ValueError('absent branch has an active PR')
            if not ancestor(pr['head']['sha']):
                raise ValueError('completed history not reachable from main')
        return tip

    def receipt_sha():
        data = git('ls-remote', '--refs', REMOTE, receipt)
        if not data:
            return None
        fields = data.split()
        if len(fields) != 2 or fields[1] != receipt or re.fullmatch('[a-f0-9]{40}', fields[0]) is None:
            raise ValueError('ambiguous receipt reference')
        return fields[0]

    def confirm_effects():
        # Reads only. A transport failure cannot cause a second push.
        same_main()
        observed = report['confirmation'] = {'main_matches': True}
        observed['gates_passed'] = verified_gates()
        if not observed['gates_passed']:
            raise ValueError('verification changed after write')
        observed['branch_absent'] = preflight() is None
        observed['receipt_matches'] = receipt_sha() == head
        if not observed['branch_absent'] or not observed['receipt_matches']:
            raise ValueError('atomic retirement effects not established')

    phase('verify-gates')
    if not verified_gates():
        report['status'] = 'waiting-for-verification'
        return
    phase('preflight')
    sha = preflight()
    existing = receipt_sha()
    if existing is not None:
        phase('verify-existing-receipt')
        if existing != head or sha is not None:
            raise ValueError('receipt does not prove completed retirement')
        confirm_effects()
        report['status'] = 'already-recorded'
        return
    if sha is None:
        report['status'] = 'already-absent'
        return
    report['expected_head'] = sha
    phase('archive')
    git('bundle', 'create', str(root/'artifacts/completed-branch/before.bundle'), '--all')
    phase('recheck-preflight')
    if preflight() != sha or receipt_sha() is not None:
        raise ValueError('preflight changed')
    same_main()
    if not verified_gates():
        raise ValueError('verification changed before write')
    ref = 'refs/heads/' + branch
    phase('atomic-write')
    report['status'] = 'outcome-unknown'
    try:
        git('push', '--atomic', '--porcelain', '--force-with-lease=' + ref + ':' + sha,
            '--force-with-lease=' + receipt + ':', REMOTE, ':' + ref, head + ':' + receipt)
    except Exception as exc:
        # Keep the original failure class; never leak exception messages which
        # may contain credentials. Reconcile exact effects without repeating it.
        report['write_error_type'] = type(exc).__name__
        phase('reconcile-write')
        confirm_effects()
        report['status'] = 'applied-reconciled'
    else:
        phase('confirm-write')
        confirm_effects()
        report['status'] = 'applied'
    phase('complete')


if __name__ == '__main__':
    report = {'status': 'not-applied'}
    target = ROOT/'artifacts/completed-branch/report.json'
    target.parent.mkdir(parents=True, exist_ok=True)
    try:
        run(report)
    except Exception as e:
        report['error_type'] = type(e).__name__
        raise SystemExit('Completed-branch retirement refused; inspect metadata, not credentials') from None
    finally:
        target.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))
