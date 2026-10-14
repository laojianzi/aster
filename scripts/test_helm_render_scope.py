"""Helm tool trust and executable evidence remain additive and fail closed."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import prepare_helm as prepare
import run_migration_tests as migration
import test_evidence as evidence


class HelmRenderScopeTests(unittest.TestCase):
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
        for flag in ('"--dry-run=server"', '"--no-hooks"', '"--skip-tests"',
                     '"--enable-dns"', '"--dependency-update"', '"--post-renderer"'):
            self.assertNotIn(flag, source)


if __name__ == '__main__':
    unittest.main()
