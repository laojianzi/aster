"""Ensure integer-key scope adds mandatory evidence without weakening old gates."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import retire_completed_oidc as retirement
import run_migration_tests as migration
import test_evidence as evidence


class IntegerMapScopeTests(unittest.TestCase):
    def test_registration_retains_feature_gates(self):
        self.assertEqual(retirement.APPROVED[60], 'feat/native-schema-integer-map-keys')
        self.assertNotIn(60, retirement.DEPENDENCY_SCOPES)
        self.assertEqual(retirement.REQUIRED,
                         ('CI', 'Source evidence', 'Native migration verification', 'OIDC qualification'))
        self.assertEqual(retirement.APPROVED[58], 'renovate/mygo-native-framework')

    def test_migration_requires_both_native_cases_and_old_history(self):
        for name in evidence.INTEGER_MAP_REQUIRED['native-os-interactions']:
            self.assertIn(name, migration.REQUIRED)
        self.assertIn('TestNativeSchemaListMapPreservesDraftAndHistory', migration.REQUIRED)
        self.assertIn('TestNativeSchemaListSetPreservesDraftAndHistory', migration.REQUIRED)
        self.assertIn('TestNativeMyGo048ChunkedBufferPreservesMixedBytesAndHistory', migration.REQUIRED)
        self.assertIn('TestTerminalNativeMyGo048WheelRoutingAndClose', migration.TERMINAL_REQUIRED)
        self.assertEqual(len(migration.REQUIRED), len(set(migration.REQUIRED)))

    def test_named_suite_union_keeps_explicit_old_requirements(self):
        for suite, names in evidence.INTEGER_MAP_REQUIRED.items():
            got = evidence.required_for_suite(suite, ['TestOldContract', names[0]])
            self.assertEqual(got[0], 'TestOldContract')
            self.assertEqual(set(got), {'TestOldContract', *names, *evidence.INTEGER_SET_REQUIRED.get(suite, ()), *evidence.MANIFEST_CLIPBOARD_REQUIRED.get(suite, ()), *evidence.BOOLEAN_COLLECTIONS_REQUIRED.get(suite, ())})
            self.assertEqual(len(got), len(set(got)))
        self.assertEqual(evidence.required_for_suite('another-suite', ['TestOldContract']),
                         ['TestOldContract'])

    def test_actual_cli_rejects_missing_new_or_old_names(self):
        script = Path(evidence.__file__)
        for suite, names in evidence.INTEGER_MAP_REQUIRED.items():
            for excluded in (names[0], 'TestOldContract', None):
                with self.subTest(suite=suite, excluded=excluded), tempfile.TemporaryDirectory() as temp:
                    path = Path(temp) / 'tests.jsonl'
                    events = []
                    for name in ['TestOldContract', *names, *evidence.INTEGER_SET_REQUIRED.get(suite, ()), *evidence.MANIFEST_CLIPBOARD_REQUIRED.get(suite, ()), *evidence.BOOLEAN_COLLECTIONS_REQUIRED.get(suite, ())]:
                        if name != excluded:
                            for action in ('run', 'pass'):
                                events.append({'Action': action, 'Package': 'fixture', 'Test': name})
                    events.append({'Action': 'pass', 'Package': 'fixture'})
                    path.write_text(''.join(json.dumps(e) + '\n' for e in events), encoding='utf-8')
                    result = subprocess.run([sys.executable, str(script), str(path), '--suite', suite,
                                             '--require', 'TestOldContract'], capture_output=True,
                                            text=True, timeout=10)
                    report = json.loads(result.stdout)
                    self.assertEqual(result.returncode, 0 if excluded is None else 1)
                    self.assertEqual(report['missing_required_tests'], [] if excluded is None else [excluded])
                    self.assertEqual(json.loads(path.with_name('summary.json').read_text()), report)


if __name__ == '__main__':
    unittest.main()
