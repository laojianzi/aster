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
APPROVED = {25: BRANCH, 28: 'feat/native-oidc-renewal'}
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


def workflows_pass(runs, head):
    for name in REQUIRED:
        matches = sorted((r for r in runs if r.get('name') == name and r.get('head_sha') == head
                          and r.get('event') == 'push' and r.get('head_branch') == 'main'),
                         key=lambda r: r['id'], reverse=True)
        if not matches or matches[0].get('status') != 'completed' or matches[0].get('conclusion') != 'success':
            return False
    return True


def run(report):
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
    if git('rev-parse', 'HEAD') != head or api('git/ref/heads/main')['object']['sha'] != head:
        raise ValueError('main moved')
    report['head'] = head
    number = select_scope([api('pulls/' + str(n)) for n in APPROVED], head)
    if number is None:
        report['status'] = 'no-registered-completion'
        return
    branch = APPROVED[number]
    receipt = 'refs/tags/maintenance/completed-pr-' + str(number)
    report['pr'] = number
    if git('ls-remote', REMOTE, receipt):
        report['status'] = 'already-recorded'
        return
    runs = api('actions/runs?head_sha=' + head + '&event=push&per_page=100')['workflow_runs']
    if not workflows_pass(runs, head):
        report['status'] = 'waiting-for-verification'
        return
    def preflight():
        return candidate(api('pulls/' + str(number)), head,
             {b['name']: b for b in collection('branches')}, collection('pulls?state=open'),
             lambda sha: git('merge-base', '--is-ancestor', sha, head, predicate=True),
             number=number, branch=branch)
    sha = preflight()
    if sha is None:
        report['status'] = 'already-absent'
        return
    report['branch'], report['expected_head'] = branch, sha
    git('bundle', 'create', str(ROOT/'artifacts/completed-branch/before.bundle'), '--all')
    if preflight() != sha or api('git/ref/heads/main')['object']['sha'] != head:
        raise ValueError('preflight changed')
    ref = 'refs/heads/' + branch
    git('push', '--atomic', '--porcelain', '--force-with-lease=' + ref + ':' + sha,
        '--force-with-lease=' + receipt + ':', REMOTE, ':' + ref, head + ':' + receipt)
    remaining = [b['name'] for b in collection('branches')]
    if branch in remaining:
        raise ValueError('deletion was not observed')
    report.update(status='applied', remaining=remaining, receipt=receipt)


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
