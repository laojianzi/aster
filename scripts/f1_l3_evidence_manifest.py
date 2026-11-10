"""F1 L3 local evidence bundle manifest generator (offline, read-only).

WHY THIS EXISTS
---------------
The frozen delivery rules in artifacts/design-spec/03-UX-SPEC.md section 0.6
clause 2 and 01-VISUAL-DESIGN.md:105 say [forbidden] "deliverables are stored
locally only, never committed / pushed to Git".  .gitignore:15:/artifacts/
enforces this for the whole evidence tree.  As a result EVERY anchor that the
L3 protocol depends on -- the nine frozen specs, the L3 protocol itself,
acceptance.py, protocol.json, verification.json, the 900x700 screenshots,
source-hashes.json -- is tracked=false and cannot be recovered from version
control.

That is an evidence traceability gap, not something to "fix" by force-adding
artifacts/ to Git (which would violate the frozen rule).  The disposition is a
LOCAL, SELF-CONTAINED evidence bundle: a per-anchor SHA-256 manifest with an
explicit retrieval path and a tracked flag, plus a --verify mode that detects
drift.  The manifest is deliberately NOT committed either; it is a local
integrity record.  It is [ready-only] evidence, L1/L2 at most; it does not
perform or substitute for L3 real-device replay, and F1 stays
[FAIL, UNFIXED].

USAGE
-----
    python3 scripts/f1_l3_evidence_manifest.py               # write manifest
    python3 scripts/f1_l3_evidence_manifest.py --verify      # detect drift
    python3 scripts/f1_l3_evidence_manifest.py --verify --manifest PATH

Exit codes: 0 = ok, 1 = drift/mismatch, 2 = usage or I/O error.
No gi/DBus/Hyprland/Kubernetes imports.  Never runs the app.  Never writes to
Git.  Never modifies an anchor: the only file it writes is the manifest.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

MANIFEST_VERSION = 1
DEFAULT_MANIFEST = ("artifacts/implementation-readiness/"
                    "F1-L3-EVIDENCE-MANIFEST.json")

# Anchors are declared as data so the bundle is auditable.  "path" is relative
# to the repository root unless it starts with "$MOD".  "$MOD" expands to the
# mygo v0.7.4 module cache directory: those files live OUTSIDE the repository
# and are reproducible via go.mod:8 + go.sum:11-12 (`go mod download`), which
# is a strictly stronger retrieval path than an artifacts/ snapshot.
MOD = "github.com/egoist/mygo@v0.7.4"

# (group, path, retrieval, note)
ANCHORS = (
    # --- frozen design specs: the adjudication baseline -------------------
    ("frozen_specs", "artifacts/design-spec/00-TEAM-BRIEF.md",
     "local artifact only; NOT recoverable from version control",
     "team brief; U013 local-only decision"),
    ("frozen_specs", "artifacts/design-spec/00-SESSION-ANALYSIS.md",
     "local artifact only; NOT recoverable from version control",
     "session log"),
    ("frozen_specs", "artifacts/design-spec/01-VISUAL-DESIGN.md",
     "local artifact only; NOT recoverable from version control",
     "clause 2 forbids committing artifacts"),
    ("frozen_specs", "artifacts/design-spec/02-ARBITRATION.md",
     "local artifact only; NOT recoverable from version control",
     "section 11.4 A4 / section 12 final wording"),
    ("frozen_specs", "artifacts/design-spec/03-UX-SPEC.md",
     "local artifact only; NOT recoverable from version control",
     "section 0.6 clause 2 + section 5.4 F1"),
    ("frozen_specs", "artifacts/design-spec/04-FRONTEND-HANDOFF.md",
     "local artifact only; NOT recoverable from version control",
     "C9 contract"),
    ("frozen_specs", "artifacts/design-spec/99-CONSISTENCY-CHECK.md",
     "local artifact only; NOT recoverable from version control",
     "11/11 gate snapshot"),
    ("frozen_specs", "artifacts/design-spec/DESIGN-SPEC.md",
     "local artifact only; NOT recoverable from version control",
     "known failure item F1"),

    # --- implementation-readiness docs -----------------------------------
    ("readiness_docs", "artifacts/implementation-readiness/00-README.md",
     "local artifact only; NOT recoverable from version control", "status index"),
    ("readiness_docs", "artifacts/implementation-readiness/F1-L3-VERIFICATION-PROTOCOL.md",
     "local artifact only; NOT recoverable from version control",
     "sections 0-9: controlled session, matrix, judgement, evidence"),
    ("readiness_docs", "artifacts/implementation-readiness/F1-LOCAL-GEOMETRY-REMEDIATION.md",
     "local artifact only; NOT recoverable from version control",
     "d345a4a local geometry fix; section 9 outstanding items"),
    ("readiness_docs", "artifacts/implementation-readiness/F1-L3-HUMAN-SIGNOFF-SHEET.md",
     "local artifact only; NOT recoverable from version control",
     "per-cell human sign-off rows; all rows EMPTY this round"),

    # --- gate kernel: TRACKED, recoverable from version control -----------
    ("gate_kernel", "scripts/f1_l3_assertions.py",
     "local scripts/ file; once committed, git show HEAD:scripts/f1_l3_assertions.py",
     "pure-Python strengthened assertions; L1/L2 only"),
    ("gate_kernel", "scripts/test_f1_l3_assertions.py",
     "local scripts/ file; once committed, git show HEAD:scripts/test_f1_l3_assertions.py",
     "offline negative validation"),
    ("gate_kernel", "scripts/f1_l3_evidence_manifest.py",
     "local scripts/ file (this generator)",
     "recompute with --verify to detect evidence drift"),
    ("gate_kernel", "scripts/test_f1_l3_evidence_manifest.py",
     "local scripts/ file; once committed, git show HEAD:scripts/test_f1_l3_evidence_manifest.py",
     "8 offline tests, decoupled from the real artifacts/ tree"),
    ("gate_kernel", "go.mod", "git show HEAD:go.mod",
     "line 8 pins mygo v0.7.4"),
    ("gate_kernel", "go.sum", "git show HEAD:go.sum",
     "lines 11-12 pin the module checksums"),

    # --- original 2026-10-28 replay evidence: MUST stay byte-identical ----
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/acceptance.py",
     "local artifact only; NOT recoverable from version control",
     "ORIGINAL weak assertion at line 129 (workspace > 150px). "
     "Must remain byte-identical; a strengthened replay is a SEPARATE file."),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/run.sh",
     "local artifact only; NOT recoverable from version control",
     "controlled-session wrapper"),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/ACCEPTANCE.md",
     "local artifact only; NOT recoverable from version control",
     "original weak acceptance text"),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/REPORT.md",
     "local artifact only; NOT recoverable from version control",
     "states the <120px defect and requires strengthening the assertion"),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/protocol.json",
     "local artifact only; NOT recoverable from version control",
     "141 records; field.detailText H=604.5 / H=80"),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/verification.json",
     "local artifact only; NOT recoverable from version control",
     "FAIL_VISUAL_USABILITY; NO scheme/dark fields"),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/appearance-checks.json",
     "local artifact only; NOT recoverable from version control",
     "AUTHORITATIVE theme source: 900x700-scale-2 dark=false [252,252,253]"),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/portal-calls.json",
     "local artifact only; NOT recoverable from version control",
     "scheme tally {(1.0,1):7,(2.0,2):4}; 1=dark 2=light"),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/checks.json",
     "local artifact only; NOT recoverable from version control",
     "12 geometry/menu checks, all passed, missed the defect"),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/source-hashes.json",
     "local artifact only; NOT recoverable from version control",
     "476 Aster product-source hashes from 2026-10-30; 24 differ from d345a4a"),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/before.json",
     "local artifact only; NOT recoverable from version control",
     "fixture Pod state, cmp with after.json"),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/after.json",
     "local artifact only; NOT recoverable from version control",
     "fixture Pod state"),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/remaining-containers.txt",
     "local artifact only; NOT recoverable from version control",
     "must be empty"),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/acceptance.log",
     "local artifact only; NOT recoverable from version control", ""),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/app.log",
     "local artifact only; NOT recoverable from version control", ""),
    ("original_evidence",
     "artifacts/native-redesign/native-object-sections/cleanup.log",
     "local artifact only; NOT recoverable from version control", ""),

    # --- screenshots: old names lack the -scheme-<N>- segment -------------
    ("screenshots",
     "artifacts/native-redesign/native-object-sections/startup-dark.png",
     "local artifact only; NOT recoverable from version control", ""),
    ("screenshots",
     "artifacts/native-redesign/native-object-sections/1440x960-scale-1.png",
     "local artifact only; NOT recoverable from version control", ""),
    ("screenshots",
     "artifacts/native-redesign/native-object-sections/900x700-scale-2.png",
     "local artifact only; NOT recoverable from version control", ""),
    ("screenshots",
     "artifacts/native-redesign/native-object-sections/900x700-scale-2-YAML.png",
     "local artifact only; NOT recoverable from version control",
     "F1 failure screenshot; SHA-256 0f29b760...c11b"),
    ("screenshots",
     "artifacts/native-redesign/native-object-sections/900x700-scale-2-Overview.png",
     "local artifact only; NOT recoverable from version control", ""),
    ("screenshots",
     "artifacts/native-redesign/native-object-sections/900x700-scale-2-Related.png",
     "local artifact only; NOT recoverable from version control", ""),
    ("screenshots",
     "artifacts/native-redesign/native-object-sections/900x700-scale-2-Events.png",
     "local artifact only; NOT recoverable from version control", ""),
    ("screenshots",
     "artifacts/native-redesign/native-object-sections/1440x960-scale-1-YAML.png",
     "local artifact only; NOT recoverable from version control", ""),
    ("screenshots",
     "artifacts/native-redesign/native-object-sections/1440x960-scale-1-Overview.png",
     "local artifact only; NOT recoverable from version control", ""),
    ("screenshots",
     "artifacts/native-redesign/native-object-sections/1440x960-scale-1-Related.png",
     "local artifact only; NOT recoverable from version control", ""),
    ("screenshots",
     "artifacts/native-redesign/native-object-sections/1440x960-scale-1-Events.png",
     "local artifact only; NOT recoverable from version control", ""),

    # --- the archived binary: its hash proves it is NOT d345a4a -----------
    ("binaries",
     "artifacts/native-redesign/native-object-sections/aster-e2e",
     "local artifact only; NOT recoverable from version control",
     "built 2026-10-30 17:53:33 +0800; pre-fix source. "
     "A future replay MUST rebuild from a frozen HEAD."),
    ("binaries",
     "artifacts/native-redesign/native-object-sections/libghostty-vt.so",
     "local artifact only; NOT recoverable from version control", ""),

    # --- upstream snapshot helpers (parent dir, NOT upstream-focus/) ------
    ("upstream_helpers",
     "artifacts/native-redesign/mygo-linux-menu-audit/upstream-files.json",
     "local artifact only; NOT recoverable from version control", ""),
    ("upstream_helpers",
     "artifacts/native-redesign/mygo-linux-menu-audit/REVIEW.md",
     "local artifact only; NOT recoverable from version control", ""),
    ("upstream_helpers",
     "artifacts/native-redesign/mygo-linux-menu-audit/source-hashes.json",
     "local artifact only; NOT recoverable from version control",
     "512 entries; hashes the Aster PRODUCT source tree, NOT a mygo file list. "
     "This is a 2026-10-30 pre-fix baseline, not a v0.7.4 manifest."),
    ("upstream_helpers",
     "artifacts/native-redesign/mygo-linux-menu-audit/verification.json",
     "local artifact only; NOT recoverable from version control", ""),

    # --- framework role evidence: reproduced via the module cache ---------
    ("module_cache", "$MOD/ui/access.go",
     "go mod download github.com/egoist/mygo@v0.7.4 "
     "(pinned by go.mod:8, go.sum:11-12)",
     "Role const block lines 22-91: RolePopUpButton :43, RoleTabList :44, "
     "RoleTab :45, RoleComboBox :65; NO RoleTabPanel"),
    ("module_cache", "$MOD/ui/base.go",
     "go mod download github.com/egoist/mygo@v0.7.4 "
     "(pinned by go.mod:8, go.sum:11-12)",
     "coreTabsBase :188 emits RoleTabList; :199-232 emits RoleTab; "
     "coreSelectBase :287 emits RolePopUpButton"),
    ("module_cache", "$MOD/internal/platform/access.go",
     "go mod download github.com/egoist/mygo@v0.7.4 "
     "(pinned by go.mod:8, go.sum:11-12)",
     "OS role const block: RolePopUpButton :104, RoleTabList :105, "
     "RoleTab :106, RoleComboBox :123; NO RoleTabPanel"),
)


def _sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def _tracked_paths(root: Path) -> set[str]:
    try:
        out = subprocess.run(
            ["git", "ls-files", "-z"], cwd=root,
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            check=False, timeout=30,
        ).stdout
        return {p for p in out.decode("utf-8", "replace").split("\0") if p}
    except (OSError, subprocess.SubprocessError):
        return set()


def _expand(path: str, root: Path) -> tuple[Path, bool]:
    """Return (absolute path, is_outside_repo)."""
    if path.startswith("$MOD/"):
        base = os.environ.get("GOMODCACHE")
        if not base:
            try:
                base = subprocess.run(
                    ["go", "env", "GOMODCACHE"], cwd=root,
                    stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                    check=False, timeout=30,
                ).stdout.decode().strip()
            except (OSError, subprocess.SubprocessError):
                base = ""
        if not base:
            base = str(Path.home() / "go" / "pkg" / "mod")
        return Path(base) / MOD / path[len("$MOD/"):], True
    return root / path, False


def collect(root: Path) -> list[dict]:
    tracked = _tracked_paths(root)
    rows = []
    for group, path, retrieval, note in ANCHORS:
        absolute, outside = _expand(path, root)
        exists = absolute.is_file()
        row = {
            "group": group,
            "path": path,
            "source_tree": "module_cache" if outside else "repo",
            "exists": exists,
            "sha256": None,
            "bytes": None,
            "tracked": False if outside else path in tracked,
            "retrieval": retrieval,
            "note": note,
        }
        if exists:
            row["sha256"] = _sha256(absolute)
            row["bytes"] = absolute.stat().st_size
        rows.append(row)
    return sorted(rows, key=lambda r: r["path"])


def bundle_hash(rows: list[dict]) -> str:
    """Hash over (path, sha256) pairs; excludes metadata and generated_at."""
    h = hashlib.sha256()
    for row in rows:
        h.update(("%s\0%s\0" % (row["path"], row["sha256"] or "MISSING"))
                 .encode("utf-8"))
    return h.hexdigest()


def build(root: Path) -> dict:
    rows = collect(root)
    present = sum(1 for r in rows if r["exists"])
    return {
        "manifest_version": MANIFEST_VERSION,
        "generated_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
        "generator": "scripts/f1_l3_evidence_manifest.py",
        "root": str(root),
        "purpose": ("Local, self-contained evidence bundle for the F1 L3 "
                    "readiness package. NOT committed to Git: the frozen "
                    "delivery rule in 03-UX-SPEC.md section 0.6 clause 2 "
                    "forbids committing artifacts/."),
        "status": {
            "f1": "FAIL_UNFIXED",
            "evidence_level": "L1_L2_READINESS_ONLY",
            "l3_replay_performed": False,
            "human_signoff": None,
            "ci_dispatched": False,
            "m_w": "UNVERIFIED_DEFERRED_BY_USER_INSTRUCTION_NOT_PASSED",
            "product_source_changed": False,
            "frozen_baselines_reset": False,
        },
        "counts": {
            "anchors_total": len(rows),
            "anchors_present": present,
            "anchors_missing": len(rows) - present,
            "anchors_tracked": sum(1 for r in rows if r["tracked"]),
            "anchors_untracked": sum(1 for r in rows if not r["tracked"]),
        },
        "bundle_sha256": bundle_hash(rows),
        "anchors": rows,
        "caveats": (
            "1) tracked=false is a FACT, not a defect to silently repair: "
            "force-adding artifacts/ would violate the frozen rule. "
            "2) source-hashes.json in native-object-sections/ hashes the "
            "2026-10-30 pre-fix Aster source (476 entries, 24 differ from "
            "d345a4a); it does NOT cover these evidence files themselves. "
            "3) A future replay must freeze a HEAD, rebuild aster-e2e, "
            "regenerate source-hashes.json and ARCHIVE (not overwrite) this "
            "original evidence. "
            "4) Passing any automated gate remains L1/L2 and cannot upgrade "
            "F1 to L3 without per-cell human visual sign-off."
        ),
    }


def verify(root: Path, manifest_path: Path) -> int:
    if not manifest_path.is_file():
        print("VERIFY_FAIL: manifest not found: %s" % manifest_path)
        return 1
    try:
        saved = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        print("VERIFY_FAIL: cannot read manifest: %s" % exc)
        return 1
    old = {r["path"]: r for r in saved.get("anchors", [])}
    new_rows = collect(root)
    new = {r["path"]: r for r in new_rows}
    problems = []
    for path in sorted(set(old) | set(new)):
        if path not in old:
            problems.append("ADDED   %s" % path)
            continue
        if path not in new:
            problems.append("REMOVED %s" % path)
            continue
        for field in ("sha256", "bytes", "exists", "tracked", "source_tree"):
            if old[path].get(field) != new[path].get(field):
                problems.append("CHANGED %s %s: %r -> %r" %
                                (path, field, old[path].get(field),
                                 new[path].get(field)))
    old_bundle = saved.get("bundle_sha256")
    new_bundle = bundle_hash(new_rows)
    bundle_ok = old_bundle == new_bundle
    if not bundle_ok:
        problems.append("BUNDLE_HASH: %s -> %s" % (old_bundle, new_bundle))
    print("anchors=%d present=%d tracked=%d" % (
        len(new_rows), sum(1 for r in new_rows if r["exists"]),
        sum(1 for r in new_rows if r["tracked"])))
    print("bundle_sha256=%s" % new_bundle)
    if problems:
        print("VERIFY_FAIL: %d drift item(s)" % len(problems))
        for p in problems[:40]:
            print("  " + p)
        return 1
    print("VERIFY_OK: all anchors byte-identical, bundle hash matches")
    return 0


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--root", default=None, help="repository root")
    ap.add_argument("--manifest", default=None, help="manifest path")
    ap.add_argument("--verify", action="store_true",
                    help="recompute and compare against an existing manifest")
    args = ap.parse_args(argv)
    root = Path(args.root).resolve() if args.root else Path(__file__).resolve().parent.parent
    manifest = (Path(args.manifest).resolve() if args.manifest
                else root / DEFAULT_MANIFEST)
    if not root.is_dir():
        print("ERROR: root is not a directory: %s" % root)
        return 2
    if args.verify:
        return verify(root, manifest)
    doc = build(root)
    try:
        manifest.parent.mkdir(parents=True, exist_ok=True)
        manifest.write_text(json.dumps(doc, ensure_ascii=False, indent=2) + "\n",
                            encoding="utf-8")
    except OSError as exc:
        print("ERROR: cannot write manifest: %s" % exc)
        return 2
    c = doc["counts"]
    print("manifest=%s" % manifest)
    print("anchors=%d present=%d missing=%d tracked=%d untracked=%d" % (
        c["anchors_total"], c["anchors_present"], c["anchors_missing"],
        c["anchors_tracked"], c["anchors_untracked"]))
    print("bundle_sha256=%s" % doc["bundle_sha256"])
    return 0


if __name__ == "__main__":
    sys.exit(main())
