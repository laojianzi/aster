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

    def test_added_test_exemption_set_is_pinned(self):
        # A path in ADDED_TEST_FILES is exempt from the census forever, so edits
        # to it stop being detectable. The exemption list therefore has to be a
        # reviewed constant, not an append-only convenience. Growing it by one
        # path means editing this digest in the same change.
        self.assertEqual(len(execution.ADDED_TEST_FILES), 34)
        self.assertEqual(hashlib.sha256(
            '\n'.join(sorted(execution.ADDED_TEST_FILES)).encode()).hexdigest(),
            'c5b6af9b63817d3f4dacf6135c0f5a110896fffd6ff9397ba119937c799fc3cb')
        # Only test files may be exempted; otherwise the list becomes a way to
        # hide production source from the census.
        for path in execution.ADDED_TEST_FILES:
            self.assertTrue(path.endswith('_test.go'), path)

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
        # Re-frozen for the delivery float32 comparison correction. The
        # 250-test census and 265 rows are unchanged; one row moved:
        # delivery_test.go now compares b.W+0.01 < changeWidth. Real CI run
        # 42601061257 printed changeWidth=202.07295 against b.W=202.07294
        # on macOS -- one float32 ULP, with bottom/right/H/ok all passing.
        # A real clip (211 vs 229 at scale 2) still fails the new condition,
        # so no expectation was relaxed. The Wrap() experiment it prompted
        # was reverted: the geometry was byte-identical to run 42596603353.
        # Retired: a08e0de7... (runner budget + diagnostics),
        # aabe72c2... (same-Kind call sites), e8edff95... (b48981a),
        # 0f57c72c... (86193f7), bef187aa... (2ac9161), 261f30e8... (8de1541),
        # 0474a120... (v0.7.3).
        self.assertEqual(hashlib.sha256('\n'.join(sorted(rows)).encode()).hexdigest(),
                         '562372e38715eee175b1ddf6418839f4ee33da617c44f852b240a19ec8c194cb')

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
