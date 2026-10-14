"""Boolean collection evidence is additive and omissions are never passes."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import retire_completed_oidc as retirement
import run_migration_tests as migration
import test_evidence as evidence


class BooleanCollectionsScopeTests(unittest.TestCase):
    def test_migration_keeps_new_and_old_native_contracts(self):
        for name in evidence.BOOLEAN_COLLECTIONS_REQUIRED['native-os-interactions']:
            self.assertIn(name, migration.REQUIRED)
            self.assertNotIn(name, migration.TERMINAL_REQUIRED)
        for previous in (evidence.INTEGER_MAP_REQUIRED, evidence.INTEGER_SET_REQUIRED,
                         evidence.MANIFEST_CLIPBOARD_REQUIRED):
            for name in previous['native-os-interactions']:
                self.assertIn(name, migration.REQUIRED)
        self.assertEqual(len(migration.REQUIRED), len(set(migration.REQUIRED)))
        self.assertIn('TestTerminalNativeMyGo048WheelRoutingAndClose', migration.TERMINAL_REQUIRED)
        self.assertEqual(retirement.REQUIRED,
                         ('CI', 'Source evidence', 'Native migration verification', 'OIDC qualification'))

    def test_suite_union_retains_every_previous_name(self):
        for suite, names in evidence.BOOLEAN_COLLECTIONS_REQUIRED.items():
            got = evidence.required_for_suite(suite, ['TestOldContract', names[0]])
            self.assertEqual(got[0], 'TestOldContract')
            self.assertEqual(set(got), {'TestOldContract', *names,
                             *evidence.INTEGER_MAP_REQUIRED.get(suite, ()),
                             *evidence.INTEGER_SET_REQUIRED.get(suite, ()),
                             *evidence.MANIFEST_CLIPBOARD_REQUIRED.get(suite, ())})
            self.assertEqual(len(got), len(set(got)))
        self.assertEqual(evidence.required_for_suite('another-suite', ['TestOldContract']),
                         ['TestOldContract'])

    def test_cli_rejects_each_missing_new_name_and_old_scope(self):
        for suite, names in evidence.BOOLEAN_COLLECTIONS_REQUIRED.items():
            required = evidence.required_for_suite(suite, ['TestOldContract'])
            excluded_cases = [*names, 'TestOldContract',
                              evidence.INTEGER_MAP_REQUIRED[suite][0],
                              evidence.INTEGER_SET_REQUIRED[suite][0],
                              evidence.MANIFEST_CLIPBOARD_REQUIRED[suite][0], None]
            for excluded in excluded_cases:
                with self.subTest(suite=suite, excluded=excluded), tempfile.TemporaryDirectory() as temp:
                    path = Path(temp) / 'tests.jsonl'
                    events = []
                    for name in required:
                        if name != excluded:
                            events.extend({'Action': action, 'Package': 'fixture', 'Test': name}
                                          for action in ('run', 'pass'))
                    events.append({'Action': 'pass', 'Package': 'fixture'})
                    path.write_text(''.join(json.dumps(e) + '\n' for e in events), encoding='utf-8')
                    result = subprocess.run([sys.executable, str(Path(evidence.__file__)),
                                             str(path), '--suite', suite, '--require', 'TestOldContract'],
                                            capture_output=True, text=True, timeout=10)
                    report = json.loads(result.stdout)
                    self.assertEqual(result.returncode, 0 if excluded is None else 1)
                    self.assertEqual(report['missing_required_tests'], [] if excluded is None else [excluded])
                    self.assertEqual(json.loads(path.with_name('summary.json').read_text()), report)


if __name__ == '__main__':
    unittest.main()
