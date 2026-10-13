# MyGo v0.4.8 compatibility review

Date: 2026-10-13. Scope: the existing Renovate group and PR #58, with #11 as
its dependency queue. Base main is `3660b1888f5d2a7cedd37d902b5506674de76358`
(tree `6359dd18721b850b74e5f6d54f7e981369d7b88a`). This is a dependency
qualification increment, **not** a migration to `ui/editor`, an MVP completion
claim, or a signed release. Enterprise authority remains deferred P3.

## Source and module identity

The official tags are v0.4.5 at `4d7114c4522498f991f7ebcba9edc9af0f820c6a`
and v0.4.8 at `c0e1068027faf8b14777c9cb925c937ef34ac2bb`.
Review inputs were retained by the existing offline-devkit workflow, before
terminal preparation, so its failed candidate did not discard the upstream
source needed to investigate the failure.

| Input | Identity |
| --- | --- |
| v0.4.5 source artifact | run 38185683519, artifact 11718889602; ZIP SHA-256 `6561fd86561e2fc59cc8d8468566536f2d56385d520974c678fc711fd945d940` |
| v0.4.8 source artifact | run 38367965162, artifact 11797463864; ZIP SHA-256 `2a960293bc7facb93ff175e1771ab4101e3ad40e3b6e5a960ada79c8419cd3f1` |
| v0.4.8 module | `h1:NpzFFiv8L4uXhkriknAehoucN//gtCtnGgr/DzQxZIw=` |
| v0.4.8 go.mod | `h1:+B7ZTBwVfa/nL/6+o6Vm8xBFYwJoFCg4VJbY9k6T0E8=` |

Both ZIP digests/CRCs and the v0.4.8 inner SHA256SUMS were checked. Independently
rehashing every extracted module file using Go's sorted-name h1 construction
reproduced the official module sums: **982 old files and 1,105 new files**.
The complete path/byte comparison contains **123 additions, 120 modifications,
and no deletions**. This accounts for the whole module, rather than treating
Renovate's two version substitutions as the compatibility diff. Reproducing a
module checksum is not an independent author signature or a security audit.
The workflow artifacts expire; the immutable tag/module identities above are
the durable reproduction inputs.

The upstream module adds three indirect audio decoder requirements. Aster does
not add audio, game, browser or new-editor imports. No Aster dependency other
than MyGo is deliberately upgraded; exact `go mod tidy` and clean module
metadata remain CI requirements, not something waived by the manual sums.

## App-facing compatibility boundaries

* `ui.TextBuffer` and indexed `TextAreaBuffer` now use `internal/rope`. The public
  rune-based replacement, UTF-16 bridges, snapshots, line indexing and native
  history remain Aster's surface. The new regression loads a document spanning
  4 KiB chunks with mixed CRLF/LF, selects/replaces supplementary-plane Unicode,
  loses/regains window focus, and verifies exact bytes, old snapshots, selection,
  buffer identity and two levels of native Undo/Redo. Existing Find, source
  navigation, oversize input, composition and stale-result tests stay mandatory.
* The focus repaint optimization records whether a frame reads window focus.
  Existing input controls still read it. No hidden retained handle, custom
  history stack or synthetic production insertion is introduced. Headless
  focus/input tests do not qualify physical keyboard or platform IME behavior.
* Upstream also changes popover/toast focus, parent drag handoff, text-layout
  caching, transforms, clipping and raster/GPU drawing. Existing dialog/toast,
  selection, row identity, minimum-window and actual-process window checks are
  retained. Changed D3D11/Metal shader arrays are not certified as reproducibly
  compiled merely because their source-side interfaces match; all three OS
  checked/production builds and rendering evidence are still required.
* Window-state storage becomes configurable upstream and Windows restore logic
  changes. Aster introduces no new persistence policy. WebView2 discovery, web
  touch input, audio/game helpers and example code are not new Aster features.
  They are not accepted as proof of desktop product behavior.

The separate [new-editor review](editor-upstream-mygo-0.4.8-review.md) remains in
force. `ui/editor` normalizes loaded mixed line endings. The unchanged external
`TextBuffer.Replace` widget synchronization still clears native history and
composition. A framework version bump does **not** resolve safe completion,
arbitrary giant non-clipboard input, history across unmount/remount, or IME.

## Native terminal: review before repinning

The initial bot head `36618cf6867cdc3885df07df135a423fa8ac60af` changed only the
two version pins and lacked updated sums. Its source/native preparation correctly
failed on `terminal.go`; it is nonqualifying evidence, not a reason to bypass
hashes. After the two reviewed source pins were updated locally, the strict
patch check also correctly rejected one outdated context line in `view.go`.

Only **two of the 28 pinned terminal sources** changed:

| File | Official Git blob at v0.4.8 | New SHA-256 |
| --- | --- | --- |
| `terminal.go` | `a1541fb0e116ddf9c47a4e5371844a4453553991` | `728a0a67a3da277f95fe8432cd94fcfe6e610796a0c2639cfdcde99cec0f3aac` |
| `view.go` | `0bc3b5ac7c1581b609d36040563bde87e0c04923` | `af6499573f7bfbd8aada8436f68153d7ff145c396c9307bc39f555bad9722f02` |

Both full-file Git blob identities were cross-checked against the official tag.
The added `Options.OnScroll` hook runs outside the terminal lock, only when
neither mouse tracking nor alternate-screen arrows own the wheel. Aster's
wrapper does not expose or set this hook. Mouse tracking keeps precedence over
alternate arrows; disabled alternate scroll remains local.

`hardening.patch` changes **one context line only**, from the old inline
alternate-screen condition to `case v.wheelArrows():`. Every added/deleted
hardening line is unchanged. Its new SHA-256 is
`76d2d7efb342c400f032aaee06a1f0fb2dfe9dd569c7ac6074534aee62e3af10`.
The strict `git apply --check`/apply path remains. No patch-fuzz option is added.
The other 26 source pins and **all six libghostty hashes/URLs** remain unchanged,
as does libghostty commit `befcdfd2c3a1cb24d9ec886e93c95b2b5daa7028`.

Remote-only connection construction, bounded input and resize, queue failure
teardown, no OSC52 clipboard access, no remote-link local launch, packaged-only
library lookup, mandatory native hash validation and restricted Windows DLL
search all remain in the patch. Native preparation successfully rebuilt the
28-source adaptation against the new module and verified the Linux library.
That local result does not certify the other five library binaries.

The new terminal regression drives the actual Aster surface through local
scrollback, SGR mouse reporting, alternate arrows, mouse precedence and disabled
alternate scrolling, then scrolls after close. A queued byte marker establishes
input ordering; it does not rely on sleeping to claim that no input occurred.
Existing Unicode, OSC52, remote-link, oversize-paste and queue-failure tests stay.

## Verification and retirement

Local development used Go 1.27.2 from the prior verified offline kit, the exact
current source tree, and an **uncommitted** vendor overlay of the checksum-checked
new module. It is not a substitute for a clean module-resolved CI checkout.
The complete local race run passed (1,707 PASS events, counting package-level
results and subtests). Both new regression top levels passed with race checking.
All 53 Python regressions passed on an unchanged rerun; the first invocation
retained one five-second Metrics installer fixture timeout while compilation
was running. No test, timeout or installer security guard was relaxed.

`run_migration_tests.py` now requires both new cases, preserving every previous
UI and terminal requirement, in checked and production builds on each OS. The
existing CI workflow remains unchanged. The version allowlist admits v0.4.8 for
its pinned codemod/no-op check; it does not admit an arbitrary future version.
The registered retirement scope is only PR #58 on the existing Renovate branch,
with the additional exact-main Renovate gate required for dependency scopes.

Before merging, independently check the final exact-head **CI, Source evidence,
Native migration verification, OIDC qualification and Renovate configuration**
runs and their original artifacts. After merging, qualify fresh exact-main
runs; do not substitute a PR run or the failed initial candidate. Observe the
real branch outcome and Dashboard lifecycle. Renovate may remove its own merged
branch first; an `already-absent` report is not an applied deletion or a recovery
bundle. Preserve merged ancestry and all historical recovery tags. Keep #13
open for actual remaining MVP and release work.

PR/main qualification and actual closure timestamps belong in #58/#13 after
they happen; this pre-merge source document does not assert those future results.
