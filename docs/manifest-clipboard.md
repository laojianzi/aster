# Resource-editor clipboard preflight (#63)

The shared resource **Edit** / **New resource** editor now uses the pinned public
MyGo `Element.OnPaste` hook before inserting clipboard text. This fills a gap in
resource editing; the already-rendered manifest delivery workflow keeps its own
retention UI and now shares the same conservative budget calculation. No editor
engine, dependency, input-history implementation or cluster permission changes.

## What is checked

The current immutable snapshot plus incoming raw clipboard text must fit all of:

- 256 KiB of source bytes;
- 5,000 newline characters (`LF`, including the LF in a CRLF pair);
- a conservative 8,192-byte upper bound for a potentially joined line.

The byte test runs first, without scanning or copying an oversized incoming
string. Newline and longest-line scans run only when the combined byte budget
fits. The explicit clipboard consumer can flatten the bounded current snapshot;
idle frames still do not flatten it. Delivery retains its existing 2 MiB /
160,000-newline limits and 8,192-byte conservative line bound.

Selections are deliberately not subtracted and native composition is not
reconstructed. This can retain a paste that would fit after replacing selected
text, or after taking account of the exact insertion point. It is a conservative
insertion bound, not an exact prediction or a claim that every retained input is
invalid. Empty clipboard text is a native no-op.

An accepted paste is inserted by the original native control, including its
existing CRLF-to-LF input normalization and Undo/Redo. Loaded source and captured
snapshots keep their original bytes. The app does not inject keys, replace the
buffer or maintain a second history to perform a paste.

## Retention and review

A risky paste is kept as one complete separate string, without truncation,
normalization or insertion. **Manifest retained paste** shows only its byte count
and a fixed explanation. **Discard retained editor paste** releases that retained
reference; it does not replace the editor or restore a previous approval. Another
paste while the slot is occupied is not inserted and cannot overwrite the first
retained string. The system clipboard is not modified by interception.

The original editor stays mounted and editable. Existing selection/history and
new ordinary typing remain native; Find and Schema can inspect the current draft.
This does not promise history across switching away from the editor. Resource
mutation previews and execution are blocked until the retained paste is discarded.
Unsent previews, queued successful results, Prepared plans and confirmations are
revoked without inventing a source edit. A fresh explicit server dry-run and normal
confirmation are required afterward. Already-submitted operations keep their own
result or Unknown state; retention cannot cancel or roll them back.

Paste callbacks capture only public buffer and document/connection/detail identity,
not a build-scoped Element or Context. Same-document unrendered edits are observed
before calculating the current budget. Old-document, hidden-tab, closed or expired
callbacks consume nothing into the new document. A generation-bound discard cannot
clear another retained paste. Explicit document replacement, detail teardown,
disconnect, credential expiry and window close release the retained reference.
Separate workspace owners do not share retained clipboard text.

## Native composition boundary

MyGo v0.4.8 calls `commitCompose` before dispatching both native keyboard and menu
commands, before `OnPaste` runs. Thus a paste command can commit existing preedit
text even when its clipboard insertion is intercepted. The incoming clipboard
string is not inserted, but this earlier native composition commit is not frozen
or reversed. The hook evaluates the snapshot after that commit and invalidates
stale source-bound work normally. It neither clears a composition on its own nor
sets the editor read-only (which would clear preedit).

Regression tests distinguish the handler/rendering path from the native command
path. Active-composition interception is compared with an empty native paste,
including subsequent typing and Undo/Redo. The prior stronger assumption that a
paste command leaves preedit uncommitted was disproved by an executed native test,
not adopted as a workaround or silently hidden.

Pinned primary source: `egoist/mygo` commit
`c0e1068027faf8b14777c9cb925c937ef34ac2bb`, `ui/textinput.go` (`OnPaste`),
`ui/editor.go` (`process` and `commitCompose`) and `ui/editor_navigation.go`
(`command`). The compatibility review in `editor-upstream-mygo-0.4.8-review.md`
continues to apply to the separate editor engine.

## Verification and limits

Core tests cover exact byte/newline/line boundaries, Unicode/CRLF, independent
split-line comparisons, conservative selected-replacement behavior, unchanged
snapshots and zero-allocation rejection of a preexisting oversized string.
Native tests cover keyboard/menu pastes, original mixed bytes, selection,
Undo/Redo while retained and after discard, minimum Edit/Create windows,
composition, repeated input, unrendered edits, stale callback/discard, queued
previews, execution guards and workspace/expiry/window teardown. All four new
native cases are mandatory on three OSes and both checked/production migration
builds, retaining prior scopes. Real-cluster Edit/Create cases prove fresh dry-run
resumption, absent unsubmitted creates and unchanged live UID/resourceVersion/data.
Exact commit qualification and original evidence belong to #63 and its single PR.

The OS has already supplied a complete clipboard string before this hook. Retaining
it is not a bound on OS allocation, native/Go process memory or secure erasure.
Arbitrary giant direct Type/IME payloads, full physical IME/font/GPU acceptance,
safe completion insertion, cross-unmount history, Helm/GitOps and production
release remain unfinished. Enterprise authority stays P3 deferred outside MVP.
