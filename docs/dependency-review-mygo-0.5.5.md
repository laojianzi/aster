# MyGo v0.5.1 to v0.5.5 compatibility and adoption review

Review scope: existing PR #75, branch `renovate/mygo-native-framework`.
Accepted predecessor is `1251507aa2e2504c28f8b95c3330e8f7c7cd46da` (PR #74).
This document does not declare the candidate merged or its later main qualified.
The historical [0.3–0.5.1 ledger](mygo-0.3-0.5-feature-ledger.md) remains intact;
its 72 families and 179 commits are not repeated or silently reclassified.

## Fixed sources, not a patch-version assumption

The original review-input artifact 12023023563 from run 38846154286 has SHA-256
`c77961b63d2401d0e07a83b11a6da628579c4695ab8057bfe7270824b446ca83`.
Five complete module ZIPs were CRC checked, their module/go.mod h1 values recomputed,
and every module file matched to the Git blob at its restored exact tag. The complete
upstream bundle passed `git fsck --full --strict`. Aster recovery is different evidence.

| Tag | Exact commit | Module files |
|---|---|---:|
| v0.5.1 | `408b4def1f3e6a8d3fac852396b4a46d82ba22a3` | 1392 |
| v0.5.2 | `45cdfb7ff3f32090cc8aa9920e59442361db1529` | 1653 |
| v0.5.3 | `27ef957fb463314cdada5dffc3b04e881398fdba` | 1657 |
| v0.5.4 | `f16dc81c06c5b3341f158cdfa23dc01c7433371a` | 1663 |
| v0.5.5 | `4d8673cd2658a2c6e86b26c44751e57bf8174f91` | 1693 |

The interval contains **56 commits and 527 changed paths**. The fixed
[complete commit mapping](mygo-0.5.1-0.5.5-commits.txt) maps every commit to one
of 34 decisions below. Chronological commit-list SHA-256: `169285f67a407f240017431b844f4e5cb71ab831ed98f7c260a1d86d7cea59aa`.
Each row covers the final tagged surface, not an unshipped intermediate prototype.
This is change accountability, not an assertion that all optional features are enabled.

The MyGo v0.5.5 module checksum is `h1:3pvlEwumsck/L99cbDpay/Eqcf51B7zhKNXekiodLOw=`.
Its go.mod checksum remains `h1:daHGetyAlBkHhIZOOIcAI7C82yLUY7HgeT2+Z6w2VPU=`.
No transitive dependency or toolchain upgrade is required by this interval; JOSE stays
v4.1.6. Of 31 terminal source pins, only paste.go, terminal.go and view.go change.
The Ghostty commit and all six library identities stay unchanged. The strict patch
is reapplied to verified sources rather than copying an unreviewed generated directory.

## Complete per-family dispositions

Source links are exact upstream commits in the mapping; issue numbers here identify
changes, not approval. `inherit` requires application gates, `adapt` requires the
stated correction, and `defer`/`not-used` do not count as implemented product features.

| ID | Change | Decision | Aster handling and acceptance |
|---|---|---|---|
| P01 | Website, launcher docs and shader-cost documentation | metadata | No Aster runtime change. Keep source identities separate from website deployment and example measurements. |
| P02 | Watch scan entries relative to their directory (#366) | not-used | Aster does not call WatchFiles. Do not add implicit chart/kubeconfig reload or equate a watch event with authorization. |
| P03 | Keyed ListState.KeepOffset (#367) | defer | Keep identity-aware resource selection and existing scroll policy; persistent sorting/offset behavior needs a product decision. |
| P04 | Go-syntax Kage-derived shader compiler and ui/shader (#371/#372) | defer | No shader import or dynamic source compilation is introduced. Resource contents must never become executable shader source. |
| P05 | Shader uint parsing and GLSL option corrections (#377) | not-used | Relevant to optional shader compilation only; no change to YAML/JSON numeric semantics. |
| P06 | File identity rename pairing, first FSEvent and HintsOnly (#370/#375) | not-used | No watch subscription, automatic refresh or persistence is enabled. Coarse hints are not authoritative file identity. |
| P07 | chart.MinMax and Painter.StrokePolyline (#363) | defer | Potential bounded monitoring work; bucketing must retain extrema/gaps/time semantics. No Aster performance claim from upstream benchmarks. |
| P08 | Element.TextAnchorCaret (#352) | defer | Useful future selection-direction API, not a raw-byte/history/IME transaction. Retain existing editor and exact source navigation. |
| P09 | Concurrent global shortcut registration (#348) | not-used | No global shortcuts are added. Workbench shortcut authorization stays window/model scoped. |
| P10 | Percentage maximum in flex main axis (#349) | inherit | Retain scaled workbench and navigation/footer regressions at 100/125/150 percent and LTR/RTL host; no text-scale clamp. |
| P11 | Live SetCopyOnSelect and Select All copying (#345) | adapt | The generated adapter forces CopyOnSelectNone even through the new setter; explicit Copy remains usable. Runtime setter/Select All regression is mandatory. |
| P12 | Image-only empty bracketed paste (#344) | adapt | Reject non-text offers without emitting even empty bracketed-paste bytes. No PNG/file provider evaluation, retained offer or later remote read authorization. |
| P13 | Windows app file read/rename retry | inherit | Inherit bounded framework retry behavior only. Do not add retries to Kubernetes mutations, credentials or reviewed-operation clocks. |
| P14 | App InfoPlist.strings preservation (#380) | not-used | No new language resources or packaging pipeline. Signed installation/localization remain independently qualified. |
| P15 | System double/triple click counting per element (#347) | inherit | Retain Unicode selection, raw buffer history, physical-shortcut and target-identity regressions. Tester time is explicit; physical platform clicks remain a separate limit. |
| P16 | Version/release metadata | metadata | Four release commits are inventoried, not counted as application features. Module graph and go.mod are unchanged across these five snapshots. |
| P17 | Direct3D background shader compile and disk cache (#381) | not-used | No ui/shader call path is introduced. Do not enable a shader cache for untrusted workload data; ordinary D3D build/smoke remains required. |
| P18 | Markdown links use Theme.Link (#382) | not-used | No Markdown renderer is enabled; logs/manifests remain literal and no new URL-opening route exists. |
| P19 | Shader fills for text, paths and icons (#383) | not-used | No shader-backed paint is requested. Existing glyph/font and renderer checks do not certify every GPU. |
| P20 | Element color/distortion/layer shader effects (#384) | not-used | No effect pipeline is adopted for workload content; no sensitive backdrop capture is requested. |
| P21 | Upstream event-based waits and GUI input completion hooks (#391/#393) | inherit | Preserve Aster deadlines and assertions. Event synchronization is not permission to remove network timeouts or to count headless input as physical IME. |
| P22 | Typed uniforms, Metal precompile and backdrop stack (#401) | not-used | All three subfeatures are covered: typed setter mismatch panics/Values precedence; asynchronous Metal readiness; backdrop sampling/blur on each renderer. No shader import, cache or sensitive backdrop use. |
| P23 | Document save-then-rename and Windows FILE_SHARE_DELETE (#395) | not-used | Documents/autosave/recovery/recent-files remain disabled. No new manifest or credential persistence. |
| P24 | Wind, black-hole and chat-glow examples | not-used | All example animation, scrolling and titlebar adjustments remain upstream examples, not new Aster assistant/network features. |
| P25 | Split pane List Changed delivery (#406) | inherit | Mandatory public-API regression verifies current selected index and exactly one Changed effect in both directions, without replay after resize. |
| P26 | Deterministic ui.Tester clock (#396) | adapt | Toast expiry advances UI time explicitly: absent before 300 ms and closed exactly once by the existing 2-second bound. Frame does not advance time; do not replace operation/credential clocks. |
| P27 | Single-fetch cross-process upstream test inputs (#392) | not-used | Keep Aster hash-pinned build-only preparation. No download path is added to product runtime or tests from workload data. |
| P28 | Windows clipboard contention retry and channel-test fix (#397) | inherit | Keep explicit clipboard commands, preflight budgets and terminal denial. Real OS clipboard/provider and native smoke gates remain required; no expansion of clipboard access. |
| P29 | Upstream work-count test metrics (#399) | metadata | Do not transfer upstream benchmark numbers into product claims, weaken Aster budgets or remove its timeouts. |
| P30 | Retry unclaimed keys against current-frame shortcuts (#398) | inherit | Mandatory registration/removal/exactly-once regression plus existing Escape-dialog and revoked-consent tests. No application retry or retained capability is introduced. |
| P31 | mygo vet includes cgo packages (#405) | inherit | Run the real pinned native lifetime analyzer in CI. Do not skip cgo packages or suppress diagnostics to obtain a green gate. |
| P32 | Split shrink behavior and percentage split (#403) | inherit | Shrink behavior has a mandatory two-direction regression: both panes reachable, stored size unchanged. SplitPercent itself is not adopted in the workbench. |
| P33 | Tester Advance/time jump and stopped-clock surface (#390) | adapt | Use final tagged Now/Advance semantics, including negative Advance clamping; no wall-clock sleeps for UI expiry. Do not infer a separate Stop method from release prose. |
| P34 | Conic gradients, stops and element blur (#407) | defer | Do not add visual effects solely for an upgrade. Shared renderers still require ordinary/production smoke; no physical GPU/backdrop/privacy certification is implied. |

## Breaking behavior and actual fixes

The public OnPaste transfer.Data break and previous-frame constructor-time input
contract were already adapted in #74. This interval must retain those current-model
checks, consumed consent, raw mixed CRLF/LF, single mounted history and stale identity
boundaries. It does not solve the new code editor's loaded newline normalization,
physical IME, cross-unmount history or arbitrary giant direct-input safety.

1. **Tester time is now deterministic.** Real sleeps and ordinary Frame calls cannot
   expire a Toast anymore. Only the UI fixture switches to explicit Advance; the
   original deadline and exactly-once assertion remain. Request/credential TTLs,
   Helm deadlines and real-cluster clocks are not made synthetic.
2. **Terminal selection can be changed live.** Constructor defaults alone are no
   longer a sufficient guard. The new adapter SetCopyOnSelect always sets None;
   pointer selection and Select All cannot silently write host clipboard data.
   Explicit local Copy is retained. Image-only paste is a no-op in all four tested
   combinations of modes 2004/5522, even when upstream would send an empty marker.
   No lazy image/file provider is evaluated or kept for a later OSC52/5522 query.
3. **Event ordering and split behavior changed.** A shortcut appearing before its
   first painted frame can consume the key; removal must revoke it without replay.
   Split List Changed observes the current selection once; shrinking does not
   discard a pane or change the remembered size. Existing window-scoped operation
   authorization and action/consent callbacks remain in force.

The final Now/Advance methods, rather than a guessed Stop API, are used. Exported
field additions can break unkeyed external struct literals; Aster uses keyed options
and must pass all builds and the pinned native lifetime analyzer. Unused shader
setter panics and uniform precedence are documented rather than exercised on secrets.

## Mandatory evidence and completion boundary

The following new names join BOTH checked and production migration inventories,
without dropping any predecessor name:

- TestNativeMyGo055ExplicitClockAndCurrentFrameShortcut
- TestNativeMyGo055SplitListReportsCurrentSelectionOnce
- TestTerminalNativeMyGo055RuntimeCopyAndImagePasteStayDenied

The prior Toast, Escape, 100/125/150 percent LTR/RTL-host, pending sensitive field,
renewal, Unicode/raw-history, clipboard and real-cluster cases remain required.
The patch-ledger Python regression validates the fixed 56-commit mapping, all 34
source-backed decisions, source test declarations, both-mode registration and
missing/failed/skipped/unfinished evidence rejection. It also couples this review,
module/native pins and PR #75's exact guarded cleanup registration.

Require five exact-final-head workflows: CI (all eight jobs), Source evidence,
Native migration verification, OIDC qualification and Renovate configuration.
Read original streams and ordinary/production binaries; Source module patch must
be empty. Review fresh native images rather than inheriting #74 screenshots.
After a merge, qualify actual main separately and record the observed branch and
Dashboard state. An already-absent branch is not an applied deletion or a bundle
recovery. Keep #11 open for any actual remaining update and #13 for remaining MVP.

CI headless rendering, software-rendered Linux and unavailable AT-SPI do not
certify physical keyboards/IME, assistive technology, all fonts/locales, Wayland,
GPU drivers, mixed DPI or signed installation. Deferred shader/chart/watch/editor
features require their own product/privacy budgets; no silent persistence or new
remote authority. P3 enterprise authority remains outside the first MVP.
