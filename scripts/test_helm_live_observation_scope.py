"""Live observations cannot turn metadata into a write capability or skip gates."""
import json
import copy
import retire_completed_oidc as retirement
from pathlib import Path
import tempfile
import unittest
import run_migration_tests as migration
import test_evidence as evidence

class HelmLiveObservationScopeTests(unittest.TestCase):
    def test_retirement_requires_exact_completed_inactive_reachable_pr(self):
        number, branch, head, tip = 79, 'feat/helm-live-observation', 'a' * 40, 'b' * 40
        self.assertEqual(retirement.APPROVED[number], branch)
        self.assertNotIn(78, retirement.APPROVED)
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
                if mode == 'wrong-branch': p['head']['ref'] = retirement.APPROVED[77]
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
                if mode == 'issue': n = 78
                if mode == 'previous-pr': n = 77
                with self.assertRaises(ValueError):
                    retirement.candidate(p, head, branches, active, lambda _: mode != 'unreachable',
                                         number=n, branch=branch)

    def test_new_required_names_cannot_be_skipped_or_omitted(self):
        for suite, additions in evidence.HELM_LIVE_OBSERVATION_REQUIRED.items():
            required = evidence.required_for_suite(suite, ['TestPrior'])
            for group in (evidence.INTEGER_MAP_REQUIRED, evidence.INTEGER_SET_REQUIRED,
                          evidence.MANIFEST_CLIPBOARD_REQUIRED, evidence.BOOLEAN_COLLECTIONS_REQUIRED,
                          evidence.HELM_RENDER_REQUIRED, evidence.HELM_METADATA_REQUIRED,
                          evidence.HELM_INSPECTION_REQUIRED, evidence.HELM_UNINSTALL_PREVIEW_REQUIRED):
                self.assertTrue(set(group.get(suite, ())) <= set(required))
            for name in additions:
                for outcome in ('pass', 'missing', 'fail', 'skip', 'unfinished'):
                    with self.subTest(suite=suite, name=name, outcome=outcome), tempfile.TemporaryDirectory() as d:
                        events = []
                        for test in required:
                            status = outcome if name == test else 'pass'
                            if status != 'missing':
                                events.append(dict(Action='run', Package='fixture', Test=test))
                            if status not in ('missing', 'unfinished'):
                                events.append(dict(Action=status, Package='fixture', Test=test))
                        events.append(dict(Action='pass', Package='fixture'))
                        path = Path(d) / 'tests.jsonl'
                        path.write_text(''.join(json.dumps(e) + '\n' for e in events))
                        self.assertEqual(evidence.summarize(path, required, suite)['valid'], outcome == 'pass')
        self.assertTrue(set(evidence.HELM_LIVE_OBSERVATION_REQUIRED['native-os-interactions']) <= set(migration.REQUIRED))
        self.assertFalse(set(evidence.HELM_LIVE_OBSERVATION_REQUIRED['native-os-interactions']) & set(migration.TERMINAL_REQUIRED))

    def test_read_only_source_and_explicit_lifetime(self):
        root = Path(__file__).resolve().parents[1]
        for file in ('internal/helmrelease/live_observation.go',
                     'internal/kube/helm_live_observation.go', 'internal/ui/helm_live_observation.go'):
            source = (root / file).read_text()
            for forbidden in ('exec.Command', '.Post(', '.Patch(', '.Delete(', '.Submit(', 'operation.Prepared'):
                self.assertNotIn(forbidden, source)
        backend = (root / 'internal/kube/helm_live_observation.go').read_text()
        self.assertEqual(backend.count('b.HelmReleaseMetadata('), 2)
        for gate in ('LatestRevisionSet', 'resolveLiveRoute', 'maxLiveReadBytes',
                     'PartialObjectMetadata', 'FinishLiveObservation', 'http.MethodGet',
                     'context.WithDeadline', 'clear(data)', 'metav1.StatusReasonNotFound'):
            self.assertIn(gate, backend)
        ui = (root / 'internal/ui/helm_live_observation.go').read_text()
        for gate in ('guardedCheckbox', 'current.contentEpoch != contentEpoch', 'current.preview != p',
                     'current.live = nil', 'result.Matches(', 'time.Now()', 'r.liveAcknowledged, r.acknowledged = false, false'):
            self.assertIn(gate, ui)
        doc = (root / 'docs/helm-live-observation.md').read_text()
        for limit in ('NOT executable', '30 seconds', 'two snapshots', 'not a transaction',
                      'Foreign namespaces', 'metadata-only', 'P3'):
            self.assertIn(limit, doc)

if __name__ == '__main__':
    unittest.main()
