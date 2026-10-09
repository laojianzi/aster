"""Verify the disposable Metrics installer rejects unsafe contexts before I/O.

kubectl/curl are isolated stand-ins; these are guard tests, not cluster E2E.
"""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().with_name("install-e2e-metrics.sh")

class InstallerGuardTest(unittest.TestCase):
    def invoke(self, **overrides):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            marker = root / "effects"
            program = "#!" + sys.executable + "\n" + '''import json, os, pathlib, sys
args = sys.argv[1:]
if pathlib.Path(sys.argv[0]).name == "curl":
    pathlib.Path(os.environ["TEST_MARKER"]).write_text("download-attempt")
    sys.exit(3)
if args == ["config", "current-context"]:
    print(os.environ["TEST_CONTEXT"])
elif args[:2] == ["config", "view"]:
    print(json.dumps({"clusters":[{"cluster":{"server":os.environ["TEST_SERVER"]}}]}))
else:
    pathlib.Path(os.environ["TEST_MARKER"]).write_text("UNEXPECTED kubectl write")
    sys.exit(4)
'''
            for name in ("kubectl", "curl"):
                p = root / name
                p.write_text(program)
                p.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ["PATH"],
                       KUBECONFIG=str(root / "config"), ASTER_E2E_ALLOW_DESTRUCTIVE="1",
                       ASTER_E2E_CONTEXT="kind-aster-e2e-local", TEST_CONTEXT="kind-aster-e2e-local",
                       TEST_SERVER="https://127.0.0.1:6443", TEST_MARKER=str(marker),
                       PYTHONOPTIMIZE="1")
            env.update(overrides)
            result = subprocess.run(["bash", str(SCRIPT)], env=env, capture_output=True,
                                    text=True, timeout=5)
            return result.returncode, marker.read_text() if marker.exists() else ""

    def test_unsafe_contexts_stop_before_download_or_mutation(self):
        cases = [dict(ASTER_E2E_ALLOW_DESTRUCTIVE=""), dict(TEST_CONTEXT="production"),
                 dict(ASTER_E2E_CONTEXT="kind-production", TEST_CONTEXT="kind-production"),
                 dict(TEST_SERVER="https://192.0.2.10:6443"), dict(TEST_SERVER="http://127.0.0.1:6443")]
        for case in cases:
            with self.subTest(case=case):
                code, effects = self.invoke(**case)
                self.assertNotEqual(code, 0)
                self.assertEqual(effects, "")

    def test_owned_loopback_prefix_reaches_download_but_cannot_apply_on_error(self):
        code, effects = self.invoke()
        self.assertNotEqual(code, 0)
        self.assertEqual(effects, "download-attempt")

if __name__ == "__main__":
    unittest.main()
