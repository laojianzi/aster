# Incremental manifest buffer

The Native resource Edit/Create editor uses one MyGo `TextAreaBuffer` and indexed
`TextBuffer` per document identity. The buffer stays stable across normal input,
idle frames, Find, advisory Schema and unrelated status messages. Resource or
connection replacement deliberately resets the buffer identity and native history.
Apply-intent and other small text fields retain their existing string controls;
this milestone is not a rewrite of every text input or a complete IDE.

## Revisions and consumers

The UI owner observes immutable snapshots and keeps a semantic revision. Unchanged
frames compare snapshots in O(1), with no source flattening or whole-document
hashing. A changed root with different size/rune/line summaries is immediately
invalidated. Equal-summary changed roots use a bounded comparison in 1024-rune
chunks: MyGo can publish a no-op root while IME preedit is active. This does not
justify erasing Find navigation or Undo. The comparison is O(document size) in
that exceptional case, not a universal O(1) editing guarantee.

Find and Schema workers receive captured immutable snapshots, never a mutable UI
buffer. Only explicit consumers export text, bounded by 256 KiB / 5000 newline
characters and cancellation checks. Navigation freshness compares identity and
semantic revision, not a source hash on each frame. Preview captures the same
bounded input and revision; queued results and execution recheck programmatic
edits even without an intervening frame. Existing server dry-run, UID/version,
Prepared single-use and no-replay protections remain authoritative.

A snapshot shares the underlying unchanged chunks; it is immutable, not a promise
of secure erasure. An old captured snapshot can remain alive until its worker
terminates. The UI's prior supported snapshot shares tree nodes; it is not a full
plaintext persistence layer. This is not an application-wide native/Go memory cap.

## Oversized input

An oversized draft is retained in full, not flattened, truncated or converted to
an empty successful export. Its expensive layout and content consumers are
suspended. **Restore prior draft** explicitly discards the oversized edit and
adopts the last supported snapshot with a new document/Undo identity. It cannot
restore another resource's text. Without such a snapshot, Refresh detail is the
explicit discard/reload path; Create can be closed and restarted. No background
request can silently discard an oversized draft.

## Verification boundaries

Unit tests cover immutable concurrent snapshots, cancellation/byte/line limits,
zero-allocation idle revision checks and explicit export. Native tests cover
Undo/redo, equal-text identity reset, preedit publication, queued Schema and
preview rejection, execution without a frame, and oversized layout/restoration.
The existing real Kubernetes edit/Find/Schema tests additionally assert stable
buffer ownership and unchanged historical snapshots. Existing assertions are
preserved; string-based test inspection is replaced with explicit test-only reads.

Both checked and `mygo_noinspector` migration suites require these cases. CI records
64 KiB/256 KiB idle and small-edit allocation benchmarks; those are measurements
on development runners, not a guarantee of latency, full IDE performance, physical
IME/accessibility/GPU support, or enterprise production qualification.
