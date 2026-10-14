# Resource-editor clipboard preflight (#63)

Implementation scope is tracked in Issue #63 and one branch,
`feat/manifest-clipboard-preflight`. This initial scope commit is not an
implementation or qualification result.

Resource Edit/Create will use the pinned public MyGo OnPaste hook and the
existing delivery clipboard budget calculation. Risky input will be retained
separately; normal native insertion, source bytes and mounted history remain
owned by the existing editor. Unresolved retained input blocks unsent resource
previews and execution until explicit discard and fresh review.

MyGo v0.4.8 commits existing preedit before native key/menu command dispatch,
before the hook can run. Interception must not freeze or undo that native
composition commit. The final implementation must characterize this boundary,
retain old tests, and add original-evidence gates before qualification.

Arbitrary direct Type/IME input, OS clipboard allocation, cross-unmount history,
editor-engine migration, Helm/GitOps and production release remain separate.
Enterprise authority stays P3 deferred outside the first MVP.
