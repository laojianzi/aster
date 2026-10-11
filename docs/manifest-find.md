# Native manifest Find

Scope: Issue #32, the sole `feat/native-manifest-find` implementation, built on
completed source diagnostics #29/#30 and the reviewed MyGo 0.4.1 #33 upgrade.

## Interaction

Open a non-Secret resource in **Edit**, choose **Find**, type a literal query and
press **Search**. Typing the query alone does not run a search. The first match
is selected once; **Previous** and **Next** navigate the retained matches.
**Close** clears the query, results, pending navigation and query Undo identity.
The same-document manifest Undo history is not reset. Search and navigation do
not mutate text, create a Prepared operation, send requests or apply changes.
Actual typing into a selected match is a normal local edit; Preview/Execute
retain their existing server-side validation and confirmation requirements.

Matching is exact, case-sensitive, non-overlapping and unnormalized. Punctuation
is literal, not a regular expression. Coordinates count Unicode code points,
not bytes, grapheme clusters or terminal cells. LF starts a new line; CR and BOM
are preserved by the search core as source characters. MyGo normalizes CRLF to
LF when inserting text through a native input event; Find uses the resulting
current draft, not the pre-input bytes. The search core can inspect invalid YAML
as text without changing Kubernetes decoding or admission semantics. The native
query control is a single-line text field.

## Explicit bounds

The document uses the existing editor budget of 256 KiB and at most 5,000 LF
separators. A query is nonempty valid UTF-8, at most 1,024 bytes and 256 code
points. The linear-time KMP scan checks cancellation periodically and retains
at most 512 matches. Truncation is reported only after another match is found;
wrapping traverses this retained prefix, not an invented complete result set.
Results expose value ranges and digests rather than retaining source/query
copies or excerpts. Errors/status never echo document values or query text.
This is not a secure-memory-erasure guarantee.

At most one search worker per window is admitted. Closing or editing cancels
it, but admission is released only when its completion is handled. Work has a
five-second deadline and uses the current connection context. Preflight and
freshness hashing reject oversized input before hashing. Draft hashes,
revision, detail/connection epochs, session identity and query hashes bind each
result; local UI reads and bounded hashing still consume the UI thread budget.

Navigation stores immutable ranges/epochs, never a MyGo Element or Context.
Applying the selection is synchronous within the current view build. Ongoing
composition defers it; a committed edit invalidates it, and a focus change
cancels a deferred jump instead of stealing focus. Status continues to show
truncation/wrap. Last explicit Schema/Find navigation wins. Tab/resource change,
disconnect, lease expiration, credential panels and window close discard the
old query/results. Stale or undelivered callbacks cannot publish into a newer
draft or regain another worker slot.

## Verification boundaries

Core tests and fuzzing compare literal ranges with an independent substring
reference and exercise UTF-8, CRLF, BOM, combining characters, empty/oversized
input, cancellation, adversarial repeated prefixes and exact truncation.
Native tests exercise selection/Undo, minimum window, composition, one-shot
focus, canceled admission and changed identity/draft/query epochs in both MyGo
checked and production builds. A real-cluster test searches a Pod draft, types
a local image correction, undoes it, and checks the live Pod is unchanged.
Existing OS vault, OIDC provider, Kubernetes and terminal gates remain mandatory.

Raw exact-head and post-merge results belong in the PR and #32/#13 checkpoint;
this description is not a passing-test receipt. No regex, replace-all,
incremental text engine, global resource search or full IDE completion is
provided. CI composition simulations and window smoke are not physical
IME/accessibility/GPU/mixed-DPI qualification. No production release is claimed.
