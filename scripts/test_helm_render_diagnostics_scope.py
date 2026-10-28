"""The blocking render diagnostic increment is additive and mandatory."""
import hashlib
import json
from pathlib import Path
import re
import tempfile
import unittest

import helm_native_install_evidence as install
import helm_render_diagnostics_evidence as diagnostics
import run_migration_tests as migration
import test_evidence as evidence
from helm_native_scope import production_calls

ROOT = Path(__file__).resolve().parents[1]
CENSUS = {
    'RENDER_NAMES': (17, 'ff21eadefa5a25d88e3e9d37a0b26057e0b068105156ace2ab5638a03d871dca'),
    'HELPER_NAMES': (13, '20ff55b4c02064c189184e1b60e7b5a59a1adbb5cc5efc95d2590863e943fabd'),
    'UI_NAMES': (5, '5421c1bb967b07f8881301cc8efd06b67b3baa1c1ebaf7e73d32baaa621c9d63'),
}


def digest(names):
    return hashlib.sha256('\n'.join(sorted(names)).encode()).hexdigest()


class HelmRenderDiagnosticsScopeTests(unittest.TestCase):
    def test_exact_diagnostic_names_and_disjoint_install_scope(self):
        all_names = set()
        for group, (count, checksum) in CENSUS.items():
            names = getattr(diagnostics, group)
            self.assertEqual(len(names), count)
            self.assertEqual(len(set(names)), count)
            self.assertEqual(digest(names), checksum)
            self.assertFalse(all_names & set(names))
            all_names.update(names)
        self.assertFalse(all_names & set(install.NATIVE_NAMES))
        self.assertFalse(all_names & set(install.REAL_NAMES))

    def test_only_three_new_test_paths_with_exact_declared_roots(self):
        paths = {
            'internal/credentialexec/render_diagnostics_test.go': diagnostics.HELPER_NAMES,
            'internal/helmrender/diagnostics_test.go': diagnostics.RENDER_NAMES,
            'internal/ui/helm_render_diagnostics_test.go': diagnostics.UI_NAMES,
        }
        self.assertEqual(diagnostics.ADDED_TEST_FILES, set(paths))
        self.assertEqual(install.ADDED_TEST_FILES, install.INSTALL_TEST_FILES | set(paths))
        self.assertEqual(len(install.INSTALL_TEST_FILES), 5)
        for path, names in paths.items():
            roots = set(re.findall(r'^func (Test\w+)\(', (ROOT / path).read_text(), re.M))
            self.assertEqual(roots, {name.split('/', 1)[0] for name in names}, path)

    def test_exact_applicable_suites_and_migration_modes(self):
        expected = {
            'unit-native': set(diagnostics.RENDER_NAMES) | set(diagnostics.HELPER_NAMES) | set(diagnostics.UI_NAMES),
            'native-os-interactions': set(diagnostics.HELPER_NAMES) | set(diagnostics.UI_NAMES),
            'real-cluster-native': set(diagnostics.UI_NAMES),
            'migration-checked': set(diagnostics.UI_NAMES),
            'migration-production': set(diagnostics.UI_NAMES),
        }
        self.assertEqual(set(diagnostics.AUTOMATIC), set(expected))
        for suite, names in expected.items():
            self.assertEqual(diagnostics.additions(suite), names)
            self.assertEqual(len(diagnostics.AUTOMATIC[suite]), len(names))
            self.assertTrue(names <= set(evidence.required_for_suite(suite, [])))
        self.assertEqual(evidence.required_for_suite('unknown-suite', ['TestExisting']), ['TestExisting'])
        self.assertTrue(set(diagnostics.UI_NAMES) <= set(migration.REQUIRED))
        pattern = re.compile(migration.root_test_pattern(migration.REQUIRED))
        for name in diagnostics.UI_NAMES:
            self.assertIsNotNone(pattern.fullmatch(name.split('/', 1)[0]))
        for name in (*diagnostics.RENDER_NAMES, *diagnostics.HELPER_NAMES):
            self.assertIsNone(pattern.fullmatch(name.split('/', 1)[0]))

    def test_each_diagnostic_name_needs_one_finished_pass(self):
        names = [*diagnostics.RENDER_NAMES, *diagnostics.HELPER_NAMES, *diagnostics.UI_NAMES]
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'events.jsonl'
            for chosen in names:
                for mode in ('pass', 'missing', 'fail', 'skip', 'unfinished', 'duplicate'):
                    with self.subTest(name=chosen, mode=mode):
                        events = []
                        for name in names:
                            if name == chosen and mode == 'missing':
                                continue
                            events.append(dict(Action='run', Package='fixture', Test=name))
                            if name == chosen and mode == 'unfinished':
                                continue
                            action = mode if name == chosen and mode in ('fail', 'skip') else 'pass'
                            events.append(dict(Action=action, Package='fixture', Test=name))
                            if name == chosen and mode == 'duplicate':
                                events.append(events[-1])
                        events.append(dict(Action='pass', Package='fixture'))
                        path.write_text(''.join(json.dumps(event) + '\n' for event in events))
                        self.assertEqual(evidence.summarize(path, names, 'render-diagnostics')['valid'], mode == 'pass')

    def test_native_diagnostic_consumer_is_the_existing_capture_worker(self):
        self.assertEqual(production_calls(ROOT, 'RenderWithDiagnostics'), {'internal/ui/helm.go': 1})
        source = (ROOT / 'internal/ui/helm.go').read_text()
        self.assertIn('result, diagnostics, err := helmrender.RenderWithDiagnostics(ctx, request)', source)
        self.assertIn('err.Error() + " · " + diagnostics.Summary()', source)
        for private_sink in ('os.WriteFile', 'os.Create', 'log.Printf', 'fmt.Printf', 'http.NewRequest'):
            self.assertNotIn(private_sink, source)


if __name__ == '__main__':
    unittest.main()
