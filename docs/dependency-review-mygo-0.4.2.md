# MyGo 0.3 to 0.4 audit and 0.4.2 qualification

Scope: Dependency Dashboard #11 / PR #39, the existing grouped branch
`renovate/mygo-native-framework`. Baseline is main `38b17a508bb8ee237bea8144157a4273ebf67328`.
Sign-out #37/#38 remains an independent unfinished scope; this update does not
merge or discard its implementation. Historical #33 already adapted 0.4.1.

## Exact upstream inputs

Official stable v0.4.2 was published 2026-10-11T10:09:33Z, after the old hosted
Dashboard snapshot. Its origin is `a912c362b4f30c43610ae2f1d18844a9929637ed`.
CI resolves with Go module/checksum-database verification enabled. Resolved sums:

```
github.com/egoist/mygo v0.4.2 h1:Vs2FDcg+h0SShYSlL8vpxhFeSbMfylVeV31naaTPeoU=
github.com/egoist/mygo v0.4.2/go.mod h1:1sRzZ6jEhKE7iuEQXjyoXDgxyxQ1uTgMHDcDKZvoDIU=
```

The complete official 0.4.2 upstream archive was retrieved with verified ZIP
and module sums; the actual vendored MyGo files were compared to 0.4.1. All 28 selected upstream terminal hashes, 30 generated files,
the hardening patch and pinned libraries are unchanged. No checksum is relaxed
or regenerated merely because a version matches. Source preparation remains strict.
Other module versions and the toolchain are unchanged.

## Breaking changes: applicability, not just compilation

| Upstream change | Current Aster usage and verification |
| --- | --- |
| 0.4.0 AlertDialog Escape returns AlertDismissed (-2), not a Cancel index | No production AlertDialog use: operations use inline exact-context/name confirmation. New public-native-tester cases verify English/localized/confirm-only alerts dismiss without choosing a button or replaying the response. These are dependency contract fixtures, not cluster E2Es. |
| 0.4.0 installer filenames contain architecture | Current pipelines use `go build` and explicit `dist/aster` or `dist/aster.exe`, not MyGo installers or old installer globs. Signed packaging remains unimplemented/unqualified, not a claimed migration success. Future packaging must explicitly test architecture-qualified names including universal macOS. |
| 0.4.0 no generated src/mygo.ts when no services/events | No TypeScript frontend, generated-client consumer or `mygo generate` build step. No dummy TS client or WebView was introduced. |
| 0.4.1 Linux Bounds is the window-manager frame; ContentBounds is content | Neither accessor is used by production Aster. Real native-window smoke and minimum-window controls remain required. Future placement/restore code must review frame/content semantics. |
| 0.4 terminal OpenLink/OnPaste/OnKey dispatch additions | #33 already adapted the moved link dispatch. Retain disabled implicit local URL/path launches and OSC side effects, bounded native paste, and explicit remote stream ownership. This PR additionally requires the existing native link, paste and OSC52 tests in BOTH checked and production migration modes on each OS. |
| 0.4.2 keyed toast viewport and Spinner TextColor | New public API fixtures reproduce both toast-hover inheritance and ignored spinner color on 0.4.1 and pass unchanged on 0.4.2. Current product does not use these fixture dialogs/toasts/spinners; no Aster incident is inferred from the negative controls. |

`TestNativeMyGo04ProductionSurfaceInventory` parses production Go ASTs (excluding
tests/generated adapter) and fails on new AlertDialog, WebView/page-addressing or
frame-bounds integration until its applicability is reviewed. It is a deliberately
conservative source-inventory guard, not an interprocedural proof of non-use.
The existing constructor/lifetime and editor Undo/IME tests are retained.

## Qualification and boundaries

Four new top-level contract tests (nine records including subtests) enter the
mandatory checked/production migration suite; the three existing native terminal
security cases also run in each mode. Unit/race, source/find fuzz, actual OS vault,
three Kubernetes versions, independent Dex/OIDC, two-cluster isolation, codemod
no-op, native library/build identity and real Renovate extraction remain required.
Final test counts/commit/run identities belong in the PR and checkpoint, not this
pre-verification document. A staging devkit is not a merge gate.

Do not claim a perfect or universally compatible upgrade. Passing these gates
supports the covered current Native application contracts, not signed installers,
physical IME/accessibility/GPU/mixed-DPI behavior, universal IdPs, security-audit
certification or the unfinished enterprise product. Dashboard #11 stays open for
ongoing updates; an empty queue is not a vulnerability clearance.

## Primary sources

- https://github.com/egoist/mygo/releases/tag/v0.4.0
- https://github.com/egoist/mygo/releases/tag/v0.4.1
- https://github.com/egoist/mygo/releases/tag/v0.4.2
- https://github.com/egoist/mygo/compare/v0.4.1...v0.4.2
- https://github.com/egoist/mygo/pull/212
- https://github.com/egoist/mygo/pull/228
- https://github.com/egoist/mygo/pull/229
