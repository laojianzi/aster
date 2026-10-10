# MyGo 0.2 → 0.3 migration audit

Baseline: `cb6a73c84a708154fd3fd6ddf8ea0884a6c4b915`, MyGo `v0.3.7`.
Tracking: #21, one implementation branch `fix/mygo-03-widget-lifetimes`.

## Result, not a blanket guarantee

The earlier dependency upgrades (#16/#20) compiled, passed lifetime vet and a
substantial regression matrix. That did **not** establish complete behavioral
migration. This audit reproduced an omitted case: if resource A is replaced with
B before another frame, Undo in B's Manifest editor restored A's document.
`TestNativeEditorUndoCannotCrossResourceIdentity` failed on the exact baseline
with `Undo crossed resource identity: "original-resource-a"`.

The fix keys the detail subtree by connection/detail epochs and keys every
stateful application control before construction. Text fields for different tabs
no longer inherit each other's UI identity. An inserted status row does not reset
the current document's focus or Undo history. This is UI state isolation, not a
claim that old strings have been securely erased from process memory.

Structural click actions now run through `OnClick` after construction. Workers
still publish immutable results through the window dispatch boundary. Inline
`Changed()` polling remains where it commits model edits and invalidates previews
before actions; it is supported again in MyGo 0.3.2+, unlike original 0.3.0 timing.
This audit does not claim that the history defect originated in MyGo itself.

## Breaking-change checklist

| Upstream contract | Aster check / handling |
| --- | --- |
| Constructors/custom parts return checked values; absent value uses `Valid` | No retained application `*ui.Element` or public parts pointers; upstream terminal adapter is generated from the jointly pinned 0.3.7 source |
| Build values expire on the next build, including same-frame rebuilds | Pinned `mygo vet`; no Context/Element persisted in application state or sent to workers; production-tag regression and live-window smoke supplement checked Tester runs |
| Persistent control identity uses `Handle` | List states retain their supported handles; terminal adapter uses persistent `Services`, not stored build Context; each workspace owns separate widget state |
| Stateful keys must exist at construction | `Context.Key` on every application TextInput/TextArea/Select/Checkbox/List/Table; source-AST regression prevents regressions; detail epoch isolates resource/session changes |
| Input settings precede response polling | ReadOnly/Disabled applied before `Changed`; readonly/disabled/Undo tests; action handlers independently revalidate identity and confirmation |
| Deferred structural actions | Application buttons use `OnClick`; test asserts New workspace runs outside construction exactly once; row reordering cannot retarget a pending click |
| Same-build polling in 0.3.2+, click input in 0.3.3+ | Existing edit/apply/vault/command flows retained; do not apply original 0.3.0's next-build assumptions to 0.3.7 |
| Source codemod and `Rows`/`GridRows` changes | `check_mygo_migration.py` runs the exact pinned codemod and requires no changed Go files, including ignored generated adapter; no application Grid migration needed |
| CLI/module consistency | CLI version is derived from go.mod; native source version and file/patch/library hashes are verified together |
| Web runtime/TS plugin migration | Not applicable: Aster's product UI is native Go; no embedded web frontend or TS runtime |
| cgo behavior | Normal desktop uses Go/purego; CI builds actual OS targets and packages the fixed Ghostty library; no claim that no cgo means no native dependency |

Official references, accessed 2026-10-10:
- https://github.com/egoist/mygo/releases/tag/v0.3.0
- https://github.com/egoist/mygo/blob/v0.3.7/docs/ui/migration.md
- https://github.com/egoist/mygo/releases/tag/v0.3.7

## New mandatory migration regressions

- Cross-resource Manifest and command history isolation.
- Cross-tab editable field separation; read-only and disabled editing rejection.
- Replaced resource drops old focus/composition.
- Pending pointer press survives row reorder without acting on another resource.
- Structural actions execute after construction, once only.
- Inserting a status message preserves the same document's focus and history.
- All stateful UI constructors have construction-time keys.

Run these in the ordinary and `mygo_noinspector` configurations on all three OS
runners. The Tester keeps generation checks enabled even with production tags;
therefore also build and smoke-test actual production-tag desktop binaries. The
full existing three-Kubernetes, two-control-plane, real OS-vault and terminal
provenance matrix remains mandatory. Do not reuse pre-fix green evidence.

## Remaining qualification

No finite suite establishes "perfect" behavior on every host. Physical IME,
assistive technology, GPU/mixed DPI, signed installer/Keychain upgrade, soak and
fault injection are still release gates in #13. Tests with `Compose` are synthetic
IME event tests, not qualification of the real OS input-method candidate window.
