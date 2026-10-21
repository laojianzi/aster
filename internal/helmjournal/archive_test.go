package helmjournal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func archiveHistory(t *testing.T, s *Store, kind string) string {
	t.Helper()
	w := testBegin(t, s, testPlan())
	switch kind {
	case "completed", "unclosed-completed":
		allCompleted(t, w)
	case "partial":
		beginCompleted(t, w)
	case "rejected", "unknown", "pending":
		mustJournal(t, w.Intent(0, "7"))
		outcome, rv := Rejected, ""
		if kind == "unknown" {
			outcome = Unknown
		}
		if kind == "pending" {
			outcome, rv = AcceptedPending, "8"
		}
		mustJournal(t, w.Record(0, outcome, rv))
	}
	if strings.HasPrefix(kind, "unclosed") {
		mustJournal(t, w.Abort())
	} else {
		mustJournal(t, w.Finish(time.Now()))
	}
	if kind == "corrupt" {
		f, err := os.OpenFile(filepath.Join(s.root.path, w.ID()+".wal"), os.O_APPEND|os.O_WRONLY, 0600)
		mustJournal(t, err)
		_, err = f.Write([]byte("torn"))
		mustJournal(t, err)
		mustJournal(t, f.Close())
	}
	return w.ID()
}
func archiveGrant(t *testing.T, s *Store, id string) *ArchiveConsent {
	t.Helper()
	review, err := s.PrepareArchive(context.Background(), id)
	mustJournal(t, err)
	grant, err := review.Confirm(id)
	mustJournal(t, err)
	return grant
}
func archiveFixture(t *testing.T, s *Store, id string, archived bool, data []byte) {
	t.Helper()
	name := id + ".wal"
	if archived {
		name = id + archivedSuffix
	}
	f, err := s.root.openFile(name, true, true)
	mustJournal(t, err)
	_, err = f.Write(data)
	mustJournal(t, err)
	mustJournal(t, f.Sync())
	mustJournal(t, f.Close())
}

// A compact valid terminal record with no accepted effects. Use the actual
// encoder and parser; the large-record helper assumes a uniform target RV.
func archiveRecordBytes(t *testing.T, id string, p Plan) []byte {
	t.Helper()
	finished := p.Started.Add(time.Second)
	first, digest, err := encodeFrame(frame{Version: 1, Sequence: 1, Event: "start", ID: id, Plan: &p})
	mustJournal(t, err)
	last, _, err := encodeFrame(frame{Version: 1, Sequence: 2, Previous: digest, Event: "finish", Finished: &finished})
	mustJournal(t, err)
	data := append(append([]byte(journalMagic), first...), last...)
	if snapshot := parse(data, id).snapshot; !archiveEligible(snapshot) {
		t.Fatal("invalid terminal fixture")
	}
	return data
}

func TestHelmJournalArchiveEligibility(t *testing.T) {
	for _, kind := range []string{"not-started", "rejected", "completed", "unclosed-not-started", "unclosed-completed", "unknown", "pending", "partial", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := testStore(t)
			id := archiveHistory(t, s, kind)
			before := admissionFiles(t, dir)
			review, err := s.PrepareArchive(context.Background(), id)
			allowed := kind == "not-started" || kind == "rejected" || kind == "completed"
			if allowed {
				mustJournal(t, err)
				if review.Record().ID != id {
					t.Fatal("wrong review identity")
				}
			} else {
				want := ErrUnresolved
				if kind == "corrupt" {
					want = ErrCorrupt
				}
				if !errors.Is(err, want) || review != nil {
					t.Fatal("uncertain history became archive authority", err)
				}
			}
			unchangedAdmissionFiles(t, dir, before)
			if s.active != nil || s.poisoned {
				t.Fatal("review changed store state")
			}
		})
	}
}

func TestHelmJournalArchiveRoundTrip(t *testing.T) {
	s, dir := testStore(t)
	id := archiveHistory(t, s, "completed")
	before := admissionFiles(t, dir)
	// Capture identity through a real handle while the original name exists.
	// Windows path-based FileInfo lazily resolves its file ID; doing that only
	// after rename would query the now-absent old path rather than the old file.
	f, err := s.root.openFile(id+".wal", false, false)
	mustJournal(t, err)
	identity, err := f.Stat()
	mustJournal(t, err)
	mustJournal(t, f.Close())
	fact := before[id+".wal"]
	if !os.SameFile(fact.info, identity) || fact.info.Size() != identity.Size() || fact.info.Mode() != identity.Mode() {
		t.Fatal("archive snapshot identity does not match the original path")
	}
	fact.info = identity
	before[id+".wal"] = fact
	original, err := s.Load(id)
	mustJournal(t, err)
	review, err := s.PrepareArchive(context.Background(), id)
	mustJournal(t, err)
	copyRecord := review.Record()
	copyRecord.Plan.Targets[0].UID = "caller-changed"
	copyRecord.Effects[0].Outcome = Unknown
	if review.Record().Plan.Targets[0].UID == "caller-changed" || review.Record().Effects[0].Outcome == Unknown {
		t.Fatal("aliased review")
	}
	if review.Bytes() != before[id+".wal"].info.Size() || time.Until(review.ExpiresAt()) <= 0 || time.Until(review.ExpiresAt()) > ArchiveLifetime {
		t.Fatal("bad review limits")
	}
	grant, err := review.Confirm(id)
	mustJournal(t, err)
	copyGrant := *grant
	copyStore := *s
	result, err := copyStore.Archive(context.Background(), &copyGrant)
	mustJournal(t, err)
	if result.Outcome != ArchiveCompleted || result.RecordID != id || result.Bytes != review.Bytes() {
		t.Fatal("archive result lost identity")
	}
	if _, err := s.Archive(context.Background(), grant); !errors.Is(err, ErrArchiveConsent) {
		t.Fatal("grant copy replayed")
	}
	after := admissionFiles(t, dir)
	old, new := before[id+".wal"], after[id+archivedSuffix]
	if len(after) != len(before) || old.hash != new.hash || !os.SameFile(old.info, new.info) || old.info.Mode() != new.info.Mode() || !os.SameFile(before[lockName].info, after[lockName].info) {
		t.Fatalf("archive changed: entries %d/%d, same bytes %v, same identity %v, mode %v/%v, same lock %v",
			len(before), len(after), old.hash == new.hash, os.SameFile(old.info, new.info),
			old.info.Mode(), new.info.Mode(), os.SameFile(before[lockName].info, after[lockName].info))
	}
	if _, ok := after[id+".wal"]; ok {
		t.Fatal("active copy remains")
	}
	ids, err := s.List()
	mustJournal(t, err)
	if len(ids) != 0 {
		t.Fatal("archive counted as active")
	}
	ids, err = s.ListArchived()
	mustJournal(t, err)
	if len(ids) != 1 || ids[0] != id {
		t.Fatal("archive disappeared")
	}
	snapshot, err := s.LoadArchived(id)
	mustJournal(t, err)
	if snapshot.Digest != original.Digest || !snapshot.Closed || snapshot.Effects[3].Outcome != Completed {
		t.Fatal("original history changed")
	}
	snapshot.Effects[0].Outcome = Unknown
	again, err := s.LoadArchived(id)
	mustJournal(t, err)
	if again.Effects[0].Outcome != Completed {
		t.Fatal("aliased archive read")
	}
	admission, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
	mustJournal(t, err)
	if !admission.InventoryComplete || admission.Records != 0 || admission.Bytes != 0 || admission.ArchivedRecords != 1 || admission.ArchivedBytes != result.Bytes {
		t.Fatal("quota report is inaccurate")
	}
	w := testBegin(t, s, testPlan())
	mustJournal(t, w.Finish(time.Now()))
	mustJournal(t, s.Close())
	reopened, err := Open(dir)
	mustJournal(t, err)
	defer reopened.Close()
	recovered, err := reopened.LoadArchived(id)
	mustJournal(t, err)
	if recovered.Digest != original.Digest {
		t.Fatal("archive did not survive reopen")
	}
}

func TestHelmJournalArchiveConsent(t *testing.T) {
	for _, kind := range []string{"wrong-id", "confirm-twice", "review-expired", "grant-expired", "revoked", "cancelled", "nil-context", "wrong-store", "closed-store", "busy-writer", "busy-mutex", "changed-file", "changed-bytes", "changed-lock", "cancelled-during-scan"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := testStore(t)
			id := archiveHistory(t, s, "completed")
			review, err := s.PrepareArchive(context.Background(), id)
			mustJournal(t, err)
			if kind == "wrong-id" || kind == "review-expired" || kind == "confirm-twice" {
				copyReview := *review
				typed := id
				if kind == "wrong-id" {
					typed = " " + id
				}
				if kind == "review-expired" {
					review.state.expires = time.Now().Add(-time.Second)
				}
				_, err := copyReview.Confirm(typed)
				if kind == "confirm-twice" {
					mustJournal(t, err)
				} else if !errors.Is(err, ErrArchiveConsent) {
					t.Fatal("bad confirmation accepted")
				}
				if _, err := review.Confirm(id); !errors.Is(err, ErrArchiveConsent) {
					t.Fatal("review was reusable")
				}
				return
			}
			grant, err := review.Confirm(id)
			mustJournal(t, err)
			ctx := context.Background()
			target := s
			want := error(ErrArchiveConsent)
			switch kind {
			case "grant-expired":
				review.state.expires = time.Now().Add(-time.Second)
			case "revoked":
				copyGrant := *grant
				copyGrant.Discard()
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "nil-context":
				ctx = nil
				want = ErrInvalid
			case "wrong-store":
				target, _ = testStore(t)
			case "closed-store":
				mustJournal(t, s.Close())
				want = ErrClosed
			case "busy-writer":
				testBegin(t, s, testPlan())
				want = ErrBusy
			case "busy-mutex":
				s.mu.Lock()
				defer s.mu.Unlock()
				want = ErrBusy
			case "changed-file":
				path := filepath.Join(dir, id+".wal")
				data, err := os.ReadFile(path)
				mustJournal(t, err)
				mustJournal(t, os.Rename(path, filepath.Join(filepath.Dir(dir), "held-original")))
				archiveFixture(t, s, id, false, data)
			case "changed-bytes":
				p := testPlan()
				p.Revision.ResourceVersion = "different-rv"
				data := archiveRecordBytes(t, id, p)
				f, err := s.root.openFile(id+".wal", false, true)
				mustJournal(t, err)
				mustJournal(t, f.Truncate(0))
				_, err = f.Write(data)
				mustJournal(t, err)
				mustJournal(t, f.Close())
			case "changed-lock":
				_, err := s.lock.WriteAt([]byte{'!'}, 1)
				mustJournal(t, err)
				want = ErrUnavailable
			case "cancelled-during-scan":
				base, cancel := context.WithCancel(ctx)
				defer cancel()
				ctx = &admissionCancelContext{Context: base, cancel: cancel, left: 4}
				want = context.Canceled
			}
			before := admissionFiles(t, dir, s.lock)
			result, err := target.Archive(ctx, grant)
			if !errors.Is(err, want) || result.Outcome != ArchiveNotApplied {
				t.Fatalf("%s: got %s/%v, want refusal/%v", kind, result.Outcome, err, want)
			}
			unchangedAdmissionFiles(t, dir, before, s.lock)
			if _, err := s.Archive(context.Background(), grant); !errors.Is(err, ErrArchiveConsent) {
				t.Fatal("refused attempt could be retried")
			}
		})
	}
}

func TestHelmJournalArchiveUncertaintyGuards(t *testing.T) {
	for _, kind := range []string{"unknown", "pending", "partial", "unclosed-not-started", "corrupt", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := testStore(t)
			historyKind := kind
			if kind == "duplicate" {
				historyKind = "completed"
			}
			id := archiveHistory(t, s, historyKind)
			data, err := os.ReadFile(filepath.Join(dir, id+".wal"))
			mustJournal(t, err)
			if kind == "duplicate" {
				archiveFixture(t, s, id, true, data)
			} else {
				mustJournal(t, s.root.renameNoReplace(id+".wal", id+archivedSuffix))
			}
			before := admissionFiles(t, dir)
			if _, err := s.Begin(testPlan()); !errors.Is(err, ErrCorrupt) {
				t.Fatal("forged archive bypassed guard", err)
			}
			if _, err := s.ListArchived(); !errors.Is(err, ErrCorrupt) {
				t.Fatal("invalid archive hidden", err)
			}
			if kind != "duplicate" {
				if _, err := s.LoadArchived(id); !errors.Is(err, ErrCorrupt) {
					t.Fatal("invalid archive accepted", err)
				}
			}
			unchangedAdmissionFiles(t, dir, before)
		})
	}
	t.Run("unrelated-unknown-preserved", func(t *testing.T) {
		s, dir := testStore(t)
		done := archiveHistory(t, s, "completed")
		blocked := archiveHistory(t, s, "unknown")
		before, err := os.ReadFile(filepath.Join(dir, blocked+".wal"))
		mustJournal(t, err)
		_, err = s.Archive(context.Background(), archiveGrant(t, s, done))
		mustJournal(t, err)
		if _, err = s.Begin(testPlan()); !errors.Is(err, ErrUnresolved) {
			t.Fatal("archive cleared unrelated uncertainty")
		}
		report, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
		if !errors.Is(err, ErrUnresolved) || report.RecordID != blocked || report.ArchivedRecords != 1 {
			t.Fatal("unknown blocker lost")
		}
		after, err := os.ReadFile(filepath.Join(dir, blocked+".wal"))
		mustJournal(t, err)
		if !bytes.Equal(before, after) {
			t.Fatal("unknown WAL changed")
		}
	})
}

func TestHelmJournalArchiveCapacity(t *testing.T) {
	t.Run("active-limit-reclaims-one", func(t *testing.T) {
		s, _ := testStore(t)
		for i := 0; i < MaxRecords; i++ {
			id := fmt.Sprintf("%032x", i)
			archiveFixture(t, s, id, false, archiveRecordBytes(t, id, testPlan()))
		}
		if _, err := s.Begin(testPlan()); !errors.Is(err, ErrBudget) {
			t.Fatal("fixture not at active limit")
		}
		id := fmt.Sprintf("%032x", 0)
		result, err := s.Archive(context.Background(), archiveGrant(t, s, id))
		mustJournal(t, err)
		if result.Outcome != ArchiveCompleted {
			t.Fatal("full active store cannot archive")
		}
		w := testBegin(t, s, testPlan())
		mustJournal(t, w.Finish(time.Now()))
		report, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
		if !errors.Is(err, ErrBudget) || report.Records != MaxRecords || report.ArchivedRecords != 1 {
			t.Fatal("independent capacity accounting failed")
		}
	})
	t.Run("archive-limit-refuses-more", func(t *testing.T) {
		s, _ := testStore(t)
		active := archiveHistory(t, s, "completed")
		for i := 0; i < MaxArchivedRecords; i++ {
			id := fmt.Sprintf("%032x", i)
			archiveFixture(t, s, id, true, archiveRecordBytes(t, id, testPlan()))
		}
		if _, err := s.PrepareArchive(context.Background(), active); !errors.Is(err, ErrBudget) {
			t.Fatal("archive count is unbounded")
		}
		w := testBegin(t, s, testPlan())
		mustJournal(t, w.Finish(time.Now()))
	})
	t.Run("archive-over-limit-blocks", func(t *testing.T) {
		s, _ := testStore(t)
		for i := 0; i <= MaxArchivedRecords; i++ {
			id := fmt.Sprintf("%032x", i)
			archiveFixture(t, s, id, true, nil)
		}
		report, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
		if !errors.Is(err, ErrBudget) || report.Reason != AdmissionArchiveLimit || report.InventoryComplete {
			t.Fatal("archive overflow not bounded", err)
		}
	})
	t.Run("archive-bytes-blocks", func(t *testing.T) {
		s, _ := testStore(t)
		for i := 0; i <= MaxArchivedBytes/MaxOperationBytes; i++ {
			id := fmt.Sprintf("%032x", i)
			f, err := s.root.openFile(id+archivedSuffix, true, true)
			mustJournal(t, err)
			mustJournal(t, f.Truncate(MaxOperationBytes))
			mustJournal(t, f.Close())
		}
		report, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
		if !errors.Is(err, ErrBudget) || report.Reason != AdmissionArchiveLimit || report.InventoryComplete {
			t.Fatal("retained bytes are unbounded")
		}
	})
	t.Run("reservation-reclaimed-not-disk", func(t *testing.T) {
		s, dir := testStore(t)
		p := admissionLargePlan(t)
		sample := admissionLargeRecord(t, fixtureID, p)
		count := (MaxStoreBytes-MaxOperationBytes)/len(sample) + 1
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("%032x", i)
			archiveFixture(t, s, id, false, admissionLargeRecord(t, id, p))
		}
		before := admissionFiles(t, dir)
		report, err := s.ReviewAdmission(context.Background(), admissionScope(p))
		if !errors.Is(err, ErrBudget) || report.Reason != AdmissionReserveBytes {
			t.Fatal("fixture misses reservation barrier")
		}
		id := fmt.Sprintf("%032x", 0)
		result, err := s.Archive(context.Background(), archiveGrant(t, s, id))
		mustJournal(t, err)
		report, err = s.ReviewAdmission(context.Background(), admissionScope(p))
		mustJournal(t, err)
		if report.ArchivedBytes != result.Bytes || report.Bytes+report.ArchivedBytes != int64(len(sample)*count) || len(admissionFiles(t, dir)) != len(before) {
			t.Fatal("quota reclaim deleted bytes")
		}
	})
}

func TestHelmJournalArchiveStorageFailure(t *testing.T) {
	for _, kind := range []string{"rename-error", "committed-response-loss", "sync-error", "wrong-destination", "cancel-after-move", "revoke-after-move", "no-replace"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := testStore(t)
			id := archiveHistory(t, s, "completed")
			grant := archiveGrant(t, s, id)
			data, err := os.ReadFile(filepath.Join(dir, id+".wal"))
			mustJournal(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.renameArchive = func(old, new string) error {
				if kind == "rename-error" {
					return errors.New("PRIVATE_FAULT_PATH")
				}
				if kind == "no-replace" {
					archiveFixture(t, s, id, true, []byte("existing-target"))
				}
				if err := s.root.renameNoReplace(old, new); err != nil {
					return err
				}
				switch kind {
				case "committed-response-loss":
					return errors.New("PRIVATE_LOST_RESPONSE")
				case "wrong-destination":
					f, err := s.root.openFile(new, false, true)
					mustJournal(t, err)
					_, err = f.WriteAt([]byte{'!'}, 0)
					mustJournal(t, err)
					mustJournal(t, f.Close())
				case "cancel-after-move":
					cancel()
				case "revoke-after-move":
					grant.Discard()
				}
				return nil
			}
			if kind == "sync-error" {
				s.syncRoot = func() error { return errors.New("PRIVATE_SYNC_PATH") }
			}
			result, err := s.Archive(ctx, grant)
			if err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("failure was lost or leaked")
			}
			if kind == "cancel-after-move" || kind == "revoke-after-move" {
				if result.Outcome != ArchiveCompleted || s.poisoned {
					t.Fatal("late cancellation erased verified archive")
				}
			} else {
				if result.Outcome != ArchiveUnknown || !s.poisoned {
					t.Fatal("uncertain dispatch not fail-closed")
				}
				if _, err := s.Begin(testPlan()); !errors.Is(err, ErrUnavailable) {
					t.Fatal("poisoned store admitted writer")
				}
			}
			if _, err := s.Archive(context.Background(), grant); !errors.Is(err, ErrArchiveConsent) {
				t.Fatal("failed archive retried")
			}
			switch kind {
			case "rename-error", "no-replace":
				after, err := os.ReadFile(filepath.Join(dir, id+".wal"))
				mustJournal(t, err)
				if !bytes.Equal(data, after) {
					t.Fatal("failed rename changed source")
				}
				if kind == "no-replace" {
					target, err := os.ReadFile(filepath.Join(dir, id+archivedSuffix))
					mustJournal(t, err)
					if string(target) != "existing-target" {
						t.Fatal("existing archive overwritten")
					}
				}
			case "wrong-destination":
				if _, err := s.LoadArchived(id); !errors.Is(err, ErrCorrupt) {
					t.Fatal("damaged archive qualified")
				}
			default:
				archived, err := s.LoadArchived(id)
				mustJournal(t, err)
				if !archiveEligible(archived) {
					t.Fatal("checked archived fact lost")
				}
				after, err := os.ReadFile(filepath.Join(dir, id+archivedSuffix))
				mustJournal(t, err)
				if !bytes.Equal(data, after) {
					t.Fatal("WAL was rewritten")
				}
			}
		})
	}
}

func TestHelmJournalArchiveConcurrency(t *testing.T) {
	t.Run("copies-one-attempt", func(t *testing.T) {
		s, _ := testStore(t)
		id := archiveHistory(t, s, "completed")
		grant := archiveGrant(t, s, id)
		var wg sync.WaitGroup
		out := make(chan ArchiveResult, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				copyGrant := *grant
				r, _ := s.Archive(context.Background(), &copyGrant)
				out <- r
			}()
		}
		wg.Wait()
		close(out)
		success := 0
		for r := range out {
			if r.Outcome == ArchiveCompleted {
				success++
			}
		}
		if success != 1 {
			t.Fatal("confirmation duplicated or no archive committed")
		}
	})
	t.Run("locked-worker", func(t *testing.T) {
		s, _ := testStore(t)
		id := archiveHistory(t, s, "completed")
		grant := archiveGrant(t, s, id)
		entered, release := make(chan struct{}), make(chan struct{})
		finished := make(chan error, 1)
		s.renameArchive = func(old, new string) error { close(entered); <-release; return s.root.renameNoReplace(old, new) }
		go func() { _, err := s.Archive(context.Background(), grant); finished <- err }()
		<-entered
		_, err := s.PrepareArchive(context.Background(), id)
		if !errors.Is(err, ErrBusy) {
			close(release)
			<-finished
			t.Fatal("review waited on blocked archive")
		}
		report, err := s.ReviewAdmission(context.Background(), admissionScope(testPlan()))
		if !errors.Is(err, ErrBusy) || report.Reason != AdmissionBusy {
			close(release)
			<-finished
			t.Fatal("admission ignored archive worker")
		}
		close(release)
		mustJournal(t, <-finished)
	})
}

func TestHelmJournalArchiveInvalidHandles(t *testing.T) {
	var review *ArchiveReview
	var grant *ArchiveConsent
	var store *Store
	if review.Record().ID != "" || review.Bytes() != 0 || !review.ExpiresAt().IsZero() {
		t.Fatal("nil review has authority")
	}
	if _, err := review.Confirm(""); !errors.Is(err, ErrArchiveConsent) {
		t.Fatal("nil review confirmed")
	}
	grant.Discard()
	if _, err := store.Archive(context.Background(), grant); !errors.Is(err, ErrArchiveConsent) {
		t.Fatal("nil grant accepted")
	}
	if _, err := store.PrepareArchive(context.Background(), fixtureID); !errors.Is(err, ErrClosed) {
		t.Fatal("nil store accepted")
	}
	if _, err := store.ListArchived(); !errors.Is(err, ErrClosed) {
		t.Fatal("nil store listed")
	}
	if _, err := store.LoadArchived(fixtureID); !errors.Is(err, ErrClosed) {
		t.Fatal("nil store loaded")
	}
	s, _ := testStore(t)
	for _, id := range []string{"", "../secret", strings.Repeat("A", 32)} {
		if _, err := s.PrepareArchive(context.Background(), id); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid archive ID accepted")
		}
		if _, err := s.LoadArchived(id); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid archived ID loaded")
		}
	}
	mustJournal(t, s.Close())
	if _, err := s.ListArchived(); !errors.Is(err, ErrClosed) {
		t.Fatal("closed store listed")
	}
	if _, err := s.LoadArchived(fixtureID); !errors.Is(err, ErrClosed) {
		t.Fatal("closed store loaded")
	}
}
