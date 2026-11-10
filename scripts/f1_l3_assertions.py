"""F1 L3 acceptance assertions for the native object-sections replay.

The 2026-10-28 object-sections QA passed 12 geometry checks while the YAML
editor was only 80 DIP tall at 900x700 / 2x.  REPORT.md states the root cause
plainly: "The initial acceptance checked outer workspace geometry too weakly."
F1-L3-VERIFICATION-PROTOCOL.md section 3 therefore requires strengthening the
acceptance assertion *before* any re-verification, and requires the original
weak assertion and failure evidence to be retained rather than overwritten.

This module is deliberately free of gi/DBus/Hyprland imports so that the gate
can be exercised offline by the Python gate.  acceptance.py imports it for the
real-device replay; scripts/test_f1_l3_assertions.py feeds it the recorded
protocol.json bounds to prove the new gate catches the old defect.

Passing these assertions is L1/L2 evidence.  It cannot replace the L3 human
visual sign-off (protocol sections 1, 6 and 7); F1 stays [FAIL, UNFIXED].
"""

from __future__ import annotations

import math

MIN_READABLE_DOCUMENT_HEIGHT = 120.0
"""REPORT.md remediation threshold for field.detailText, in DIP."""

LEGACY_WORKSPACE_HEIGHT = 150.0
"""The original weak threshold on Object workspace.  Retained for comparison."""

DOCUMENT_KEY = "field.detailText"
DOCUMENT_LABEL = "Resource document"
YAML_SECTION = "YAML"
REQUIRED_STATES = frozenset({"multiline", "readOnly"})

# field.detailText is rendered only in detail_view.go's default branch, which
# corresponds to the YAML section.  Other sections render Resource events /
# Field ownership / Manifest editor instead, so a document gate applied there
# would fail for structural reasons rather than for a readability defect.
DOCUMENT_SECTIONS = frozenset({YAML_SECTION})

# acceptance.py:126-128 waits on these labels for every case.  After d345a4a
# the compact layout (which F1's fix activates at 900x700 / 2x) folds
# Close/Refresh into the Actions popover and replaces ui.Tabs with ui.Select,
# so four of those labels no longer exist in that case.  Both sets were
# measured with a headless label probe on d345a4a.
FULL_NAVIGATION_LABELS = (
    ("Actions", "menuButton"),
    ("Diagnostics", "menuButton"),
    ("Close detail", "button"),
    ("Refresh detail", "button"),
)
COMPACT_NAVIGATION_LABELS = (
    ("Actions", "menuButton"),
    ("Diagnostics", "menuButton"),
    ("Object sections", "select"),
    ("Read-only", "button"),
    ("Edit", "button"),
    ("Field ownership", "button"),
)
COMPACT_FOLDED_ACTIONS = ("Close detail", "Refresh detail")


class DocumentGeometryError(AssertionError):
    """Raised when the real document rect fails the F1 remediation gate."""


def _as_float(value):
    """Return a finite float, or None for missing / non-numeric / NaN / inf."""
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    result = float(value)
    if math.isnan(result) or math.isinf(result):
        return None
    return result


def verify_document_rect(bounds, *, case="", window=(0, 0),
                         section=YAML_SECTION,
                         min_height=MIN_READABLE_DOCUMENT_HEIGHT):
    """Pure gate: return ``(ok, reasons)``; never raises, never does I/O.

    ``bounds`` is the accessibility node's rect (x/y/width/height).  ``window``
    is the native *content* size in DIP, not screenshot pixels.  Cases outside
    the YAML section are skipped: the node does not exist there.
    """
    if section not in DOCUMENT_SECTIONS:
        return True, ["skipped: field.detailText is rendered only in YAML"]

    if not isinstance(bounds, dict):
        return False, ["bounds is not a dict"]
    win_w = _as_float(window[0]) if len(window) > 0 else None
    win_h = _as_float(window[1]) if len(window) > 1 else None
    if win_w is None or win_h is None or win_w <= 0 or win_h <= 0:
        return False, ["window size is missing or invalid"]

    values = {k: _as_float(bounds.get(k)) for k in ("x", "y", "width", "height")}
    reasons = ["bounds.%s is missing or not finite" % k
               for k, v in values.items() if v is None]
    threshold = _as_float(min_height)
    if threshold is None or threshold <= 0:
        reasons.append("min_height is invalid")
    if reasons:
        return False, reasons

    x, y, w, h = (values["x"], values["y"], values["width"], values["height"])
    checks = (
        ("width_positive", w > 0),
        ("height_readable", h >= threshold),
        ("inside_left", x >= 0),
        ("inside_top", y >= 0),
        ("inside_right", x + w <= win_w),
        ("inside_bottom", y + h <= win_h),
    )
    reasons = ["%s: x=%g y=%g w=%g h=%g window=%gx%g min_h=%g case=%s" %
               (name, x, y, w, h, win_w, win_h, threshold, case)
               for name, ok in checks if not ok]
    return not reasons, reasons


def evaluate_document(node, width, height, *, section=YAML_SECTION, case="",
                      min_height=MIN_READABLE_DOCUMENT_HEIGHT):
    """Return a structured verdict (dict) for protocol.json / checks.json."""
    bounds = node.get("bounds") if isinstance(node, dict) else None
    ok, reasons = verify_document_rect(bounds, case=case, window=(width, height),
                                       section=section, min_height=min_height)
    if section not in DOCUMENT_SECTIONS:
        return {"case": case, "section": section, "result": "skipped",
                "reason": reasons[0]}
    states = node.get("states") if isinstance(node, dict) else None
    role = node.get("role") if isinstance(node, dict) else None
    if role not in (None, "textField"):
        ok, reasons = False, reasons + ["role=%r is not textField" % (role,)]
    if states is not None and not REQUIRED_STATES.issubset(set(states)):
        ok, reasons = False, reasons + ["states missing multiline/readOnly"]
    verdict = {
        "case": case, "section": section, "key": DOCUMENT_KEY,
        "label": DOCUMENT_LABEL, "bounds": bounds,
        "window": {"width": width, "height": height},
        "min_height": min_height,
        "result": "PASS" if ok else "FAIL_VISUAL_USABILITY",
        "failed": reasons,
        "evidence_level": "L2_geometry",
    }
    return verdict


def assert_readable_document(node, width, height, *, section=YAML_SECTION,
                             case="", min_height=MIN_READABLE_DOCUMENT_HEIGHT):
    """Gate used by the real-device replay.  Returns the verdict on PASS."""
    verdict = evaluate_document(node, width, height, section=section, case=case,
                                min_height=min_height)
    if verdict["result"] == "FAIL_VISUAL_USABILITY":
        raise DocumentGeometryError("%s: Resource document unusable: %s | %s"
                                    % (case or section, "; ".join(verdict["failed"]),
                                       verdict["bounds"]))
    return verdict


def legacy_workspace_check(bounds):
    """The original >150px outer-workspace assertion, retained verbatim."""
    h = _as_float(bounds.get("height")) if isinstance(bounds, dict) else None
    return h is not None and h > LEGACY_WORKSPACE_HEIGHT


def compact_for(width, height, scale):
    """Exact mirror of detail_view.compactDetail() for replay label selection.

    ``height < min(600, 300*scale) || width < min(1100, 550*scale)``.
    """
    s = _as_float(scale)
    w = _as_float(width)
    h = _as_float(height)
    if s is None or w is None or h is None:
        return False
    return h < min(600.0, 300.0 * s) or w < min(1100.0, 550.0 * s)


def navigation_labels(section, *, compact):
    """Labels that must be reachable for one case under its chrome mode."""
    labels = list(COMPACT_NAVIGATION_LABELS if compact else FULL_NAVIGATION_LABELS)
    if not compact:
        labels.append((section, "tab"))
    else:
        labels.append((section, "text"))
        labels.append(("Resource document", "textField"))
        labels.append(("Object workspace", "group"))
        labels.append(("Connection status", "text"))
    return tuple(labels)


# Linux-only replay matrix, transcribed from protocol section 4 with M/W
# columns deferred by the 2026-11-10 user instruction.  Kept as data (not
# prose) so scripts/test_f1_l3_assertions.py can verify the F1 cell exists and
# that every cell carries a scheme and a human-sign-off slot.
SCHEMES = (1, 2)  # appearance(case, scheme==1): 1 = dark, 2 = light
SECTIONS = ("Overview", "YAML", "Related", "Events")

# The 2026-10-28 evidence covers exactly two cases, both in a single scheme:
# portal-calls.json has (1.0, 1) and (2.0, 2), and appearance-checks.json
# records 900x700-scale-2 as dark:false / [252,252,253] = LIGHT.  Protocol
# section 4 wrongly labelled the F1 cell "dark".  Every other cell, including
# 900x700 / 2x / dark, has NO pre-existing evidence and must not inherit the
# L2 FAIL marker.
EVIDENCED_CELLS = frozenset({
    (1440, 960, 1.0, 1),  # dark, H=604.5
    (900, 700, 2.0, 2),   # light, H=80 -- the F1 failure rect
})


def verify_scheme_consistency(portal_color_scheme, dark, background_rgb=None):
    """Gate the *measured* theme against the portal setting and pixels.

    ``dark`` must be measured, never derived from ``portal_color_scheme`` --
    deriving it is exactly how protocol section 4 came to label the light
    900x700-scale-2 evidence as "dark".  acceptance.py:79/81 uses
    ``dark => max(rgb) < 80`` and ``light => min(rgb) > 200``.
    Returns ``(ok, reasons)``; never raises.
    """
    reasons = []
    scheme = _as_float(portal_color_scheme)
    if scheme not in (1.0, 2.0):
        reasons.append("portal_color_scheme must be 1 (dark) or 2 (light)")
    if not isinstance(dark, bool):
        reasons.append("dark must be a measured bool, not a derived value")
    rgb = None
    if background_rgb is not None:
        if not isinstance(background_rgb, (list, tuple)) or len(background_rgb) < 3:
            reasons.append("background_rgb must be an RGB triplet")
        else:
            rgb = [_as_float(v) for v in background_rgb[:3]]
            if any(v is None for v in rgb):
                reasons.append("background_rgb has a non-numeric channel")
                rgb = None
    if not reasons:
        expected_dark = scheme == 1.0
        if dark != expected_dark:
            reasons.append("MISMATCH: portal_color_scheme=%g expects dark=%s but "
                           "measured dark=%s" % (scheme, expected_dark, dark))
        if rgb is not None:
            if dark and not max(rgb) < 80:
                reasons.append("MISMATCH: dark=True but max(rgb)=%g >= 80" % max(rgb))
            if not dark and not min(rgb) > 200:
                reasons.append("MISMATCH: dark=False but min(rgb)=%g <= 200" % min(rgb))
    return not reasons, reasons


def section_control(compact):
    """Framework-level control metadata.  OS AT strings are NOT L3 evidence.

    Linux/Windows map RolePopUpButton and RoleComboBox to the same OS string
    ("combo box" / UIA 50003), so an OS-level string cannot be back-translated
    into a framework role.  M/W remain [unverified, to be measured].
    """
    if compact:
        return {
            "framework_role": "popUpButton",
            "role_source": "automation_tree",
            "os_at_role": {"L": "combo box (ambiguous with comboBox)",
                           "M": "unverified: to be measured",
                           "W": "unverified: to be measured"},
            "aliases": (("object.section", "popUpButton"),
                        ("Object sections", "popUpButton")),
            "key_sequence": "Down/Up opens popup + moves highlight; Enter/Space commits",
            "value_source": "Select.accValue (base.go:287) + menu item.checked (base.go:404)",
        }
    return {
        "framework_role": "tabList",
        "role_source": "automation_tree",
        "os_at_role": {"L": "unverified: measured per cell",
                       "M": "unverified: to be measured",
                       "W": "unverified: to be measured"},
        "aliases": (("Object sections", "tabList"), ("YAML", "tab")),
        "key_sequence": "Left/Right chooses the next tab and moves focus (base.go:216-233)",
        "value_source": "tab.checked (base.go:234)",
    }


# Alias kept so the harness can import either name; both pin the same formula
# as detail_view.go:26-29 and must be versioned together with that source.
is_compact = compact_for


def deviation_register():
    """Two INDEPENDENT deviations, kept separate for separate adjudication.

    A is a framework/spec incompatibility predating d345a4a; it affects the
    non-compact ui.Tabs too.  B is a product regression introduced by compact.
    Both rows are mandatory attachments to the section 7 principal ruling:
    decoupling them from the per-cell automatic gate does NOT remove them from
    the F1 disposition record.  Neither may be written into an F1 release
    condition, and A must not be used to excuse B.
    """
    return (
        {
            "id": "deviation_tabpanel_role",
            "scope": "all_cases_including_non_compact",
            "introduced_by": None,
            "status": "UNSATISFIED_FRAMEWORK",
            "affected_clause": "protocol section 5.3 (tabpanel native role)",
            "framework_evidence": ("mygo v0.7.4 ui/access.go:25-90 Role enum has no "
                                   "RoleTabPanel; ui/base.go:188 emits RoleTabList and "
                                   ":199-232 emits RoleTab only; ui/ + platform/ full "
                                   "grep for tabpanel = 0 hits. The only tabpanel string "
                                   "in the module is a React ARIA example in "
                                   "website/src/routes/index.tsx:134."),
            "finding": "Non-compact ui.Tabs also lack a tabpanel role.",
            "consequence": ("A Web/ARIA term appears to have entered a frozen native "
                            "spec. Unimplementable at framework level; unrelated to F1. "
                            "Cross-reference F6 (screen reader / a11y tree unverified) so "
                            "the same ARIA wording is not silently closed there."),
            "substitute_contract": ("Record framework_role=tabList with four child "
                                    "role=tab nodes, checked state and arrow-key/focus "
                                    "follow evidence per cell; no tabpanel assertion."),
            "platform_scope": "L", "m_w": "unverified",
            "ruling_required": "principal (frozen spec, section 8)",
            "blocks_f1_release": False,
        },
        {
            "id": "deviation_compact_role_downgrade",
            "scope": "compact_cases",
            "introduced_by": "d345a4a",
            "status": "UNSATISFIED_PRODUCT",
            "affected_clause": "protocol section 5.3 + Phase 1 non-negotiable #5",
            "framework_evidence": ("detail_view.go:87/148 swaps ui.Tabs for ui.Select; "
                                   "ui/base.go:287 sets RolePopUpButton; :311-329 arrows "
                                   "open the popup / move highlight and Enter/Space "
                                   "commits; :381-383 option coreRows are not Focusable. "
                                   "detail_view.go:147 hides the identity breadcrumb."),
            "finding": ("Four section VALUES are retained (ui.Select still receives the "
                        "tabs slice), item.checked, accValue, Actions and status text "
                        "remain, but arrow-key direct switching, focus follow and "
                        "selected/focus/hover anchoring are degraded."),
            "consequence": ("Compact cells CANNOT be signed as L3 pass even if every "
                            "other gate is green and human sign-off is complete: #5 "
                            "must first be adjudicated. In compact the current-section "
                            "name is a single point of failure -- ellipsis truncation "
                            "at 2x removes both the text value and the checked spatial "
                            "anchor, so value==section must be paired with the "
                            "sign_section_name_persistent human row."),
            "substitute_contract": ("Record framework_role=popUpButton, accValue, "
                                    "menu item.checked, option reachability via the "
                                    "trigger highlight, and the measured key sequence. "
                                    "This is deviation EVIDENCE, not a pass gate; an "
                                    "alias table must never be treated as #5 satisfied."),
            "platform_scope": "L", "m_w": "unverified",
            "ruling_required": "principal (product design decision on #5)",
            "blocks_f1_release": False,
        },
    )
MATRIX_ROWS = (
    # (width, height, scale, note)
    (900, 700, 2.0, "F1 reproduction cell; must pass >=120 DIP"),
    (900, 700, 1.0, "minimum window at base scale"),
    (1100, 800, 1.0, "mid-size window"),
    (1100, 800, 2.0, "mid-size window at enlarged text"),
    (1440, 960, 1.0, "design baseline window"),
    (900, 480, 1.0, "tiled height below the 700 minimum; separate design item"),
    (900, 480, 2.0, "tiled height below the 700 minimum; separate design item"),
    (936, 1020, 1.0, "tiled width registered by host-unlocked-acceptance"),
    (936, 1020, 2.0, "tiled width registered by host-unlocked-acceptance"),
    (1250, 900, 1.25, "intermediate text scale 125%"),
    (1250, 900, 1.5, "intermediate text scale 150%"),
)


def plan_cases():
    """Expand the Linux-only matrix into individual verification cells.

    Each cell is a dict with an explicit ``scheme`` and an empty
    ``human_signoff`` slot.  The replay must emit one screenshot, one bounds
    record and one sign-off row per cell; none of them is executed here.
    """
    cells = []
    for width, height, scale, note in MATRIX_ROWS:
        for scheme in SCHEMES:
            for section in SECTIONS:
                cells.append({
                    "width": width, "height": height, "scale": scale,
                    "scheme": scheme, "section": section,
                    "case": "%dx%d-scale-%g-scheme-%d"
                            % (width, height, scale, scheme),
                    "screenshot": "%dx%d-scale-%g-scheme-%d-%s.png"
                                  % (width, height, scale, scheme, section),
                    "compact": compact_for(width, height, scale),
                    "platform": "L",
                    "human_signoff": None,
                    "note": note,
                })
    return cells


def f1_reproduction_cells(cells=None):
    """The mandatory gate cells: 900x700 / 2x / YAML, both schemes."""
    return [c for c in (cells if cells is not None else plan_cases())
            if (c["width"], c["height"], c["scale"], c["section"])
            == (900, 700, 2.0, YAML_SECTION)]
