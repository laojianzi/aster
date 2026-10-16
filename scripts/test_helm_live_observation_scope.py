"""Live observations cannot turn metadata into a write capability or skip gates."""
import json
from pathlib import Path
import tempfile
import unittest
import run_migration_tests as migration
import test_evidence as evidence

class HelmLiveObservationScopeTests(unittest.TestCase):
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
