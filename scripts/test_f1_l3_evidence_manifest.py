"""Offline regression gate for the F1 L3 local evidence-manifest generator.

CRITICAL: /artifacts/ is .gitignore'd, so in a clean CI checkout EVERY real
anchor is absent.  These tests therefore never touch the real evidence tree --
they patch ANCHORS and the tracked-path lookup to a temporary directory.  That
also proves the generator itself is usable in a clean checkout, where its job
is to REPORT missing/untracked anchors, not to assume they exist.

The real-tree exercise (generate -> verify -> tamper -> restore) is run
separately by the team lead and recorded in the readiness docs; it is NOT a
unit test, because asserting "all 53 anchors exist" would fail in CI.
"""

from __future__ import annotations

import contextlib
import io
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import f1_l3_evidence_manifest as em

# Saved before setUp patches ANCHORS to the temporary fixture.
_REAL_ANCHORS = em.ANCHORS


class ManifestGeneratorTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        (self.root / "alpha.txt").write_bytes(b"alpha")
        (self.root / "beta.txt").write_bytes(b"beta")
        self.fake_anchors = (
            ("gate_kernel", "alpha.txt", "retrieval-alpha", "note-alpha"),
            ("original_evidence", "beta.txt", "retrieval-beta", "note-beta"),
        )
        self.manifest = self.root / "manifest.json"
        self.patches = [
            mock.patch.object(em, "ANCHORS", self.fake_anchors),
            mock.patch.object(em, "_tracked_paths", return_value={"alpha.txt"}),
        ]
        for p in self.patches:
            p.start()
            self.addCleanup(p.stop)

    def _quiet(self, fn, *args, **kwargs):
        buf = io.StringIO()
        with contextlib.redirect_stdout(buf):
            result = fn(*args, **kwargs)
        return result, buf.getvalue()

    def test_collect_hashes_every_anchor_and_reports_tracking(self):
        rows = em.collect(self.root)
        self.assertEqual(len(rows), 2)
        by_path = {r["path"]: r for r in rows}
        self.assertEqual(by_path["alpha.txt"]["sha256"],
                         em._sha256(self.root / "alpha.txt"))
        self.assertEqual(by_path["beta.txt"]["bytes"], 4)
        self.assertTrue(by_path["alpha.txt"]["tracked"])
        self.assertFalse(by_path["beta.txt"]["tracked"])
        self.assertTrue(all(r["exists"] for r in rows))
        self.assertEqual(by_path["alpha.txt"]["retrieval"], "retrieval-alpha")

    def test_missing_anchor_is_reported_not_raised(self):
        with mock.patch.object(em, "ANCHORS",
                               self.fake_anchors + (("x", "gone.txt", "r", ""),)):
            rows = em.collect(self.root)
        gone = next(r for r in rows if r["path"] == "gone.txt")
        self.assertFalse(gone["exists"])
        self.assertIsNone(gone["sha256"])
        self.assertIsNone(gone["bytes"])

    def test_bundle_hash_is_stable_and_content_sensitive(self):
        rows = em.collect(self.root)
        self.assertEqual(em.bundle_hash(rows), em.bundle_hash(rows))
        self.assertEqual(len(em.bundle_hash(rows)), 64)
        (self.root / "beta.txt").write_bytes(b"BETA!")
        self.assertNotEqual(em.bundle_hash(rows),
                            em.bundle_hash(em.collect(self.root)))

    def test_build_status_never_claims_l3_or_f1_fixed(self):
        doc = em.build(self.root)
        status = doc["status"]
        self.assertEqual(status["f1"], "FAIL_UNFIXED")
        self.assertEqual(status["evidence_level"], "L1_L2_READINESS_ONLY")
        self.assertFalse(status["l3_replay_performed"])
        self.assertIsNone(status["human_signoff"])
        self.assertFalse(status["ci_dispatched"])
        self.assertFalse(status["product_source_changed"])
        self.assertIn("DEFERRED", status["m_w"])
        self.assertEqual(doc["manifest_version"], em.MANIFEST_VERSION)
        self.assertEqual(doc["counts"]["anchors_total"], 2)
        self.assertEqual(doc["bundle_sha256"], em.bundle_hash(doc["anchors"]))

    def test_verify_ok_then_detects_a_tampered_hash(self):
        doc = em.build(self.root)
        self.manifest.write_text(json.dumps(doc, ensure_ascii=False) + "\n",
                                 encoding="utf-8")
        code, out = self._quiet(em.verify, self.root, self.manifest)
        self.assertEqual(code, 0, out)
        self.assertIn("VERIFY_OK", out)

        doc["anchors"][0]["sha256"] = "0" * 64
        self.manifest.write_text(json.dumps(doc, ensure_ascii=False) + "\n",
                                 encoding="utf-8")
        code, out = self._quiet(em.verify, self.root, self.manifest)
        self.assertEqual(code, 1, out)
        self.assertIn("VERIFY_FAIL", out)
        self.assertIn("CHANGED", out)

    def test_verify_detects_real_file_drift_and_missing_manifest(self):
        doc = em.build(self.root)
        self.manifest.write_text(json.dumps(doc, ensure_ascii=False) + "\n",
                                 encoding="utf-8")
        (self.root / "alpha.txt").write_bytes(b"alpha-DRIFT")
        code, out = self._quiet(em.verify, self.root, self.manifest)
        self.assertEqual(code, 1, out)
        self.assertIn("alpha.txt", out)

        code, out = self._quiet(em.verify, self.root, self.root / "nope.json")
        self.assertEqual(code, 1, out)
        self.assertIn("manifest not found", out)

    def test_main_write_then_verify_round_trip(self):
        code, out = self._quiet(
            em.main, ["--root", str(self.root), "--manifest", str(self.manifest)])
        self.assertEqual(code, 0, out)
        self.assertTrue(self.manifest.is_file())
        code, out = self._quiet(
            em.main, ["--root", str(self.root), "--manifest", str(self.manifest),
                      "--verify"])
        self.assertEqual(code, 0, out)
        self.assertIn("VERIFY_OK", out)

    def test_real_anchors_are_declared_with_retrieval_and_status(self):
        """Guard the DECLARATION, not the files: CI has no artifacts/ tree."""
        paths = {a[1] for a in _REAL_ANCHORS}
        self.assertIn(
            "artifacts/native-redesign/native-object-sections/acceptance.py", paths)
        self.assertIn("artifacts/design-spec/03-UX-SPEC.md", paths)
        self.assertIn("go.mod", paths)
        self.assertTrue(any(p.startswith("$MOD/") for p in paths))
        for _group, path, retrieval, _note in _REAL_ANCHORS:
            self.assertTrue(retrieval.strip(), path)
            self.assertFalse(path.startswith("/"), path)


if __name__ == "__main__":
    unittest.main()
