"""Contract characterization is mandatory, not a production CLI capability."""
import json
import copy
import subprocess
import retire_completed_oidc as retirement
from pathlib import Path
import tempfile
import unittest
import test_evidence as evidence
import helm_uninstall_contract_evidence as contract_evidence

class HelmUninstallContractScopeTests(unittest.TestCase):
    def test_each_new_case_is_required_and_cannot_hide_failure(self):
        for suite, additions in contract_evidence.REQUIRED.items():
            required = contract_evidence.required(suite) + ['TestPrior']
            self.assertEqual(len(required), len(set(required)))
            for group in (evidence.HELM_LIVE_OBSERVATION_REQUIRED, evidence.HELM_UNINSTALL_PREVIEW_REQUIRED,
                          evidence.HELM_INSPECTION_REQUIRED, evidence.HELM_METADATA_REQUIRED,
                          evidence.HELM_RENDER_REQUIRED, evidence.INTEGER_MAP_REQUIRED,
                          evidence.INTEGER_SET_REQUIRED, evidence.BOOLEAN_COLLECTIONS_REQUIRED,
                          evidence.MANIFEST_CLIPBOARD_REQUIRED):
                self.assertTrue(set(group.get(suite, ())) <= set(required))
            for name in additions:
                for outcome in ('pass', 'missing', 'skip', 'fail', 'unfinished'):
                    with self.subTest(suite=suite, name=name, outcome=outcome), tempfile.TemporaryDirectory() as d:
                        events = []
                        for test in required:
                            state = outcome if test == name else 'pass'
                            if state != 'missing': events.append(dict(Action='run', Package='fixture', Test=test))
                            if state not in ('missing', 'unfinished'): events.append(dict(Action=state, Package='fixture', Test=test))
                        events.append(dict(Action='pass', Package='fixture'))
                        p = Path(d) / 'tests.jsonl'
                        p.write_text(''.join(json.dumps(e) + '\n' for e in events))
                        self.assertEqual(evidence.summarize(p, required, suite)['valid'], outcome == 'pass')

    def test_only_actual_pr81_can_retire_its_exact_branch(self):
        number, branch = 81, 'test/helm-uninstall-contract'
        head, tip = 'a' * 40, 'b' * 40
        self.assertEqual(retirement.APPROVED[number], branch)
        self.assertNotIn(80, retirement.APPROVED)
        self.assertNotIn(number, retirement.DEPENDENCY_SCOPES)
        pr = dict(number=number, state='closed', merged=True, merge_commit_sha=head,
                  head=dict(ref=branch, sha=tip, repo=dict(full_name=retirement.REPO)),
                  base=dict(ref='main', repo=dict(full_name=retirement.REPO)))
        refs = {branch: dict(protected=False, commit=dict(sha=tip))}
        self.assertEqual(retirement.select_scope([pr], head), number)
        self.assertEqual(retirement.candidate(pr, head, refs, [], lambda _: True,
                                             number=number, branch=branch), tip)
        for mode in ('issue-number', 'open', 'unmerged', 'wrong-merge', 'wrong-branch',
                     'foreign', 'moved', 'protected', 'active', 'unreachable'):
            with self.subTest(mode=mode):
                p, branches = copy.deepcopy(pr), copy.deepcopy(refs)
                if mode == 'issue-number': p['number'] = 80
                if mode == 'open': p['state'] = 'open'
                if mode == 'unmerged': p['merged'] = False
                if mode == 'wrong-merge': p['merge_commit_sha'] = 'c' * 40
                if mode == 'wrong-branch': p['head']['ref'] = 'feat/helm-live-observation'
                if mode == 'foreign': p['head']['repo']['full_name'] = 'foreign/repo'
                if mode == 'moved': branches[branch]['commit']['sha'] = 'd' * 40
                if mode == 'protected': branches[branch]['protected'] = True
                with self.assertRaises(ValueError):
                    retirement.candidate(p, head, branches, [pr] if mode == 'active' else [],
                                         lambda _: mode != 'unreachable', number=number, branch=branch)

    def test_ci_gates_are_separate_commands_not_folded_yaml(self):
        text = (Path(__file__).resolve().parents[1] / '.github/workflows/ci.yml').read_text()
        for suite, step in (('unit-native', 'Validate test evidence'),
                            ('real-cluster-native', 'Validate real-cluster evidence')):
            block = text.split('      - name: ' + step + '\n', 1)[1].split('      - name:', 1)[0]
            self.assertIn('        run: |\n', block)
            self.assertTrue(any(line.startswith('          python3 scripts/test_evidence.py ')
                                for line in block.splitlines()))
            gate = [line for line in block.splitlines()
                    if line.startswith('          python3 scripts/helm_uninstall_contract_evidence.py ')]
            self.assertEqual(len(gate), 1)
            self.assertTrue(gate[0].endswith('--suite ' + suite))

    def test_cluster_entry_keeps_old_suite_and_does_not_repeat_unit_rendering(self):
        root = Path(__file__).resolve().parents[1]
        for file, tags in (('.github/workflows/ci.yml', 'e2e,osvault'), ('scripts/e2e.sh', 'e2e')):
            text = (root / file).read_text()
            commands = [line.strip() for line in text.splitlines() if 'go test -race -tags=' in line
                        and ('./e2e/...' in line or './internal/helmrender/...' in line)]
            self.assertEqual(len(commands), 2)
            self.assertIn('go test -race -tags=' + tags + ' -shuffle=on -count=1 -timeout=12m -json ./e2e/... ./internal/ui/... | tee ', commands[0])
            self.assertNotIn(' -run ', commands[0])
            self.assertNotIn('./internal/helmrender/...', commands[0])
            self.assertIn("go test -race -tags=e2e -shuffle=on -run '^TestRealHelmUninstallContractResourcesAndHistory$' -count=1 -timeout=12m -json ./internal/helmrender/... | tee -a ", commands[1])
            self.assertEqual(commands[0].split(' | tee ', 1)[1], commands[1].split(' | tee -a ', 1)[1])
            self.assertNotIn('|| true', '\n'.join(commands))
        # The original renderer unit suite remains required in BOTH full unit
        # and coverage executions; only unintended cluster duplication is removed.
        self.assertIn('TestHelmRealTemplateSnapshotAndFailure', contract_evidence.required('unit-native'))
        self.assertEqual(len(contract_evidence.REQUIRED['real-cluster-native']), 8)

    def test_split_cluster_pipeline_retains_failures_and_appended_raw_stream(self):
        root = Path(__file__).resolve().parents[1]
        for file in ('.github/workflows/ci.yml', 'scripts/e2e.sh'):
            text = (root / file).read_text()
            commands = [line.strip() for line in text.splitlines() if 'go test -race -tags=' in line
                        and ('./e2e/...' in line or './internal/helmrender/...' in line)]
            for fail in ('none', 'original', 'contract'):
                with self.subTest(file=file, fail=fail), tempfile.TemporaryDirectory() as directory:
                    output = Path(directory) / 'tests.jsonl'
                    script = """set -euo pipefail
calls=0
go() {
    calls=$((calls + 1))
    case "$*" in
      *./e2e/...*) printf '%s\n' '{"Action":"pass","Package":"old","Test":"TestOld"}'; [[ "$FAIL" != original ]] ;;
      *./internal/helmrender/...*) printf '%s\n' '{"Action":"pass","Package":"new","Test":"TestNew"}'; [[ "$FAIL" != contract ]] ;;
      *) return 90 ;;
    esac
}
python() { shift; go "${@:2}"; }
"""
                    for command in commands:
                        command = command.split(' | tee', 1)[0] + (' | tee -a ' if ' | tee -a ' in command else ' | tee ') + str(output)
                        script += command + '\n'
                    result = subprocess.run(['bash', '-c', script], env={'PATH':'/usr/bin:/bin','FAIL':fail},
                                            capture_output=True, text=True, timeout=5)
                    self.assertEqual(result.returncode == 0, fail == 'none')
                    records = [json.loads(line) for line in output.read_text().splitlines()]
                    self.assertEqual([item['Package'] for item in records], ['old'] if fail == 'original' else ['old','new'])

    def test_coverage_retains_original_suite_and_failure_with_raw_evidence(self):
        text = (Path(__file__).resolve().parents[1] / '.github/workflows/ci.yml').read_text()
        block = text.split('      - name: Coverage\n', 1)[1].split('      - name:', 1)[0]
        self.assertIn('        shell: bash\n', block)
        self.assertIn('          set -euo pipefail\n', block)
        self.assertIn('go test -coverprofile=artifacts/unit/coverage.out -json ./... ', block)
        self.assertIn('2>artifacts/unit/coverage/stderr.log | tee artifacts/unit/coverage/tests.jsonl', block)
        self.assertIn('python3 scripts/helm_uninstall_contract_evidence.py artifacts/unit/coverage/tests.jsonl --suite unit-native', block)
        self.assertNotIn('continue-on-error', block)
        self.assertNotIn('|| true', block)
        self.assertNotIn('-run ', block)
        self.assertNotIn('-timeout', block)

    def test_test_only_execution_and_disposable_cluster_guards(self):
        root = Path(__file__).resolve().parents[1]
        hermetic = (root / 'internal/helmrender/uninstall_contract_test.go').read_text()
        cluster = (root / 'internal/helmrender/uninstall_contract_e2e_test.go').read_text()
        self.assertTrue(cluster.startswith('//go:build e2e\n'))
        self.assertIn('observeHelmUninstallTargetAbsent(ctx, 3*time.Second, cm.UID', cluster)
        self.assertNotIn('expected real target deletion', cluster)
        for marker in ('readLocal(', 'sha256.Sum256', 'unpackExecutable(', 'isolatedEnvironment(',
                       'exec.CommandContext', 'cmd.WaitDelay', 'httptest.NewServer', 'entry.Options.Preconditions',
                       'observeHelmUninstallTargetAbsent(', 'PollUntilContextTimeout(', 'apierrors.IsNotFound('):
            self.assertIn(marker, hermetic)
        for marker in ('testcluster.Config(t)', 'IsLoopback()', 'cfg.Insecure', 'cfg.ExecProvider',
                       'clientcmd.Write(raw)', 'GenerateName:', 't.Cleanup(', 'PreviewHelmUninstall(',
                       'ObserveHelmPreview(', 'reflect.DeepEqual', 'originalUID'):
            self.assertIn(marker, cluster)
        for file in ('.github/workflows/ci.yml', 'scripts/e2e.sh'):
            self.assertIn('./internal/helmrender/...', (root / file).read_text())
            self.assertIn('scripts/helm_uninstall_contract_evidence.py', (root / file).read_text())
        contract = (root / 'docs/helm-uninstall-contract.md').read_text()
        for boundary in ('NOT enabled', 'Unknown', 'UID', 'resourceVersion', 'No automatic retry',
                         'sole stored revision', 'P3', 'not activated'):
            self.assertIn(boundary, contract)

if __name__ == '__main__':
    unittest.main()
