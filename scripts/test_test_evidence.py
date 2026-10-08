import json
from pathlib import Path
import tempfile
import unittest

from test_evidence import summarize


class EvidenceTests(unittest.TestCase):
    def check_events(self, events, required=None):
        with tempfile.TemporaryDirectory() as tmp:
            file = Path(tmp) / "tests.jsonl"
            file.write_text("\n".join(json.dumps(e) for e in events), encoding="utf-8")
            return summarize(file, required or [], "self-test")

    def test_absent_evidence_is_not_success(self):
        self.assertFalse(self.check_events([])["valid"])

    def test_skipped_missing_unfinished_and_package_failure_are_failures(self):
        start = {"Package": "pkg", "Test": "TestReal", "Action": "run"}
        for final in (None, "skip", "fail"):
            events = [start] + ([dict(start, Action=final)] if final else [])
            self.assertFalse(self.check_events(events)["valid"])
        passed = [start, dict(start, Action="pass"), {"Package": "pkg", "Action": "pass"}]
        self.assertFalse(self.check_events(passed, ["TestMissing"])["valid"])
        self.assertFalse(self.check_events(passed + [{"Package": "pkg", "Action": "fail"}])["valid"])
        self.assertTrue(self.check_events(passed, ["TestReal"])["valid"])

    def test_retry_does_not_erase_a_failure_or_skip(self):
        start = {"Package": "pkg", "Test": "TestReal", "Action": "run"}
        for prior in ("skip", "fail"):
            events = [start, dict(start, Action=prior), start, dict(start, Action="pass"), {"Package": "pkg", "Action": "pass"}]
            self.assertFalse(self.check_events(events)["valid"])

    def test_truncated_package_evidence_is_not_success(self):
        start = {"Package": "pkg", "Test": "TestReal", "Action": "run"}
        self.assertFalse(self.check_events([start, dict(start, Action="pass")])["valid"])


if __name__ == "__main__":
    unittest.main()
