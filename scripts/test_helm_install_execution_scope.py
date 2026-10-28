"""Execution is additive and cannot silently activate native writes or erase gates."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

import helm_install_execution_evidence as execution
import helm_uninstall_contract_evidence as contract
import test_evidence as evidence

ROOT = Path(__file__).resolve().parents[1]

class InstallExecutionScopeTests(unittest.TestCase):
    def test_mandatory_names_are_unique_and_additive(self):
        for suite, names in execution.INSTALL_EXECUTION_REQUIRED.items():
            self.assertEqual(len(names), len(set(names)))
            required = contract.required(suite)
            self.assertEqual(len(required), len(set(required)))
            self.assertTrue(set(names) <= set(required))
        self.assertEqual(len(execution.INSTALL_EXECUTION_REQUIRED['unit-native']), 120)
        self.assertEqual(len(execution.INSTALL_EXECUTION_REQUIRED['real-cluster-native']), 5)

    def test_every_new_requirement_must_run_and_finish_once(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'tests.jsonl'
            for suite, names in execution.INSTALL_EXECUTION_REQUIRED.items():
                for name in names:
                    for mode in ('pass', 'missing', 'fail', 'skip', 'unfinished', 'duplicate'):
                        events = []
                        for other in names:
                            if other == name and mode == 'missing':
                                continue
                            events.append(dict(Action='run', Package='fixture', Test=other))
                            if other == name and mode == 'unfinished':
                                continue
                            action = mode if other == name and mode in ('fail', 'skip') else 'pass'
                            events.append(dict(Action=action, Package='fixture', Test=other))
                            if other == name and mode == 'duplicate':
                                events.append(events[-1])
                        events.append(dict(Action='pass', Package='fixture'))
                        path.write_text(''.join(json.dumps(e)+'\n' for e in events))
                        self.assertEqual(evidence.summarize(path, list(names), suite)['valid'], mode == 'pass')

    def test_exact_250_existing_go_tests_workflows_pins_preserved(self):
        rows, count = [], 0
        index = subprocess.check_output(['git','ls-files','--stage','-z'],cwd=ROOT)
        for entry in index.split(b'\0'):
            if not entry:
                continue
            header, path = entry.decode().split('\t',1)
            mode, _, stage = header.split()
            self.assertEqual(stage, '0')
            protected = (path.endswith('_test.go') or path.startswith('.github/workflows/')
                         or path in ('go.mod','go.sum') or path.startswith('third_party/native-terminal/'))
            if protected and path not in execution.ADDED_TEST_FILES:
                count += path.endswith('_test.go')
                rows.append(path+'\0'+mode+'\0'+hashlib.sha256((ROOT/path).read_bytes()).hexdigest())
        self.assertEqual(count,250)
        self.assertEqual(len(rows),265)
        self.assertEqual(hashlib.sha256('\n'.join(sorted(rows)).encode()).hexdigest(),
                         '261f30e834d8aa7b6262e892c43bccfae87103bc41530fce8951dfcf1d62d78b')

    def test_native_controls_remain_disabled_and_store_reused(self):
        for p in (ROOT/'internal/ui').glob('*.go'):
            if not p.name.endswith('_test.go'):
                self.assertNotIn('ExecuteHelmInstall(',p.read_text())
                self.assertNotIn('AcknowledgeHelmInstallExecution(',p.read_text())
        src = (ROOT/'internal/kube/helm_install_execution.go').read_text()
        for token in ('s.store.BeginInstall(p)', 'origin.executionMinted = true',
                      'origin.preparation.ExpiresAt()', 'writer.Finish('):
            self.assertIn(token,src)
        transport = (ROOT/'internal/kube/helm_install_execution_transport.go').read_text()
        self.assertLess(transport.index('r.journal.Intent('),transport.index('r.b.metricsHTTP.Do(req)'))
        self.assertIn('req.GetBody = nil',transport)
        self.assertNotIn('http.MethodDelete',transport)
        self.assertNotIn('q.Set("dryRun"',transport)
        self.assertNotIn('http.MethodPut',transport)
