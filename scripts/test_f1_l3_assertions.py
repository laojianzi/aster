"""Offline regression gate for the F1 L3 acceptance assertions.

All bounds below are transcribed verbatim from the 2026-10-28 evidence in
artifacts/native-redesign/native-object-sections/{protocol,verification}.json
and from the local headless probes on b402999 / d345a4a.  They are inlined so
this gate runs in a clean checkout where /artifacts/ is .gitignore'd.

The decisive test is ``test_old_failure_evidence_is_rejected``: the original
acceptance checked Object workspace > 150px and passed all 12 geometry checks
while the YAML editor was 80px tall.  The new gate must reject that exact rect.
"""

from __future__ import annotations

import unittest

import f1_l3_assertions as f1


# --- recorded evidence -----------------------------------------------------
# 1440x960 / scale 1 / scheme 1 (dark): protocol.json tree responses 33-39.
FULL_CASE_BOUNDS = {"x": 236, "y": 305.5, "width": 1188, "height": 604.5}
FULL_CASE_WINDOW = (1440, 960)
FULL_CASE_WORKSPACE = {"x": 220, "y": 141.5, "width": 1220, "height": 784.5}

# 900x700 / scale 2 / scheme 2 (light): protocol.json tree responses 95-101,
# verification.json field.detailText bounds.  This is the F1 failure rect.
F1_CASE_BOUNDS = {"x": 376, "y": 574, "width": 508, "height": 80}
F1_CASE_WINDOW = (900, 700)
F1_CASE_WORKSPACE = {"x": 376, "y": 406, "width": 508, "height": 233}

# Local headless probes: b402999 (pre-fix) and d345a4a (post-fix).
PREFIX_BOUNDS = {"x": 376, "y": 571, "width": 508, "height": 68}
FIXED_BOUNDS = {"x": 8, "y": 341, "width": 884, "height": 306}


def _node(bounds, role="textField",
          states=("focusable", "multiline", "readOnly")):
    return {"key": f1.DOCUMENT_KEY, "label": f1.DOCUMENT_LABEL,
            "role": role, "bounds": dict(bounds), "states": list(states)}


class VerifyDocumentRectTests(unittest.TestCase):
    def test_old_failure_evidence_is_rejected(self):
        """DECISIVE: the new gate must catch what the 12 old checks missed."""
        ok, reasons = f1.verify_document_rect(
            F1_CASE_BOUNDS, case="900x700-scale-2", window=F1_CASE_WINDOW)
        self.assertFalse(ok)
        self.assertTrue(any("height_readable" in r for r in reasons), reasons)

    def test_legacy_weak_assertion_would_still_pass(self):
        """Root cause: workspace 233/784.5px passed while editor was 80px."""
        self.assertTrue(f1.legacy_workspace_check(FULL_CASE_WORKSPACE))
        self.assertTrue(f1.legacy_workspace_check(F1_CASE_WORKSPACE))
        self.assertEqual(F1_CASE_BOUNDS["height"], 80)

    def test_full_case_evidence_passes(self):
        ok, reasons = f1.verify_document_rect(
            FULL_CASE_BOUNDS, case="1440x960-scale-1", window=FULL_CASE_WINDOW)
        self.assertTrue(ok, reasons)

    def test_prefix_rect_rejected_and_fixed_rect_accepted(self):
        self.assertFalse(f1.verify_document_rect(
            PREFIX_BOUNDS, case="b402999", window=F1_CASE_WINDOW)[0])
        self.assertTrue(f1.verify_document_rect(
            FIXED_BOUNDS, case="d345a4a", window=F1_CASE_WINDOW)[0])

    def test_threshold_boundary_is_exact(self):
        for h, expected in ((119.9, False), (120.0, True), (120.1, True),
                            (0.0, False), (-1.0, False)):
            b = dict(FIXED_BOUNDS, height=h, y=0)
            with self.subTest(h=h):
                self.assertEqual(
                    f1.verify_document_rect(b, window=F1_CASE_WINDOW)[0], expected)

    def test_zero_width_and_out_of_window_are_rejected(self):
        variants = [
            dict(FIXED_BOUNDS, width=0),
            dict(FIXED_BOUNDS, x=-1),
            dict(FIXED_BOUNDS, y=-1),
            dict(FIXED_BOUNDS, x=100, width=900),   # right = 1000 > 900
            dict(FIXED_BOUNDS, y=400, height=400),  # bottom = 800 > 700
        ]
        for b in variants:
            with self.subTest(bounds=b):
                self.assertFalse(f1.verify_document_rect(
                    b, window=F1_CASE_WINDOW)[0])

    def test_non_yaml_sections_are_skipped(self):
        for section in ("Overview", "Related", "Events"):
            with self.subTest(section=section):
                ok, reasons = f1.verify_document_rect(
                    F1_CASE_BOUNDS, window=F1_CASE_WINDOW, section=section)
                self.assertTrue(ok)
                self.assertIn("skipped", reasons[0])
                self.assertEqual(f1.evaluate_document(
                    _node(F1_CASE_BOUNDS), 900, 700,
                    section=section)["result"], "skipped")

    def test_invalid_inputs_fail_without_raising(self):
        for bad in (None, {}, {"x": 0}, {"x": "a", "y": 0, "width": 1, "height": 1},
                    {"x": float("nan"), "y": 0, "width": 1, "height": 200},
                    {"x": 0, "y": 0, "width": 1, "height": float("inf")}):
            with self.subTest(bounds=bad):
                ok, reasons = f1.verify_document_rect(bad, window=F1_CASE_WINDOW)
                self.assertFalse(ok)
                self.assertTrue(reasons)
        self.assertFalse(f1.verify_document_rect(
            FIXED_BOUNDS, window=(0, 0))[0])
        self.assertFalse(f1.verify_document_rect(
            FIXED_BOUNDS, window=F1_CASE_WINDOW, min_height=0)[0])

    def test_assert_helper_raises_on_failure_and_returns_verdict_on_pass(self):
        with self.assertRaises(f1.DocumentGeometryError) as ctx:
            f1.assert_readable_document(
                _node(F1_CASE_BOUNDS), 900, 700, case="900x700-scale-2")
        self.assertIn("80", str(ctx.exception))
        verdict = f1.assert_readable_document(
            _node(FIXED_BOUNDS), 900, 700, case="d345a4a")
        self.assertEqual(verdict["result"], "PASS")
        self.assertEqual(verdict["evidence_level"], "L2_geometry")

    def test_role_and_states_are_checked_by_evaluate_document(self):
        self.assertEqual("FAIL_VISUAL_USABILITY", f1.evaluate_document(
            _node(FIXED_BOUNDS, role="button"), 900, 700)["result"])
        self.assertEqual("FAIL_VISUAL_USABILITY", f1.evaluate_document(
            _node(FIXED_BOUNDS, states=("focusable",)), 900, 700)["result"])


class CompactMirrorTests(unittest.TestCase):
    def test_compact_matches_the_f1_matrix(self):
        # 900x700/2x triggers compact (F1 case); 1100x700/2.5x must NOT.
        self.assertTrue(f1.compact_for(900, 700, 2))
        self.assertFalse(f1.compact_for(1100, 700, 2.5))
        self.assertFalse(f1.compact_for(1440, 960, 1))
        self.assertTrue(f1.compact_for(1100, 599, 2))   # height branch
        self.assertTrue(f1.compact_for(1100, 299, 1))   # height branch, scale 1
        self.assertFalse(f1.compact_for(1100, 700, 2))
        self.assertFalse(f1.compact_for(900, 700, None))
        # 900x480 tiling at scale 1 does NOT trigger compact: 480 >= 300 and
        # 900 >= 550.  This is why protocol section 4 registers 900x480 as a
        # separate design/acceptance item rather than an F1 matrix cell.
        self.assertFalse(f1.compact_for(900, 480, 1))

    def test_full_navigation_uses_tabs_and_direct_close(self):
        labels = f1.navigation_labels("YAML", compact=False)
        self.assertIn(("Close detail", "button"), labels)
        self.assertIn(("Refresh detail", "button"), labels)
        self.assertIn(("YAML", "tab"), labels)
        self.assertNotIn(("Object sections", "select"), labels)

    def test_compact_navigation_never_waits_on_removed_tabs(self):
        """acceptance.py:126-128 used to time out waiting for these labels."""
        labels = f1.navigation_labels("YAML", compact=True)
        rendered = {label for label, _ in labels}
        for missing in ("Close detail", "Refresh detail", "Overview", "Related", "Events"):
            self.assertNotIn(missing, rendered, missing)
        self.assertIn("Object sections", rendered)
        self.assertIn("Resource document", rendered)
        self.assertIn("YAML", rendered)  # current section name stays visible
        self.assertEqual(f1.COMPACT_FOLDED_ACTIONS,
                         ("Close detail", "Refresh detail"))

    def test_constants_match_the_protocol(self):
        self.assertEqual(f1.MIN_READABLE_DOCUMENT_HEIGHT, 120.0)
        self.assertEqual(f1.LEGACY_WORKSPACE_HEIGHT, 150.0)
        self.assertEqual(f1.DOCUMENT_KEY, "field.detailText")
        self.assertEqual(f1.DOCUMENT_LABEL, "Resource document")


class SchemeConsistencyTests(unittest.TestCase):
    """Offline L1/L2 gate for the section-4 light/dark mislabel.

    ``dark`` is MEASURED (cross-checked against appearance-checks.json and the
    captured background_rgb), never derived from portal_color_scheme.  Protocol
    section 4 labelled the light 900x700 / 2x F1 evidence "dark"; that is the
    decisive regression this class pins down.  None of this is L3 evidence.
    """

    def test_decisive_light_cell_marked_dark_is_rejected(self):
        ok, reasons = f1.verify_scheme_consistency(2, True)
        self.assertFalse(ok)
        self.assertTrue(any("MISMATCH" in r for r in reasons), reasons)

    def test_measured_theme_matching_portal_and_pixels_passes(self):
        for scheme, dark, rgb in ((2, False, [252, 252, 253]),
                                  (1, True, [17, 17, 19])):
            with self.subTest(scheme=scheme, dark=dark, rgb=rgb):
                ok, reasons = f1.verify_scheme_consistency(scheme, dark, rgb)
                self.assertTrue(ok, reasons)

    def test_pixel_evidence_contradicting_the_theme_is_rejected(self):
        for scheme, dark, rgb in ((1, False, [252, 252, 253]),
                                  (2, False, [17, 17, 19]),
                                  (1, True, [252, 252, 253])):
            with self.subTest(scheme=scheme, dark=dark, rgb=rgb):
                ok, reasons = f1.verify_scheme_consistency(scheme, dark, rgb)
                self.assertFalse(ok)
                self.assertTrue(any("MISMATCH" in r for r in reasons), reasons)

    def test_invalid_inputs_fail_without_raising(self):
        for args in ((3, True), (None, True), ("1", True), (2, "true"),
                     (2, None), (2, 1), (2, False, [1, 2]),
                     (2, False, [1, 2, "a"])):
            with self.subTest(args=args):
                ok, reasons = f1.verify_scheme_consistency(*args)
                self.assertFalse(ok)
                self.assertTrue(reasons)

    def test_background_rgb_none_is_legal(self):
        for scheme, dark in ((2, False), (1, True)):
            with self.subTest(scheme=scheme, dark=dark):
                ok, reasons = f1.verify_scheme_consistency(scheme, dark, None)
                self.assertTrue(ok, reasons)

    def test_evidenced_cells_are_only_the_two_recorded_cases(self):
        self.assertEqual(set(f1.EVIDENCED_CELLS),
                         {(1440, 960, 1.0, 1), (900, 700, 2.0, 2)})
        self.assertNotIn((900, 700, 2.0, 1), f1.EVIDENCED_CELLS)


class SectionControlTests(unittest.TestCase):
    """Framework-level control metadata, independent of OS AT strings."""

    def test_non_compact_uses_tablist_and_horizontal_arrows(self):
        control = f1.section_control(False)
        self.assertEqual(control["framework_role"], "tabList")
        self.assertIn("Left/Right", control["key_sequence"])
        self.assertIn(("YAML", "tab"), control["aliases"])

    def test_compact_uses_popupbutton_and_trigger_keys(self):
        control = f1.section_control(True)
        self.assertEqual(control["framework_role"], "popUpButton")
        self.assertIn("Down/Up", control["key_sequence"])
        self.assertIn("Enter/Space", control["key_sequence"])
        self.assertIn(("Object sections", "popUpButton"), control["aliases"])

    def test_os_at_roles_m_w_unverified_and_source_is_automation_tree(self):
        for compact in (False, True):
            with self.subTest(compact=compact):
                control = f1.section_control(compact)
                self.assertEqual(control["role_source"], "automation_tree")
                self.assertIn("unverified", control["os_at_role"]["M"])
                self.assertIn("unverified", control["os_at_role"]["W"])

    def test_compact_formula_alias_is_the_same_object(self):
        self.assertIs(f1.is_compact, f1.compact_for)


class DeviationRegisterTests(unittest.TestCase):
    """A/B are adjudicated separately and are mandatory section-7 attachments."""

    def setUp(self):
        self.rows = f1.deviation_register()
        self.by_id = {row["id"]: row for row in self.rows}

    def test_exactly_two_deviations_with_distinct_scopes(self):
        self.assertEqual(len(self.rows), 2)
        self.assertEqual(set(self.by_id),
                         {"deviation_tabpanel_role",
                          "deviation_compact_role_downgrade"})
        self.assertEqual(len({row["scope"] for row in self.rows}), 2)

    def test_introduced_by_and_status(self):
        a = self.by_id["deviation_tabpanel_role"]
        b = self.by_id["deviation_compact_role_downgrade"]
        self.assertIsNone(a["introduced_by"])
        self.assertEqual(b["introduced_by"], "d345a4a")
        self.assertEqual(a["status"], "UNSATISFIED_FRAMEWORK")
        self.assertEqual(b["status"], "UNSATISFIED_PRODUCT")

    def test_both_are_advisory_and_await_distinct_principal_rulings(self):
        a = self.by_id["deviation_tabpanel_role"]
        b = self.by_id["deviation_compact_role_downgrade"]
        for row in self.rows:
            with self.subTest(id=row["id"]):
                self.assertEqual(row["platform_scope"], "L")
                self.assertEqual(row["m_w"], "unverified")
                self.assertIs(row["blocks_f1_release"], False)
                self.assertTrue(row["ruling_required"])
        self.assertNotEqual(a["ruling_required"], b["ruling_required"])

    def test_consequence_and_framework_evidence_are_recorded(self):
        a = self.by_id["deviation_tabpanel_role"]
        b = self.by_id["deviation_compact_role_downgrade"]
        self.assertIn("CANNOT be signed as L3 pass", b["consequence"])
        self.assertIn("RoleTabPanel", a["framework_evidence"])
        self.assertIn("0", a["framework_evidence"])


class MatrixPlanTests(unittest.TestCase):
    """The Linux-only plan is data; no cell is executed or signed off here."""

    def test_plan_has_88_linux_only_cells_with_scheme_and_signoff_slot(self):
        cells = f1.plan_cases()
        self.assertEqual(len(cells), 88)
        for cell in cells:
            with self.subTest(case=cell["case"]):
                self.assertIn(cell["scheme"], f1.SCHEMES)
                self.assertEqual(cell["platform"], "L")
                self.assertIsNone(cell["human_signoff"])
                self.assertIn("-scheme-", cell["case"])
                self.assertTrue(cell["screenshot"].endswith(
                    "-%s.png" % cell["section"]))
                self.assertEqual(
                    cell["compact"],
                    f1.compact_for(cell["width"], cell["height"], cell["scale"]))
        self.assertFalse(any(c["platform"] != "L" for c in cells))

    def test_900x480_tiling_is_registered_as_separate_design(self):
        rows = [c for c in f1.plan_cases()
                if (c["width"], c["height"]) == (900, 480)]
        self.assertTrue(rows)
        self.assertTrue(all("separate design" in c["note"] for c in rows))

    def test_f1_cells_cover_both_schemes_with_exactly_one_evidenced(self):
        cells = f1.f1_reproduction_cells()
        self.assertEqual(len(cells), 2)
        for cell in cells:
            self.assertEqual(
                (cell["width"], cell["height"], cell["scale"], cell["section"]),
                (900, 700, 2.0, f1.YAML_SECTION))
        self.assertEqual({c["scheme"] for c in cells}, {1, 2})
        evidenced = [c for c in cells
                     if (c["width"], c["height"], c["scale"], c["scheme"])
                     in f1.EVIDENCED_CELLS]
        self.assertEqual(len(evidenced), 1)
        self.assertEqual(evidenced[0]["scheme"], 2)
        self.assertNotIn((900, 700, 2.0, 1), f1.EVIDENCED_CELLS)


if __name__ == "__main__":
    unittest.main()
