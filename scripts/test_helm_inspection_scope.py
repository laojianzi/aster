"""Selected sensitive reads stay explicit, private and additive to all old gates."""
import json
from pathlib import Path
import tempfile
import unittest
import run_migration_tests as migration
import test_evidence as evidence

class HelmInspectionScopeTests(unittest.TestCase):
    def test_required_names_are_additive_and_missing_or_skipped_fail(self):
        for suite, names in evidence.HELM_INSPECTION_REQUIRED.items():
            required = evidence.required_for_suite(suite, ['TestPrior'])
            for group in (evidence.INTEGER_MAP_REQUIRED, evidence.INTEGER_SET_REQUIRED,
                          evidence.MANIFEST_CLIPBOARD_REQUIRED, evidence.BOOLEAN_COLLECTIONS_REQUIRED,
                          evidence.HELM_RENDER_REQUIRED, evidence.HELM_METADATA_REQUIRED, evidence.HELM_INSPECTION_REQUIRED):
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
                        self.assertEqual(evidence.summarize(path, required, suite)['valid'], outcome == 'pass')
        for name in evidence.HELM_INSPECTION_REQUIRED['native-os-interactions']:
            self.assertIn(name, migration.REQUIRED)


    def test_read_path_is_explicit_bounded_and_separate_from_metadata(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'internal/kube/helm_inspection.go').read_text()
        for forbidden in ('exec.Command', '.Post(', '.Patch(', '.Delete(', 'BearerToken', 'Impersonate', 'GetObject('):
            self.assertNotIn(forbidden, source)
        for required in ('if !acknowledged', 'helmrelease.ValidSelection', 'b.config.RateLimiter.Wait(ctx)',
                         'b.metricsHTTP.Do(req)', 'io.LimitReader', 'helmrelease.ReadTimeout'):
            self.assertIn(required, source)
        metadata = (root / 'internal/kube/helm_releases.go').read_text()
        self.assertNotIn('InspectSecret', metadata)
        self.assertIn('req.Header.Set("Accept", helmrelease.Accept)', metadata)
        decoder = (root / 'internal/helmrelease/inspection.go').read_text()
        for required in ('gz.Multistream(false)', 'source.Len() != 0', 'MaxReleaseBytes+1',
                         'm.ResourceVersion != selected.ResourceVersion', 'string(m.UID) != selected.UID'):
            self.assertIn(required, decoder)

if __name__ == '__main__':
    unittest.main()
