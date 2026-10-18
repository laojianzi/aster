"""Package scheduling must not weaken race, runtime or evidence requirements."""
from pathlib import Path
import shlex
import unittest

ROOT = Path(__file__).resolve().parents[1]


class UnitRaceSchedulingTests(unittest.TestCase):
    def test_serial_packages_keep_the_complete_single_race_stream(self):
        text = (ROOT / '.github/workflows/ci.yml').read_text()
        block = text.split('      - name: Unit, native interaction and race tests\n', 1)[1].split('      - name:', 1)[0]
        commands = [line.strip() for line in block.splitlines() if line.strip().startswith('go test ')]
        self.assertEqual(len(commands), 1)
        self.assertEqual(shlex.split(commands[0]), [
            'go', 'test', '-race', '-shuffle=on', '-count=1', '-timeout=5m',
            '-p=1', '-json', './...', '|', 'tee', 'artifacts/unit/tests.jsonl',
        ])
        self.assertIn('set -o pipefail', block)
        self.assertNotIn('continue-on-error', block)
        self.assertNotIn('||', block)

    def test_renderer_deadlines_and_all_real_cases_are_unchanged(self):
        product = (ROOT / 'internal/helmrender/render.go').read_text()
        test = (ROOT / 'internal/helmrender/render_test.go').read_text()
        self.assertRegex(product, r'Timeout\s+= 15 \* time.Second')
        self.assertIn('context.WithTimeout(parent, Timeout)', product)
        root = test.split('func TestHelmRealTemplateSnapshotAndFailure(t *testing.T) {', 1)[1]
        self.assertIn('context.WithTimeout(context.Background(), 30*time.Second)', root)
        self.assertEqual(root.count('Render(ctx, request)'), 2)
        for name in ('"error"', '"display-overflow"', '"stdout-overflow"'):
            self.assertIn(name, root)
        self.assertIn('!errors.Is(err, ErrOutput)', root)

    def test_independent_coverage_and_original_evidence_gates_remain(self):
        text = (ROOT / '.github/workflows/ci.yml').read_text()
        self.assertIn('python3 scripts/helm_uninstall_contract_evidence.py artifacts/unit/tests.jsonl --suite unit-native', text)
        block = text.split('      - name: Coverage\n', 1)[1].split('      - name:', 1)[0]
        self.assertIn('set -euo pipefail', block)
        self.assertIn('go test -coverprofile=artifacts/unit/coverage.out -json ./... ', block)
        self.assertIn('python3 scripts/helm_uninstall_contract_evidence.py artifacts/unit/coverage/tests.jsonl --suite unit-native', block)
        self.assertNotIn('-run ', block)
        self.assertNotIn('||', block)


if __name__ == '__main__':
    unittest.main()
