"""Install prerequisites add mandatory evidence without enabling any writes."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
import helm_install_evidence as install
import helm_uninstall_contract_evidence as contract
import test_evidence as evidence

ROOT = Path(__file__).resolve().parents[1]
# Independent d19a692 baseline. Updating a workflow/pin requires a new scope.
FROZEN = {'.github/workflows/branch-cleanup.yml': 'dc31f44ae8aedf3f2cdc3f34d908e938d3fb0f7870db4adf1e75e63f302212c0',
 '.github/workflows/ci.yml': '7ee11db7b12f95aac5c21c4800848bcbf8ca034aefd5676d01534fa8bbb4b1c6',
 '.github/workflows/native-migration.yml': '899e1b524b26ab3396b65e4d2acc1788575ffd4b7576f6976a62bccc9289cb33',
 '.github/workflows/offline-devkit.yml': '12fdf79c9baef63ec1e0e8666dbf297f17ab7b421679f4479d65a62a95c2e405',
 '.github/workflows/oidc-qualification.yml': 'f808d03d07563ae4ac7004b7b644820c9e5ecf203154b896fbb3fbc424b4f7b0',
 '.github/workflows/renovate-config.yml': '08aa8dd8b9e5908d6d6a4c122d5081dee53f08345b3f83bfc5e27ec914ea7a24',
 '.github/workflows/retire-completed-oidc.yml': '36b231f83ebc0f79c93dff94966e638197fbb3d8cce359ac9878ee1dcedfb07b',
 '.github/workflows/source-evidence.yml': '6e1c5b4508b9cf184d16a03347f3f29e1c970599ff5ba52a064a57dfb855d072',
 '.github/workflows/terminal-devkit.yml': '5502c443df4dd085b547f4c0cc00c4814e36ee15b041156d35ea40e25ab6d0d0',
 'go.mod': 'ced4d9f572eb1cab3b8835856721d92f552452c58f55685c0274241c8677b5a9',
 'go.sum': '5310164c6636b92fc32f7b8ae4019f2246e0d06c9ecfa607f38b83a9446ef0c2',
 'internal/helmrender/distributions.json': '50ffd7dcb3430899e5e577466eddca52fa4461b7981c42834220650c495475f2',
 'third_party/native-terminal/UPSTREAM.json': 'c98eaa315a57173a2892ecd5c45c7b2d86887ede8b655921dc0ff89fcd62121a'}

class HelmInstallScopeTests(unittest.TestCase):
    def test_exact_required_census_and_additive_gates(self):
        expected = {"unit-native": 117, "real-cluster-native": 9}
        for suite,names in install.INSTALL_REQUIRED.items():
            self.assertEqual(len(names),expected[suite])
            self.assertEqual(len(names),len(set(names)))
            required=contract.required(suite)
            self.assertTrue(set(names)<=set(required))
            self.assertEqual(len(required),len(set(required)))
            for old in (contract.REQUIRED,contract.STATUS_TRANSITION_REQUIRED,
                        contract.UNINSTALL_PREPARATION_REQUIRED,contract.UNINSTALL_DRY_RUN_REQUIRED,
                        contract.UNINSTALL_EXECUTION_REQUIRED,contract.UNINSTALL_RECOVERY_REQUIRED,
                        contract.JOURNAL_ADMISSION_REQUIRED,contract.JOURNAL_ARCHIVE_REQUIRED):
                self.assertTrue(set(old.get(suite,()))<=set(required))
        self.assertFalse(set(install.INSTALL_REQUIRED["unit-native"]) &
                         set(install.INSTALL_REQUIRED["real-cluster-native"]))

    def test_each_name_needs_one_complete_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            p=Path(tmp)/"tests.jsonl"
            for suite,names in install.INSTALL_REQUIRED.items():
                for selected in names:
                    for mode in ("pass","fail","skip","missing","unfinished","duplicate"):
                        with self.subTest(suite=suite,name=selected,mode=mode):
                            events=[]
                            for name in names:
                                if name==selected and mode=="missing": continue
                                events.append(dict(Action="run",Package="fixture",Test=name))
                                if name==selected and mode=="unfinished": continue
                                action=mode if name==selected and mode in ("fail","skip") else "pass"
                                events.append(dict(Action=action,Package="fixture",Test=name))
                                if name==selected and mode=="duplicate":
                                    events.append(dict(Action="pass",Package="fixture",Test=name))
                            events.append(dict(Action="pass",Package="fixture"))
                            p.write_text("".join(json.dumps(e)+"\n" for e in events))
                            self.assertEqual(evidence.summarize(p,names,"install-fixture")["valid"],mode=="pass")

    def test_fixed_workflows_and_provenance(self):
        for path,digest in FROZEN.items():
            with self.subTest(path=path):
                self.assertEqual(hashlib.sha256((ROOT/path).read_bytes()).hexdigest(),digest)

    def test_private_capture_has_no_public_payload_or_alternate_cli(self):
        text=(ROOT/"internal/helmrelease/install_capture.go").read_text()
        self.assertIn("payload string",text)
        self.assertIn("func (c InstallCandidate) MarshalJSON()",text)
        self.assertIn("func (c InstallCandidate) Format(",text)
        capture=(ROOT/"internal/helmrender/install_capture.go").read_text()
        self.assertIn('version != "v4.3.0"',capture)
        self.assertIn("credentialexec.RunRenderHelper(",capture)
        self.assertIn('"--dry-run=client"',capture)
        self.assertIn("isolatedEnvironment(root)",capture)
        self.assertIn("hex.EncodeToString(digest[:]) != d.SHA256",capture)
        self.assertNotIn('"--kube-version"',capture)
        for forbidden in ("os/exec","exec.Command","LoadKubeconfig","--dry-run=server","--no-hooks","--hide-secret"):
            self.assertNotIn(forbidden,capture)

    def test_preparation_and_codecs_have_no_write_dispatch(self):
        for name in ("internal/helmrelease/install_capture.go","internal/helmrelease/install_storage.go","internal/helmrelease/install_metadata.go",
                     "internal/kube/helm_install_preparation.go"):
            text=(ROOT/name).read_text()
            for forbidden in ("http.MethodPost","http.MethodPatch","http.MethodPut","http.MethodDelete",
                              ".Create(", ".Patch(", ".Update(", ".Delete(", "store.Begin(", "exec.Command"):
                self.assertNotIn(forbidden,text)
        text=(ROOT/"internal/kube/helm_install_preparation.go").read_text()
        self.assertEqual(text.count("b.helmReleaseMetadata(ctx, c.Namespace(), helmrelease.DecodeInstallMetadataPage)"),2)
        self.assertEqual(text.count('b.installAbsent(ctx, c, "Secret", "secrets", c.StorageName()'),2)
        self.assertIn("reflect.DeepEqual(before.Revisions, after.Revisions)",text)
        self.assertIn("p.candidate == c",text)
        for path in (ROOT/"internal/ui").glob("*.go"):
            if not path.name.endswith("_test.go"):
                self.assertNotIn("CaptureInstall(",path.read_text())
                self.assertNotIn("PrepareHelmInstall(",path.read_text())

    def test_real_server_fixture_guards_every_request_and_canary(self):
        text=(ROOT/"e2e/helm_install_preparation_test.go").read_text()
        for token in ("//go:build e2e","testcluster.NewHelmMetadata(t, 0)","http.MethodGet",
                      "reflect.DeepEqual(got, want)","competing-release","unlabeled-reserved-secret",
                      "rest.ImpersonationConfig","raced.CompareAndSwap"):
            self.assertIn(token,text)

if __name__=="__main__":
    unittest.main()
