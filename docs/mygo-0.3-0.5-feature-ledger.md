# MyGo 0.3–0.5 feature decisions

Audit range: `v0.3.0..v0.5.1` (179 commits; 61 after v0.4.8).
[Compatibility contract and evidence](dependency-review-mygo-0.5.1.md).
Full path inventories and source checks are retained in the review receipt.
Each commit in [the complete mapping](mygo-0.3-0.5-commits.txt) has exactly one primary family; F65 additionally covers the multi-API settlement.
Decisions are not blanket acceptance of optional APIs or physical platforms.

| ID | Capability / change | Decision | Aster disposition |
|---|---|---|---|
| F01 | Checked Element / Handle / Services / construction-time keys | retain | Retain the prior 0.3 lifetime, identity and same-build polling migration. 0.3.1/2/3 release commits contain functional changes, not only version bumps. |
| F02 | Frame scheduling, close during frame and Context lifetime | inherit | Retain window teardown and one-shot callbacks; no Context captured by workers. Timer and close behavior still need runtime regressions. |
| F03 | Native ready/first frame and dev startup | inherit | Use actual native-window smoke; readiness is not application or cluster readiness. |
| F04 | Wayland activation and native resize | inherit | Retain Linux window smoke; hosted Xvfb does not certify physical Wayland compositors. |
| F05 | Tray menus and native menu-bar example | defer | No new tray background lifecycle; application close/cancellation must not silently become hide-to-tray. |
| F06 | CLI config loader and development progress | inherit | No runtime config-loader execution. Development tooling only; no Bun/Node frontend is added. |
| F07 | Windows vibrancy and example chrome | defer | Keep opaque workbench presentation; do not enable translucent effects merely because the toolkit supports them. |
| F08 | Antialiased text and exact stroke masks | inherit | Render changes require visual comparison; examples and upstream mask tests are not Aster acceptance. |
| F09 | Tooltip placement, labels, cursor and intrinsic column height | inherit | Retain minimum-window, focus and tooltips; layout bounds alone do not establish complete glyph readability. |
| F10 | Table column drag and horizontal list/header scrolling | defer | Current virtualized resource identity/order is retained; no new persisted column movement or scrolling model. |
| F11 | TextArea TrackLines | defer | Possible source gutter; must preserve raw byte/coordinate mapping, scroll ownership and bounded work. |
| F12 | Disk-image sizing, retries, detach and architecture filenames | defer | Relevant to later signed installers, not CI developer binaries; release globs must use actual platform names. |
| F13 | TextRange styling during composition | inherit | Retain synthetic composition/history tests; physical IME remains separate. |
| F14 | No unnecessary TypeScript client; web-view documentation | not-used | Aster has no bound frontend services or generated TS client; do not add a web surface. |
| F15 | Appearance-specific traffic lights and macOS full-screen placement | inherit | No multi-space launcher policy is enabled. Keep platform-window qualification separate. |
| F16 | Finder file-reference URLs / clipboard files | not-used | No implicit dropped-file ingestion; file targets remain explicitly supplied and validated. |
| F17 | Bitmap.Update and inset shadows | defer | No product demand for mutable bitmap or inset effects; preserve opaque/readable controls. |
| F18 | Terminal links, paste/key hooks and wheel interception | adapt | Port hardened adapter, retain explicit text paste/input budgets, block local link launching, and keep remote input ordering. |
| F19 | TextInput OnPaste | retain | Keep existing risky-clipboard preflight; it does not bound arbitrary direct typing or IME segments. |
| F20 | Web pages in native views / HTML reload / page background | not-used | No WebView or page bridge added; do not bring browser storage/auth into native workbench. |
| F21 | Popup highlight scrolling, combobox replacement and focus restore | inherit | Retain supported Select interactions and focused control identity; verify busy popup selection explicitly. |
| F22 | AlertDialog Escape and overlay lifetime | retain | Escape dismisses, not approval. Existing Alert/Toast migration regressions remain mandatory. |
| F23 | Rounded table rows and Spinner TextColor | retain | Retain explicit colors and prior Spinner regression; do not introduce custom sort semantics. |
| F24 | Linux frame Bounds / ContentBounds and flyouts | not-used | Aster does not use these changed geometry/flyout APIs. Outer-window bounds must not substitute for content-space tests. |
| F25 | Browser native UI, WebGL, web build and mobile keyboards | not-used | Desktop-only product; do not claim browser/iOS support from native CI. |
| F26 | Element arena allocation and physical-key shortcut matching | retain | Keep checked/production lifetime gates and physical-key regression, including non-Latin keyboard layouts. |
| F27 | Transforms, springs, keyframes, draggable and offscreen canvases | defer | No decorative motion/editor drag transaction added. Any later adoption must honor reduced motion and input identity. |
| F28 | Native charts | defer | Candidate for P2 monitoring with retained sample budgets; dependency upgrade does not implement monitoring history. |
| F29 | macOS presentation, selection drawing and callbacks | inherit | Retain Unicode selection, callback order and exact raw bytes; compare actual frames rather than performance claims. |
| F30 | Renderer masks/cache/focus redraw and Windows restore | inherit | Preserve bounded application work and window geometry; inherited optimization is not a measured Aster speedup. |
| F31 | macOS icon assets and small-icon fix | defer | Handle in signed installer/icon qualification; no release is created by this upgrade. |
| F32 | Custom window-state storage | defer | No new persistence of workspace identities, paths or geometry; choose a privacy policy before enabling persistence. |
| F33 | Diff-scene wheel and WebView2 availability checks | not-used | Example diff and browser runtime changes do not replace existing bounded field diff or add a WebView requirement. |
| F34 | Audio, loop ranges, games, gamepads, image regions and pixel art | not-used | No Kubernetes product requirement; do not link or initialize audio/game/controller features. |
| F35 | Code editor / DiffView and transactions introduced in 0.4.8 | defer | Mixed CRLF/LF normalization is incompatible with raw manifests. Keep TextBuffer and one native history; editor adoption is a separate P1 contract. |
| F36 | Windows dark native menus | inherit | Inherited only where native platform menus are used; no new action routes or approvals. |
| F37 | Custom schemes readable by pages | not-used | No page/custom-scheme fetch surface; no credential exposure via a frontend bridge. |
| F38 | Terminal copy-on-select, keybind tables and secondary-click actions | adapt | Force no automatic copy; preserve remote Ctrl+C/V, explicitly unbind platform defaults that would intercept remote control bytes. |
| F39 | Whole stapled tickets in delta updates | defer | Future signed update/recovery qualification must exercise installed notarized bundles, not just build output. |
| F40 | Secrets API | defer | Keep existing OS-vault target key, expiry, confirmation, cancellation and migration policy; do not introduce a second credential namespace. |
| F41 | Diff active-side commands, cursor and hunks | defer | Not a drop-in SSA diff/review substitute; side, offset and inside-hunk semantics need explicit adapter tests. |
| F42 | FontFamilies values and enumeration | defer | Candidate for persistent font settings. Cache off the frame path; font availability and glyph coverage are separate. |
| F43 | Terminal zoom | inherit | Local font zoom only; do not turn it into remote bytes or unlimited terminal geometry. Existing resize/wheel bounds remain. |
| F44 | OSC 5522 / multi-format paste | reject | Do not offer formats or retain lazy file/image providers to remote programs. Project explicit bounded text only, even when mode 5522 is enabled. |
| F45 | Outline indentation | defer | Future resource topology may use it; zero uses default and negative indent is invalid. No current Outline adapter. |
| F46 | Highlighter edit notifications and LineBackgrounds | defer | Useful for future source diagnostics; require bounded incremental cost, stable line mapping and raw-buffer compatibility first. |
| F47 | Network connectivity/metered state | defer | Connectivity is not cluster authorization or liveness. Do not automatically connect, retry mutations or refresh credentials. |
| F48 | OnEnter prototype replaced by OnTextKey / callback-scoped TextKey | defer | Use only final tagged OnTextKey API for any future keyboard adapter. Do not retain TextKey or conflate SetText with a history-preserving transaction. |
| F49 | Theme accent, high contrast, transparency, text scale and motion | adapt | Honor text scale; wrap actions and retain readable workbench. Current synthetic 125% check is not all scale/platform/accessibility certification. |
| F50 | Modal editor input, CursorShape and NoInputMethod | defer | No Vim/normal mode or disabled IME in manifest input; do not import transient names or hide composition. |
| F51 | Long-text styling index | inherit | Inherited TextRanges optimization; arbitrary giant direct-input safety is still unresolved and must not be inferred from it. |
| F52 | Multi-column sort and HeaderMenu | defer | Keep existing stable resource sorting. Future adapter must synchronize Sort/SortBy, handle Shift/Cmd and col=-1, and distinguish loaded-page from global sort. |
| F53 | File watches, settled batches, root resolution and FSEvents | defer | No automatic kubeconfig/chart reload. Later explicit watcher must bind identity, cancel correctly, treat replacement and avoid automatic cluster writes. |
| F54 | Recent documents and jump-list tasks | reject | Do not publish cluster names, kubeconfig paths or manifests into OS recent-file history. Later explicit local-document scope needs privacy review. |
| F55 | Cookies / persistent WebView profiles | not-used | System-browser OIDC retained. No embedded cookie store, hidden WebView or browsing-data access. |
| F56 | Launcher panels, Wayland layer-shell, icons and app launching | not-used | No launcher/app enumeration or ambient local execution; native workbench windows remain scoped to explicit workspaces. |
| F57 | Document windows, autosave, recovery and external edits | reject | Do not autosave sensitive cluster data or reinterpret document save as an API mutation. Existing session-local drafts stay explicit. |
| F58 | Rich text, attributed document, clipboard formats and theme | not-used | Not appropriate for raw manifests/logs; no HTML/RTF clipboard, formatting normalization or second editor history. |
| F59 | Streaming Markdown | defer | Potential future read-only help, not raw YAML/log replacement. Require no remote images or unreviewed URL launching, plus bounded parsing. |
| F60 | ANGLE D3D11 fragment shader fix | inherit | Keep native Windows smoke; browser ANGLE support is not claimed by that test. |
| F61 | Timeline scheduling and reduced-motion seek behavior | defer | No new motion; future scheduling must use final placement names and test seek/pause separately from reduced auto-animation. |
| F62 | WebViewAvailable / pre-Run runtime probes | not-used | Native windows must not gain a WebView prerequisite; no new probe or hidden web-window path. |
| F63 | Synthetic italic for fonts without italic face | inherit | Native text rendering benefit; not a guarantee of CJK coverage, font fallback or physical accessibility. |
| F64 | ICU catalogs, locale/region, CLDR and linked translations | defer | No partial app translation or locale-setting persistence. Keep operational identifiers and protocol output invariant; optional localized presentation needs reviewed catalogs. |
| F65 | Final 0.5 API settlement and default changes | adapt | Review all subchanges, not only the release summary: terminal/data, text key, fonts, table, Outline, editor, timeline, watches, cookies, recent files, Theme/Network/Secrets and unsupported errors. Unreleased prototypes are not 0.4.8 APIs. |
| F66 | Unused-feature linking and footprint | inherit | Retain native-only graph and measure Aster binaries; upstream counter sizes are not Aster results. |
| F67 | Terminal clipboard read deny/allow/ask | reject | Force deny before provider evaluation. Do not treat a prompt or prior paste offer as permanent permission. |
| F68 | Constructor-time input and previous-frame editability | adapt | Commit candidates only while current model is editable; suppress Changed side effects and prove unlock does not replay input or restore consent. |
| F69 | GPU occlusion, split rings/shadows and triple Metal drawables | inherit | Preserve visual semantics; actual GPU/driver/DPI stress and latency measurements remain release work. |
| F70 | 0.5.1 native-only locale generation fix | inherit | Do not generate a frontend locale module for native-only apps; no frontend is introduced. |
| F71 | Website/docs/examples and upstream test-only maintenance | not-used | Not Aster runtime changes; retain provenance without counting upstream docs/examples/tests as delivered product features. |
| F72 | Version publication metadata | metadata | Release markers for the reviewed source range, not separate features or Aster release certificates. |

## Exact commit coverage

`mygo-0.3-0.5-commits.txt` records the full 40-hex upstream commit, family ID,
and `+` (after v0.4.8) or `=` (already in v0.4.8). It contains all 179 commits
from the verified Git ancestry, not a truncated GitHub compare file page.
Read a commit as `https://github.com/egoist/mygo/commit/<full-sha>` or use
`git show <full-sha>` in the upstream repository. The first three release-labelled
commits contain functional changes and belong to F01, not metadata-only F72.

The dispositions apply to reviewed source surfaces and current Aster policy.
`inherit` requires application regression and does not certify all upstream code;
`defer`, `reject` and `not-used` are explicit decisions, not implemented features.
A decision count is not an MVP-completion percentage. Final PR/main evidence and
physical-device qualifications remain distinct from this inventory.
