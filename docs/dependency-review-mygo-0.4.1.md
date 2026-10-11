# MyGo 0.3.7 to 0.4.1 review

Scope: existing Renovate PR #33 / `renovate/mygo-native-framework`, requested
through ongoing Dashboard #11 after #31 restored its framework visibility.
Do not create a second MyGo update branch. No web frontend is introduced.

## Applicable contracts

Upstream v0.4.0 changes AlertDialog Escape to AlertDismissed, architecture-bearing
installer names, and omitted TypeScript generation for apps with no bound services.
Aster uses none of these APIs or old installer globs today. Signed packaging is
still unqualified. Linux frame Bounds versus ContentBounds is not used by Aster;
actual native-window smoke and minimum-window controls remain required. The
0.2-to-0.3 constructor/lifetime migration gates are retained, including both build
modes and codemod no-op. The migration checker now explicitly permits reviewed
0.4.1 in addition to the historical 0.3.7 baseline, not arbitrary future versions.

## Terminal adaptation

Reviewed official module origin: `296ea8592eecf97e4118330dd39c6e356fedaf94`, tag
v0.4.1. Go module and checksum database verification remains enabled in CI. Of the
28 selected upstream files only links.go, terminal.go and view.go changed. The
native-library manifest and all library hashes are unchanged. The three source
hashes are updated only after comparing the actual official source. terminal.go
adds callback options; links.go extends file-path matching; view.go moves input
and link dispatch. Aster does not expose these callbacks to remote content.

The prior hardening patch correctly failed against moved hyperlink dispatch.
The adapted patch still rejects implicit local shells, uses bounded ordered
input, caps native geometry, prevents runtime native-code downloads, validates
native-library hashes, and disables OSC clipboard/notification side effects.
Both link hover resolution and activation are disabled before any local URL or
OpenLink callback sink. Local copy/paste remains an explicit user action with
its previous budget. Actual native-surface tests click plain URLs, OSC8 links
and paths, and exercise oversized native paste, rather than only testing direct
Terminal methods. No rejected patch hunk is ignored by production preparation.

## Verification and limits

Require CI, Native migration verification, OIDC qualification, Source evidence,
and Renovate configuration at the final head. Existing real OS stores, three
Kubernetes versions, independent Dex and two-control-plane tests are retained.
Record exact final results in #33/#13; this document is not a passing-test receipt.
The read-only offline workflow preserves public upstream review inputs before
strict preparation; a failed native gate remains a failure, not an accepted kit.
A green development matrix is not physical IME/GPU/accessibility, signed installer
or security-audit qualification. No runtime WebView/TS integration is added.
