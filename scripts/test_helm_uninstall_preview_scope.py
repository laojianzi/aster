"""Uninstall scope is inert, bounded, and cannot bypass existing evidence gates."""
import json
from pathlib import Path
import tempfile
import unittest
import run_migration_tests as migration
import test_evidence as evidence

class HelmUninstallPreviewScopeTests(unittest.TestCase):
    def test_required_names_are_additive_and_missing_or_skipped_fail(self):
        for suite, names in evidence.HELM_UNINSTALL_PREVIEW_REQUIRED.items():
            required = evidence.required_for_suite(suite, ['TestPrior'])
            for group in (evidence.INTEGER_MAP_REQUIRED, evidence.INTEGER_SET_REQUIRED,
                          evidence.MANIFEST_CLIPBOARD_REQUIRED, evidence.BOOLEAN_COLLECTIONS_REQUIRED,
                          evidence.HELM_RENDER_REQUIRED, evidence.HELM_METADATA_REQUIRED, evidence.HELM_INSPECTION_REQUIRED, evidence.HELM_UNINSTALL_PREVIEW_REQUIRED):
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
        for name in evidence.HELM_UNINSTALL_PREVIEW_REQUIRED['native-os-interactions']:
            self.assertIn(name, migration.REQUIRED)


    def test_preview_remains_read_only_and_does_not_export_payload(self):
        root = Path(__file__).resolve().parents[1]
        transport = (root / 'internal/kube/helm_uninstall_preview.go').read_text()
        self.assertIn('b.readHelmRevision(', transport)
        self.assertIn('time.Now()', transport)
        for file in ('internal/helmrelease/uninstall_preview.go',
                     'internal/kube/helm_uninstall_preview.go',
                     'internal/ui/helm_uninstall_preview.go'):
            text = (root / file).read_text()
            for forbidden in ('exec.Command', '.Post(', '.Patch(', '.Delete(', '.Submit(', 'delivery.Parse('):
                self.assertNotIn(forbidden, text)
        source = (root / 'internal/helmrelease/uninstall_preview.go').read_text()
        for required in ('inspectSecret(ctx', 'manifest.IndexSource', 'index.Partial()',
                         'MaxPreviewObjects', 'MaxPreviewSegments', 'PreviewLifetime',
                         'rand.Read', '!now.Before(p.created)', 'now.Before(p.expires)',
                         'append([]UninstallItem(nil), p.items...)'):
            self.assertIn(required, source)
        ui = (root / 'internal/ui/helm_inspection.go').read_text()
        self.assertIn('guardedCheckbox(', ui)
        self.assertIn('current.contentEpoch != contentEpoch', ui)
        self.assertIn('scope.Matches(session, ns, row, time.Now())', ui)
        self.assertIn('r.preview = nil', ui)
        doc = (root / 'docs/helm-uninstall-preview.md').read_text()
        for boundary in ('NOT executable', 'not necessarily the latest', 'live ownership',
                         'no server dry-run', 'CRDs', '2 minutes', 'source order', 'P3'):
            self.assertIn(boundary, doc)

if __name__ == '__main__':
    unittest.main()
