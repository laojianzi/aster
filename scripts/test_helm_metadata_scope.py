"""Metadata-only inspection stays additive to the existing executable gates."""
import json
from pathlib import Path
import tempfile
import unittest

import run_migration_tests as migration
import test_evidence as evidence


class HelmMetadataScopeTests(unittest.TestCase):
    def test_required_names_are_additive_and_missing_or_skipped_fail(self):
        for suite, names in evidence.HELM_METADATA_REQUIRED.items():
            required = evidence.required_for_suite(suite, ['TestPrior'])
            for group in (evidence.INTEGER_MAP_REQUIRED, evidence.INTEGER_SET_REQUIRED,
                          evidence.MANIFEST_CLIPBOARD_REQUIRED, evidence.BOOLEAN_COLLECTIONS_REQUIRED,
                          evidence.HELM_RENDER_REQUIRED, evidence.HELM_METADATA_REQUIRED):
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
        for name in evidence.HELM_METADATA_REQUIRED['native-os-interactions']:
            self.assertIn(name, migration.REQUIRED)

    def test_production_source_has_no_full_object_or_cli_path(self):
        root = Path(__file__).resolve().parents[1]
        source = (root / 'internal/kube/helm_releases.go').read_text()
        for forbidden in ('exec.Command', '.dynamic.', '.typed.', 'GetObject(',
                          '.Post(', '.Patch(', '.Delete(', 'BearerToken', 'Impersonate'):
            self.assertNotIn(forbidden, source)
        self.assertIn('req.Header.Set("Accept", helmrelease.Accept)', source)
        self.assertIn('b.config.RateLimiter.Wait(ctx)', source)
        self.assertIn('b.metricsHTTP.Do(req)', source)
        self.assertIn('io.LimitReader', source)
        self.assertIn('helmrelease.ReadTimeout', source)
        contract = (root / 'internal/helmrelease/metadata.go').read_text()
        self.assertIn('application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1', contract)
        self.assertNotIn('application/json;q=', contract)


if __name__ == '__main__':
    unittest.main()
