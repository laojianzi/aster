# MyGo v0.5.1 compatibility and adoption contract

2026-10-15. Continue the existing PR #74 and `renovate/mygo-native-framework`.
Accepted base: `57e55aac00aa9b196a2828502dd386c83dbe4a84` (JOSE v4.1.6).
This is a pre-merge source review, not a completed MVP, release or physical-device
certificate. The [72 change-family decisions](mygo-0.3-0.5-feature-ledger.md)
and [179 exact commits](mygo-0.3-0.5-commits.txt) cover `v0.3.0..v0.5.1`,
including the 61 commits after v0.4.8. Counts describe coverage, not independent
features or a completion percentage. Earlier version reviews remain historical.

## Reproducible source boundary

Six original module ZIPs were rehashed using Go's sorted-name h1 construction,
including their go.mod sums. Every ZIP member's Git blob identity was compared
with the corresponding exact tag in the complete, independently restored
upstream Git bundle. `git fsck --full --strict` succeeded. This verifies bytes
and ancestry, not author identity, exhaustive security or untested platforms.

| Version | Exact Git tag commit | Module files |
|---|---|---|
| v0.3.0 | c9c1bc6c73d7c57a163c31d73230f845a9853ce3 | 841 |
| v0.3.7 | d7f292d6d972eb0718d4012d23527df009e6b349 | 869 |
| v0.4.0 | 70523fff420e3116c7f4380e0385a8ac76d76495 | 884 |
| v0.4.8 | c0e1068027faf8b14777c9cb925c937ef34ac2bb | 1105 |
| v0.5.0 | 208e952608a63f0d3d9cfa8c4a069b7e3a83a943 | 1392 |
| v0.5.1 | 408b4def1f3e6a8d3fac852396b4a46d82ba22a3 | 1392 |

v0.5.1 module: `h1:Izr8oG9u11942EMfoVUzE5YjlnDeyELiqb/KjkBkj2U=`.
Its go.mod: `h1:daHGetyAlBkHhIZOOIcAI7C82yLUY7HgeT2+Z6w2VPU=`.
Original history artifact: run 38696855478, artifact 11954505947,
SHA-256 `1d51c3fea4eca8bb2c5007270a4551e0cb8ac2553ec2005d28324ad4b834af6d`.
Its collection job succeeded although the old candidate's other devkit job failed;
that run is review input, not application qualification. Artifacts expire; fixed
public tags and module sums are durable reproduction inputs.

The required graph changes are MyGo v0.5.1, x/net v0.59.0, x/term v0.46.0 and
x/text v0.43.0. JOSE v4.1.6, the Go 1.27.2 toolchain, Kubernetes pins and all six
Ghostty library hashes remain unchanged. Online clean-checkout module verification
and a zero tidy diff remain CI requirements; the offline vendor overlay is not
committed and does not replace them.

## Breaking behavior and required adaptations

### 1. Constructor-time input and previous-frame editability

In 0.5 the bound value is updated before a control constructor returns, including
local variables. Input observes the previous-frame settings the user saw. Merely
calling Disabled/ReadOnly in the new build cannot revoke already queued input.
This is a real behavior change despite largely unchanged public signatures.

`internal/ui/input_commit.go` binds small fields to candidates and commits them
only while the CURRENT model permits editing. Callers guard Changed side effects
with the same condition. Command/TTY/forward, Helm paths and parameters, vault,
OIDC and delivery confirmation fields use it. Stable constructor keys are retained.
Buffer-backed manifests retain revision/identity-bound stale-plan invalidation;
they are not copied into small-field helpers or given a second history stack.

The migration gate requires queued-field, consumed-consent, choice/popup,
sensitive-field and renewal-opt-in regressions. Tests lock without rendering,
submit queued input, require no target/trust/epoch changes, unlock without replay,
and exercise fresh permitted input. A temporary negative-control removal of the
candidate commit guard made the selection and eight sensitive-field cases fail;
the original guard was restored. That failure is a test-sensitivity experiment,
not passing product evidence. Synthetic controls do not qualify physical IME.

### 2. Terminal OnPaste and richer data/permission defaults

`Options.OnPaste` now accepts `transfer.Data`, not a string. Upstream also adds
clipboard read deny/allow/ask, OSC 5522, lazy image/file representations, keybind
tables, copy-on-select and secondary-click behavior. Aster does not expose these
new channels merely because the dependency gained them.

The reviewed adaptation pins all 31 selected upstream source files, ports the
bounded input queue/close behavior, removes an inaccessible internal/l10n import,
and forces no clipboard writes, no remote clipboard reads, no copy-on-select and
no automatic local URL launch. Explicit user paste is projected to bounded text,
never an entire retained format offer. Windows/Linux Ctrl+C and Ctrl+V still go
to the remote process; explicit copy/paste bindings remain platform-specific.
Local font zoom is inherited without exposing a new host callback; existing
geometry/input limits and resize routing remain mandatory.

Actual native-parser tests send OSC52 and OSC5522 reads and assert no provider
access, then exercise mode5522/bracketed multi-format paste without evaluating
image/file providers. Existing oversize-paste, Unicode, link, wheel, close and
queue-limit tests remain. No user cluster or external provider is attacked.
`hardening.patch` SHA-256 is
`f685f9ab7c2a675640d6b007577aa35c41274711bdc9427eb7513d1e0fd58f02`.
Strict source hash checking and `git apply --check` stay in place; no fuzz bypass.

### 3. Locale, direction, sizing and renderer changes

The untranslated English workbench explicitly uses LTR container ordering, as do
terminal/protocol grids; this is not a claim of an Arabic/Hebrew translation.
Text scale is honored, not clamped to hide clipping. Actions wrap, formerly rigid
rows use minimum heights, and details use available space. The mandatory layout
case uses a 1100x700 window with 125% text scale, high contrast and reduced motion,
under LTR and RTL-host settings. Control bounds and two retained full-frame
screenshots show the repaired actions, not all possible glyphs or scale factors.
A subsequent Windows CI image exposed a separate navigation/footer overlap that
window-only action bounds missed. The body now uses the available height, the
sidebar is an independently keyed scroll viewport, and the status row cannot
shrink below its minimum. A mandatory 100/125/150% LTR/RTL-host regression checks
footer separation, unclipped last-item reachability by wheel, actual activation,
and keyboard Home returning to the first item. The old code failed the 150%
case locally. This is not a claim that every scale, translation or font fits
without ellipsis; narrow presentation labels may still be shortened.

Root layout direction must not normalize manifest contents. Existing Unicode,
raw mixed-CRLF/LF, Find, selection, native history, focus, dialog and checked versus
production lifetime cases stay mandatory. CJK fonts, real screen readers, physical
IME, Wayland compositors, mixed DPI and GPU drivers remain release qualifications.
Inherited Metal/D3D/GL optimizations are not measured Aster performance claims.

### 4. Stable tagged API versus unreleased prototypes

The public-source AST comparison from v0.4.8 to v0.5.1 is a declaration inventory,
not a type-checker or behavioral proof. Eight disappearing method entries belong
to a private windowControls implementation; they are not eight removed public
application APIs. Added exported struct fields can affect external unkeyed
literals even when keyed Aster call sites compile. Source files moving does not
mean their public method disappeared.

The final API-settlement commit
[f2eca9f](https://github.com/egoist/mygo/commit/f2eca9f74533b4031b13d9b8d4db89268f5bc0da) changes numerous prototypes
introduced AFTER v0.4.8. These are not additional breaks from stable v0.4.8, but
adoption decisions must use the final v0.5.1 contracts rather than interim examples:

| Surface | Final contract and Aster decision |
|---|---|
| Text keys | OnTextKey/TextKey replaces the OnEnter prototype. Callback-scoped access must not escape; SetText is not a history-preserving document transaction. No new shortcut adapter. |
| Timeline | Tween/Settled and AfterPrevious/WithPrevious/AtLabel/AtTime replace interim names. Explicit seek/pause and reduced automatic motion differ; invalid duplicate/conflicting scheduling may panic. No new animation dependency. |
| Fonts | FontFamilies returns FontFamily values, including Monospaced/Registered, rather than an interim filtered string list. Do not enumerate during every frame or claim coverage from a family name. |
| Tables | Sort and SortBy must stay synchronized; Shift/Cmd can extend multi-sort. HeaderMenu must handle column -1. Keep existing resource ordering until a bounded, identity-safe adapter is specified. |
| Outline | Zero Indent means default; negative is invalid. Future topology is independent P2 work. |
| Editor and diff | CursorShape, modal OnInput, NoInputMethod, active-side cursor and inside-hunk results have explicit semantics. NoInputMethod disables native IME; do not enable it to hide composition problems. No direct manifest editor replacement. |
| Clipboard services | ReadClipboardData/ClipboardFormats replace interim image-only helpers. Clipboard snapshots may hold lazy representations; terminal policy remains text-only/deny remote reads. |
| Watches | Settled change batches, root resolution and Close/EOF/cancellation conventions replace provisional buffering/overflow behavior. No automatic kubeconfig or chart reload. |
| Cookies | Filtered listing, deletion and profile persistence concern embedded webviews. Aster keeps system-browser OIDC and does not introduce browser storage. |
| Recent documents | OS history and jump-list task argument conventions require privacy and quoting review. Do not publish kubeconfig, cluster names or raw manifests. |
| Theme and native services | Use final Preferences/Network status and scoped Secrets interfaces only after an explicit adapter review. No duplicate vault namespace or connectivity-triggered mutation/renewal. |
| Unsupported platforms | Match the final unsupported-error contract rather than provisional sentinels; unused backends are not accepted product targets. |

The fixed full commit mapping, not the abbreviated label above, is authoritative
for checkout. Historical 0.3/0.4 changes (checked elements/handles/services, keys,
Alert Escape, Toast lifetime, Spinner color, physical shortcuts and TextBuffer)
remain covered by the older mandatory migration tests. Release 0.3.1/2/3 commits
also contain functional changes and were not classified as version-only markers.

## Optional features are decisions, not compulsory adoption

ICU catalogs, CLDR formats, linked translations and RTL translation are deferred
until reviewed messages, invariant operational identifiers and persistence policy
exist. Aster does not implicitly add Documents/autosave/recovery/recent files,
a second Secrets store, Cookies, launchers, app enumeration, audio or game APIs.
Charts/topology, font settings, multi-column tables and bounded read-only Markdown
may support separate P2 goals. Rich text is not a raw YAML/log representation.

The newer editor still normalizes loaded mixed CRLF/LF; its new completion,
modal editing and line-highlight APIs therefore do not satisfy Aster's combined
raw-byte, single-history, selection, identity, IME and bounded-input contract.
Retaining the existing raw buffer is an intentional decision, not completion of
the advanced editor goal. Theme responsiveness and GPU inheritance likewise do
not certify arbitrary giant direct input. Signed update/notarization and icon
changes belong to actual installer/recovery tests, not CI developer binaries.

## Qualification and truthful handoff

Predecessor `a7281a0069c68c102a6532feb4a918a386a2d9f3` has five successful PR gates.
Its fresh source ZIP 11978805958 was size/digest/CRC-checked and reconstructed tree
`5084ba7cabd7e387c56c76a5c8e252059438119d`; resolved modules match committed files
and its module-metadata patch is empty. This does not qualify later commits.
New regression and documentation changes require their OWN exact-head five gates,
raw mandatory-test evidence, multi-platform builds and actual-cluster results.
Local interrupted test processes are not passing suites; per-suite outcomes must
be recorded separately. No test, permission, hash, timeout or gate is relaxed.

After a reviewed merge, verify fresh actual-main results and the real branch and
Dashboard outcome. An already-absent bot branch is not an applied deletion or a
before.bundle recovery. Do not reset the manually edited Renovate branch, create
a duplicate PR, or close #11/#13 on a version substitution or green badge alone.
P3 enterprise authority stays deferred and excluded from the first MVP.
