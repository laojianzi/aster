#!/usr/bin/env python3
"""Retire only reviewed refs using one atomic, exact-lease push. No PR code executes."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import urllib.request

REPO = 'laojianzi/aster'
REMOTE = 'https://github.com/' + REPO + '.git'
PLAN_PATH = 'docs/branch-cleanup-2026-10-10.json'
RECEIPT = 'refs/tags/maintenance/branch-cleanup-2026-10-10'
ARCHIVE = 'refs/tags/archive/2026-10-10/'
ROOT = Path(__file__).resolve().parents[1]


def validate_plan(plan):
    if plan.get('repository') != REPO or plan.get('default_branch') != 'main':
        raise ValueError('unexpected repository/default branch')
    if not re.fullmatch(r'[0-9a-f]{40}', plan.get('baseline', '')):
        raise ValueError('missing exact reviewed baseline')
    entries = plan.get('branches', [])
    if not 1 <= len(entries) <= 32:
        raise ValueError('invalid candidate count')
    seen = set()
    for entry in entries:
        name, sha = entry.get('name', ''), entry.get('sha', '')
        if (not re.fullmatch(r'[a-z][a-z0-9./-]*[a-z0-9]', name)
                or '//' in name or '..' in name or '/.' in name or name.endswith('.lock') or name == 'main' or name in seen
                or not re.fullmatch(r'[0-9a-f]{40}', sha)):
            raise ValueError('unsafe or duplicate candidate ref')
        if entry.get('mode') not in ('merged', 'archive-superseded') or not entry.get('reason'):
            raise ValueError('missing retirement decision')
        seen.add(name)
    if plan.get('merging_branch') != 'fix/mygo-03-widget-lifetimes':
        raise ValueError('unexpected completion branch')
    return entries


def classify(entries, branches, prs, ancestor):
    active = {p[side]['ref'] for p in prs for side in ('head', 'base')
              if (p[side].get('repo') or {}).get('full_name') == REPO}
    ready = []
    for entry in entries:
        name, sha = entry['name'], entry['sha']
        if name not in branches:
            continue  # An already deleted branch is not recreated.
        current = branches[name]
        if current['commit']['sha'] != sha or current['protected'] or name in active:
            raise ValueError('candidate moved, protected or active: ' + name)
        if entry['mode'] == 'merged' and not ancestor(sha):
            raise ValueError('candidate is not merged into checked main: ' + name)
        ready.append(entry)
    return ready


def push_arguments(entries, head):
    args = ['push', '--atomic', '--porcelain', '--force-with-lease=' + RECEIPT + ':']
    specs = [head + ':' + RECEIPT]
    for entry in entries:
        ref = 'refs/heads/' + entry['name']
        args.append('--force-with-lease=' + ref + ':' + entry['sha'])
        specs.append(':' + ref)
        if entry['mode'] == 'archive-superseded':
            tag = ARCHIVE + entry['name']
            args.append('--force-with-lease=' + tag + ':')
            specs.append(entry['sha'] + ':' + tag)
    return args + [REMOTE] + specs


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        raise ValueError('GitHub API redirect rejected')


def main(apply, report):
    plan = json.loads((ROOT / PLAN_PATH).read_text())
    entries = list(validate_plan(plan))
    token = os.environ.get('GH_TOKEN', '')
    expected = os.environ.get('ASTER_CLEANUP_HEAD', '')
    if not token or not re.fullmatch(r'[0-9a-f]{40}', expected):
        raise ValueError('missing trusted Actions token or exact CI head')
    basic = base64.b64encode(('x-access-token:' + token).encode()).decode()
    env = {k: v for k, v in os.environ.items()
           if not k.startswith(('GIT_CONFIG_', 'GIT_TRACE', 'GIT_CURL'))}
    env.update(GIT_TERMINAL_PROMPT='0', GIT_CONFIG_COUNT='1',
               GIT_CONFIG_KEY_0='http.https://github.com/.extraheader',
               GIT_CONFIG_VALUE_0='AUTHORIZATION: basic ' + basic)

    def git(*args, allow_false=False):
        p = subprocess.run(['git', *args], cwd=ROOT, env=env, text=True,
                           capture_output=True, timeout=120)
        if allow_false and p.returncode == 1:
            return False
        if p.returncode:
            # Never print Git's raw diagnostics, headers or the inherited token.
            raise ValueError('Git operation failed with exit ' + str(p.returncode))
        return p.stdout.strip()

    opener = urllib.request.build_opener(NoRedirect())

    def api(path):
        request = urllib.request.Request('https://api.github.com/repos/' + REPO + '/' + path,
                    headers={'Authorization': 'Bearer ' + token, 'Accept': 'application/vnd.github+json',
                             'X-GitHub-Api-Version': '2022-11-28'})
        with opener.open(request, timeout=30) as response:
            raw = response.read((4 << 20) + 1)
        if len(raw) > 4 << 20:
            raise ValueError('GitHub metadata exceeds budget')
        return json.loads(raw)

    def pages(path):
        result = []
        for page in range(1, 9):
            items = api(path + ('&' if '?' in path else '?') + f'per_page=100&page={page}')
            result.extend(items)
            if len(items) < 100:
                return result
        raise ValueError('metadata pagination exceeded budget')

    head = git('rev-parse', 'HEAD')
    if head != expected or api('git/ref/heads/main')['object']['sha'] != expected:
        raise ValueError('main moved since the successful CI checkout')
    if git('merge-base', '--is-ancestor', plan['baseline'], head, allow_false=True) is False:
        raise ValueError('reviewed baseline not in current main')
    report.update(head=head, baseline=plan['baseline'], plan_sha256=hashlib.sha256((ROOT / PLAN_PATH).read_bytes()).hexdigest())
    receipt = git('ls-remote', REMOTE, RECEIPT)
    if receipt:
        sha = receipt.split()[0]
        git('fetch', '--no-tags', REMOTE, RECEIPT)
        if git('show', sha + ':' + PLAN_PATH) != (ROOT / PLAN_PATH).read_text().strip():
            raise ValueError('receipt refers to a different plan')
        report['status'] = 'already-applied'
        return
    runs = api('actions/runs?head_sha=' + head + '&event=push&per_page=100')['workflow_runs']
    for name in ('CI', 'Source evidence', 'Native migration verification'):
        matching = sorted((r for r in runs if r['name'] == name), key=lambda r: r['id'], reverse=True)
        if not matching or matching[0]['status'] != 'completed' or matching[0]['conclusion'] != 'success':
            report['status'] = 'waiting-for-verification'
            return
    prs = pages('pulls?state=open')
    branches = {b['name']: b for b in pages('branches')}
    # Only the just-merged, explicitly named completion branch can be appended.
    # Its expected SHA comes from the merged PR tied to this exact main commit,
    # never from a current branch tip selected without review.
    for pr in api('commits/' + head + '/pulls'):
        if pr['head']['ref'] == plan['merging_branch'] and (pr['head'].get('repo') or {}).get('full_name') == REPO:
            full = api('pulls/' + str(pr['number']))
            if not full['merged'] or full['merge_commit_sha'] != head or full['base']['ref'] != 'main':
                raise ValueError('completion PR is not this checked merge')
            entries.append({'name': plan['merging_branch'], 'sha': full['head']['sha'],
                            'mode': 'merged', 'reason': 'completed migration PR #' + str(pr['number'])})
    refs = ['+refs/heads/' + e['name'] + ':refs/cleanup-inputs/' + e['name']
            for e in entries if e['name'] in branches]
    if refs:
        git('fetch', '--no-tags', REMOTE, *refs)
    ready = classify(entries, branches, prs,
                     lambda sha: git('merge-base', '--is-ancestor', sha, head, allow_false=True) is not False)
    report['candidates'] = ready
    if not apply:
        report['status'] = 'dry-run'
        return
    # Recheck active PRs/current refs immediately before the atomic lease update.
    ready = classify(ready, {b['name']: b for b in pages('branches')}, pages('pulls?state=open'),
                     lambda sha: git('merge-base', '--is-ancestor', sha, head, allow_false=True) is not False)
    if api('git/ref/heads/main')['object']['sha'] != expected:
        raise ValueError('main changed during preflight')
    output = ROOT / 'artifacts/branch-cleanup'
    output.mkdir(parents=True, exist_ok=True)
    git('bundle', 'create', str(output / 'before-cleanup.bundle'), '--all')
    git(*push_arguments(ready, head))
    remaining = {b['name'] for b in pages('branches')}
    if any(e['name'] in remaining for e in ready):
        raise ValueError('post-push ref verification failed')
    report.update(status='applied', deleted=len(ready), remaining=sorted(remaining), receipt=RECEIPT)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    report = {'status': 'not-applied'}
    path = ROOT / 'artifacts/branch-cleanup/report.json'
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        main(args.apply, report)
    except Exception as error:
        report['error_type'] = type(error).__name__  # Do not serialize untrusted diagnostics.
        raise SystemExit('Cleanup refused; inspect verified refs/plan before retrying (' + type(error).__name__ + ')') from None
    finally:
        path.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))
