# Bounded terminal-record archival core

Scope [#99](https://github.com/laojianzi/aster/issues/99), sole
[PR #100](https://github.com/laojianzi/aster/pull/100), follows the qualified
Go 1.27.3 baseline `cf5ac071b9cb8225d83d90e628ea7dc214c0192a`. It reuses the
[durable journal](helm-uninstall-recovery.md) and the
[shared admission predicate](helm-journal-admission.md). This is a local storage
core, not an enabled native archive command. Backend/UI confirmation, lifecycle
integration and user-visible archive browsing need their own reviewed increment.

## Eligibility and retention decision

Only an original, completely parsed WAL with its terminal frame and no unresolved
effects can be archived. The existing `unresolved` classification is the single
source of truth: either all effects were independently observed complete, or a
closed attempt has no accepted, pending or unknown effect. Thus a terminal
no-effect or rejected attempt can qualify; Unknown, Pending, Partial, unclosed
and corrupt records cannot, regardless of newer cluster observations.

The original bytes, record ID, file identity, permissions, plan and effects are
retained. Archival changes only `ID.wal` to `ID.archived.wal` in the **same private,
locked directory**. It does not export, purge, rewrite, resolve, repair, restore
an active record, replay an operation or contact the cluster. No separate
resolution tombstone is needed: the complete original terminal record remains
the retained evidence. This is not an authenticated audit log or protection
against malicious same-user rollback.

Active history retains the original **128-record / 16 MiB** budget and the full
**256 KiB** reservation for a new operation. The independent archive is capped at
**128 records / 16 MiB**, with the same 256 KiB per-record maximum. Archival
reclaims active quota, **not disk space**. Directory scanning is bounded to 257
entries (256 records plus the permanent lock), and detects excess entries rather
than treating a truncated listing as complete. A full archive refuses another
move; ordinary execution can still use remaining active capacity. There is no
automatic rotation, eviction or second archive directory to evade those bounds.

Every archived record must still parse and satisfy the terminal/resolved rule
before admission excludes it from active quota. An invalid archive, duplicate
active/archive ID, unsafe file, corruption or excess archive budget fails closed.
Manually renaming an Unknown WAL therefore cannot clear its guard. Unrelated
unresolved active records remain unchanged and continue blocking their original
binding/namespace/Secret UID. Older application versions reject the unfamiliar
archived filename rather than silently overlooking it: downgrade is fail-closed.

## Independent review and one-shot confirmation

An already opened `Store` provides the following local APIs:

1. `PrepareArchive(ctx, id)` reads and validates the selected active record and
   archive capacity without writing anything. Its opaque review freezes the
   open store state, canonical ID, file identity, complete byte digest, checked
   history digest, byte count and a 30-second deadline measured from review
   start. Defensive accessors cannot alter that private selection.
2. `review.Confirm(exactID)` is a separate confirmation, not an automatic part of
   inspection. The first confirmation attempt consumes the review, including a
   mistyped ID or expired review. It creates a private, revocable one-shot
   `ArchiveConsent`. Copies share consumption and revocation.
3. `Store.Archive(ctx, consent)` consumes the consent before validation or I/O.
   Wrong-store, cancelled, closed, busy, stale or repeated attempts cannot reuse
   it. Under the existing OS lock and mutex it rechecks capacity, original file
   identity, complete bytes, eligibility and deadline before the rename. A live
   execution writer cannot overlap archival; a blocked archival worker makes
   read-only admission and new archive review return Busy immediately.

A consent is tied to the original open store, not merely a path string. Closing
and reopening requires a new review/confirmation. It grants no execution or GET
authority, and neither the review nor consent is serialized. New cluster reads,
including proof that an object is now absent, are not inputs to eligibility.
This increment deliberately has no production consumer outside `helmjournal`.

## Commit, failure and process crash

Linux uses `renameat2(RENAME_NOREPLACE)` and macOS uses
`renameatx_np(RENAME_EXCL)` relative to the anchored directory. Windows uses
same-directory `MoveFileEx` with `MOVEFILE_WRITE_THROUGH` but **without**
replacement, cross-volume copy or delayed/reboot flags; the existing private
fixed-volume directory handles do not permit directory deletion sharing. An
unsupported operation fails closed. There is no fallback copy-and-delete and no
retry that could overwrite retained evidence.

After a successful rename, the core reopens and verifies the archived file's
original identity and bytes, verifies that the active name is absent, and syncs
the directory on Unix. Windows retains the existing file-sync/local-fixed-volume
contract without claiming universal directory-entry or arbitrary power-loss
durability. Synchronous filesystem calls cannot be forcibly cancelled.

The result separates three cases:

- **NotApplied:** refusal before invoking the rename; no archive effect dispatched.
- **Unknown:** rename was invoked but its result or the following verification /
  sync is uncertain. The open store is poisoned against further writes. No
  automatic rollback or replay occurs; explicitly reopen and inspect history.
- **Archived:** the move and required verification/sync succeeded. A cancellation
  or revocation observed after the move is returned alongside this fact, rather
  than falsely claiming no local change occurred.

Even a rename error is conservatively Unknown after dispatch. Existing
file/destination collisions are never overwritten. The original record remains
under its active or archived name after the tested process-crash boundaries;
there is no temporary copied WAL to clean up. If a post-move storage check fails,
reads of the preserved history remain possible but do not mint a new grant.

`List` / `Load` retain their active-history meaning. `ListArchived` /
`LoadArchived` provide explicit bounded, defensive offline access to archived
originals, including errors for damaged history. `Admission.ArchivedRecords` and
`ArchivedBytes` distinguish retained quota from active counts. As before,
`InventoryComplete` is not a promise that all active WALs have been semantically
parsed or that a later execution will be admitted.

Primary platform contracts:
[Linux rename flags](https://man7.org/linux/man-pages/man2/rename.2.html),
[Darwin rename](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/rename.2.html),
[MoveFileExW](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw),
[Go file operations](https://pkg.go.dev/os).

## Evidence and non-goals

The exact separate archival census in `scripts/helm_journal_archive_evidence.py`
is additive to all previous recovery/admission tests. Full unit/race/coverage
and the existing three-OS journal matrix require these names. Tests cover
eligibility, immutable/copy-shared grants, identity/content changes, revocation,
cancellation, quota reclamation without deletion, archive limits, forged archive
names, no-overwrite, lost rename responses, sync failure, concurrency and real
subprocess termination before rename, after rename and after verification.

The existing workflow time budgets, dependency pins, native consumers and older
Go tests are preserved. Cross-compilation is not actual Windows/macOS runtime
qualification. Final PR and actual merged main need separate original evidence
review before this scope is complete. No enabled desktop feature, general
retention policy for unresolved records, unlimited disk reclamation, independent
security audit, physical-device qualification or signed release is claimed.
