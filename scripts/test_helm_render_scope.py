"""Helm tool trust and executable evidence remain additive and fail closed."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import retire_completed_oidc as retirement
import prepare_helm as prepare
import run_migration_tests as migration
import test_evidence as evidence


class HelmRenderScopeTests(unittest.TestCase):
    def test_retirement_requires_exact_completed_inactive_reachable_pr(self):
        number, branch, head, tip = 68, 'feat/local-helm-render-delivery', 'a' * 40, 'b' * 40
        self.assertEqual(retirement.APPROVED[number], branch)
        self.assertNotIn(67, retirement.APPROVED)
        self.assertNotIn(number, retirement.DEPENDENCY_SCOPES)
        pr = dict(number=number, state='closed', merged=True, merge_commit_sha=head,
                  head=dict(ref=branch, sha=tip, repo=dict(full_name=retirement.REPO)),
                  base=dict(ref='main', repo=dict(full_name=retirement.REPO)))
        refs = {branch: dict(protected=False, commit=dict(sha=tip))}
        self.assertEqual(retirement.select_scope([pr], head), number)
        self.assertEqual(retirement.candidate(pr, head, refs, [], lambda _: True,
                                             number=number, branch=branch), tip)
        self.assertIsNone(retirement.candidate(pr, head, {}, [], lambda _: True,
                                              number=number, branch=branch))
        for mode in ('open', 'unmerged', 'other-merge', 'wrong-branch', 'wrong-base',
                     'foreign-head', 'foreign-base', 'invalid-tip', 'moved', 'protected',
                     'unreachable', 'active-head', 'active-base', 'issue', 'previous-pr'):
            with self.subTest(mode=mode):
                p, branches, active = copy.deepcopy(pr), copy.deepcopy(refs), []
                n = number
                if mode == 'open': p['state'] = 'open'
                if mode == 'unmerged': p['merged'] = False
                if mode == 'other-merge': p['merge_commit_sha'] = 'c' * 40
                if mode == 'wrong-branch': p['head']['ref'] = retirement.APPROVED[66]
                if mode == 'wrong-base': p['base']['ref'] = 'other-main'
                if mode == 'foreign-head': p['head']['repo']['full_name'] = 'foreign/repo'
                if mode == 'foreign-base': p['base']['repo']['full_name'] = 'foreign/repo'
                if mode == 'invalid-tip': p['head']['sha'] = 'invalid'
                if mode == 'moved': branches[branch]['commit']['sha'] = 'c' * 40
                if mode == 'protected': branches[branch]['protected'] = True
                if mode in ('active-head', 'active-base'):
                    other = copy.deepcopy(pr)
                    other['head']['ref'], other['base']['ref'] = 'unrelated-head', 'unrelated-base'
                    other[mode.removeprefix('active-')]['ref'] = branch
                    active = [other]
                if mode == 'issue': n = 67
                if mode == 'previous-pr': n = 66
                with self.assertRaises(ValueError):
                    retirement.candidate(p, head, branches, active, lambda _: mode != 'unreachable',
                                         number=n, branch=branch)

    def test_mandatory_new_names_preserve_all_previous_scopes(self):
        for suite, names in evidence.HELM_RENDER_REQUIRED.items():
            required = evidence.required_for_suite(suite, ['TestPrior'])
            for group in (evidence.INTEGER_MAP_REQUIRED, evidence.INTEGER_SET_REQUIRED,
                          evidence.MANIFEST_CLIPBOARD_REQUIRED, evidence.BOOLEAN_COLLECTIONS_REQUIRED,
                          evidence.HELM_RENDER_REQUIRED):
                self.assertTrue(set(group.get(suite, ())) <= set(required))
            self.assertEqual(len(required), len(set(required)))
            for name in names:
                for outcome in ('pass', 'skip', 'fail', 'missing', 'unfinished'):
                    with self.subTest(suite=suite, name=name, outcome=outcome), tempfile.TemporaryDirectory() as temp:
                        path = Path(temp) / 'tests.jsonl'
                        events = []
                        for test in required:
                            status = outcome if test == name else 'pass'
                            if status != 'missing':
                                events.append(dict(Action='run', Package='fixture', Test=test))
                            if status not in ('missing', 'unfinished'):
                                events.append(dict(Action=status, Package='fixture', Test=test))
                        events.append(dict(Action='pass', Package='fixture'))
                        path.write_text(''.join(json.dumps(e) + '\n' for e in events))
                        report = evidence.summarize(path, required, suite)
                        self.assertEqual(report['valid'], outcome == 'pass')
        for name in evidence.HELM_RENDER_REQUIRED['native-os-interactions']:
            self.assertIn(name, migration.REQUIRED)

    def test_distribution_pin_and_download_verification(self):
        data = b'verified fixture only, not an executable'
        digest = hashlib.sha256(data).hexdigest()
        self.assertTrue(prepare.verified(data, digest))
        self.assertFalse(prepare.verified(data + b'x', digest))
        self.assertFalse(prepare.verified(b'', hashlib.sha256(b'').hexdigest()))
        with mock.patch.object(prepare, 'LIMIT', 3):
            self.assertFalse(prepare.verified(data, digest))
        pins = json.loads((prepare.ROOT / 'internal/helmrender/distributions.json').read_text())
        self.assertEqual(pins['version'], 'v4.3.0')
        self.assertEqual(len(pins['platforms']), 6)
        for platform, entry in pins['platforms'].items():
            ext = '.zip' if platform.startswith('windows-') else '.tar.gz'
            self.assertEqual(entry['file'], 'helm-v4.3.0-' + platform + ext)
            self.assertRegex(entry['sha256'], r'^[0-9a-f]{64}$')
        with self.assertRaises(ValueError):
            prepare.NoRedirect().redirect_request(None, None, 302, None, None, 'https://other.invalid')

    def test_every_actual_helm_gate_prepares_approved_tool(self):
        ci = (prepare.ROOT / '.github/workflows/ci.yml').read_text()
        migration_yaml = (prepare.ROOT / '.github/workflows/native-migration.yml').read_text()
        self.assertEqual(ci.count('python scripts/prepare_helm.py --github-env'), 3)
        self.assertEqual(migration_yaml.count('python scripts/prepare_helm.py --github-env'), 1)
        self.assertIn('--evidence-dir artifacts/unit/helm --archive-evidence', ci)
        self.assertNotIn('continue-on-error', ci)
        source = (prepare.ROOT / 'internal/helmrender/render.go').read_text()
        self.assertIn('hex.EncodeToString(digest[:]) != d.SHA256', source)
        self.assertIn('"--dry-run=client"', source)
        self.assertIn('"HELM_MEMORY_DRIVER_DATA": "memory-releases.yaml"', source)
        self.assertIn('"memory-releases.yaml": "[]\\n"', source)
        for flag in ('"--dry-run=server"', '"--no-hooks"', '"--skip-tests"',
                     '"--enable-dns"', '"--dependency-update"', '"--post-renderer"'):
            self.assertNotIn(flag, source)


if __name__ == '__main__':
    unittest.main()
