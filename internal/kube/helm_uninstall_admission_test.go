package kube

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
)

type helmAdmissionFileFact struct {
	info os.FileInfo
	sum  [sha256.Size]byte
}

// Retain real directory/file identities and bytes, not a second implementation
// of admission. Directory modification timestamps may settle on host-backed
// filesystems; file timestamps, entries, inode, mode and size remain exact.
func helmAdmissionFileFacts(t *testing.T, dir string) map[string]helmAdmissionFileFact {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"."}
	for _, entry := range entries {
		paths = append(paths, entry.Name())
	}
	facts := make(map[string]helmAdmissionFileFact, len(paths))
	for _, name := range paths {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		fact := helmAdmissionFileFact{info: info}
		if info.Mode().IsRegular() {
			// Zero size uniquely establishes empty contents, including the
			// permanent lock that Windows refuses to read while it is held.
			fact.sum = sha256.Sum256(nil)
			if info.Size() != 0 {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				fact.sum = sha256.Sum256(data)
			}
		}
		facts[name] = fact
	}
	return facts
}

func helmAdmissionFilesUnchanged(t *testing.T, dir string, before map[string]helmAdmissionFileFact) {
	t.Helper()
	after := helmAdmissionFileFacts(t, dir)
	if len(before) != len(after) {
		t.Fatal("local review added or removed an entry")
	}
	for name, old := range before {
		now, ok := after[name]
		if !ok || !os.SameFile(old.info, now.info) || old.info.Mode() != now.info.Mode() || old.info.Size() != now.info.Size() ||
			!old.info.IsDir() && !old.info.ModTime().Equal(now.info.ModTime()) || old.sum != now.sum {
			t.Fatal("local review changed retained storage", name)
		}
	}
}

func helmAdmissionNoHTTP(t *testing.T, f *helmReconciliationFixture) {
	t.Helper()
	lists, objects, requests := f.inspect(t)
	if lists != 0 || objects != 0 || len(requests) != 0 {
		t.Fatal("local admission performed HTTP", requests)
	}
}

func helmAdmissionHistory(t *testing.T, f *helmReconciliationFixture, dir string, count int, unknown bool) string {
	t.Helper()
	store, err := helmjournal.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	binding, err := f.b.helmUninstallJournalBinding()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Hour)
	plan := helmjournal.Plan{Binding: binding, PreparationID: strings.Repeat("a", 32), Namespace: "team", Revision: selectedHelmRevision(),
		Targets: f.targets, Started: started, Expires: started.Add(30 * time.Second)}
	id := ""
	for i := 0; i < count; i++ {
		writer, err := store.Begin(plan)
		if err != nil {
			t.Fatal(err)
		}
		id = writer.ID()
		if unknown {
			if err := writer.Intent(0, plan.Revision.ResourceVersion); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Finish(started.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return id
}

func requireHelmAdmission(t *testing.T, b *Backend, dir, session, ns string, row helmrelease.Revision, reason helmjournal.AdmissionReason, wantErr error) *HelmJournalAdmissionReview {
	t.Helper()
	started := time.Now()
	r, err := b.ReviewHelmUninstallJournalAdmission(context.Background(), dir, session, ns, row)
	if r == nil || !errors.Is(err, wantErr) || r.Report().Reason != reason || !r.Matches(b, dir, session, ns, row) {
		t.Fatalf("local review: report %v, error %v; wanted %s / %v", r, err, reason, wantErr)
	}
	if r.CheckedAt().Before(started) || r.CheckedAt().After(time.Now()) {
		t.Fatal("local review has no current check timestamp")
	}
	if err != nil && r.Report().Reason == helmjournal.AdmissionNoBlocker {
		t.Fatal("a failed local check claimed NoBlocker")
	}
	return r
}

func TestHelmUninstallJournalAdmissionReadOnly(t *testing.T) {
	for _, name := range []string{"not-initialized", "existing-no-blocker", "retained-unknown", "unrelated-corruption", "busy", "unavailable", "capacity", "metadata-statuses", "opaque-accessors"} {
		t.Run(name, func(t *testing.T) {
			// This fixture has only a current metadata row and historical bytes;
			// it has never requested preparation or made a cluster request.
			f := newHelmReconciliationFixture(t, 1)
			parent := t.TempDir()
			dir := filepath.Join(parent, "journal")
			row := f.row // uninstalling is a valid local-check selection.
			reason, wantErr := helmjournal.AdmissionNoBlocker, error(nil)
			watch, id := dir, ""
			switch name {
			case "not-initialized":
				watch, reason, wantErr = parent, helmjournal.AdmissionNotInitialized, helmjournal.ErrUnavailable
			case "unavailable":
				if err := os.WriteFile(dir, []byte("PRIVATE_NOT_A_DIRECTORY"), 0600); err != nil {
					t.Fatal(err)
				}
				watch, reason, wantErr = parent, helmjournal.AdmissionUnavailable, helmjournal.ErrUnavailable
			default:
				count := 0
				if name == "existing-no-blocker" || name == "retained-unknown" || name == "unrelated-corruption" || name == "capacity" {
					count = 1
				}
				id = helmAdmissionHistory(t, f, dir, count, name == "retained-unknown")
			}
			switch name {
			case "retained-unknown":
				reason, wantErr = helmjournal.AdmissionUnresolved, helmjournal.ErrUnresolved
			case "unrelated-corruption":
				row.UID = "another-storage-uid"
				file, err := os.OpenFile(filepath.Join(dir, id+".wal"), os.O_WRONLY|os.O_APPEND, 0600)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := file.Write([]byte("PRIVATE_DAMAGED_TAIL"))
				closeErr := file.Close()
				if writeErr != nil || closeErr != nil {
					t.Fatal(writeErr, closeErr)
				}
				reason, wantErr = helmjournal.AdmissionCorrupt, helmjournal.ErrCorrupt
			case "busy":
				owner, err := helmjournal.Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
				reason, wantErr = helmjournal.AdmissionBusy, helmjournal.ErrBusy
			case "capacity":
				if err := os.Truncate(filepath.Join(dir, id+".wal"), helmjournal.MaxOperationBytes+1); err != nil {
					t.Fatal(err)
				}
				reason, wantErr = helmjournal.AdmissionOperationBytes, helmjournal.ErrBudget
			}
			before := helmAdmissionFileFacts(t, watch)
			r := requireHelmAdmission(t, f.b, dir, "current-session", "team", row, reason, wantErr)
			if reason == helmjournal.AdmissionNoBlocker && !r.Report().InventoryComplete ||
				(reason == helmjournal.AdmissionBusy || reason == helmjournal.AdmissionNotInitialized) && r.Report().InventoryComplete {
				t.Fatal("unknown inventory was confused with a checked empty store")
			}
			if name == "retained-unknown" && (r.Report().RecordID != id || r.Report().UnresolvedKind != helmjournal.UnresolvedUnknown) {
				t.Fatal("current status/RV bypassed retained Unknown", r.Report())
			}
			if name == "unrelated-corruption" && r.Report().RecordID != id {
				t.Fatal("unrelated damaged history lost its canonical blocker ID")
			}
			if name == "metadata-statuses" {
				for _, status := range []string{"unknown", "deployed", "uninstalled", "superseded", "failed", "uninstalling", "pending-install", "pending-upgrade", "pending-rollback"} {
					row.Status = status
					requireHelmAdmission(t, f.b, dir, "current-session", "team", row, helmjournal.AdmissionNoBlocker, nil)
				}
			}
			if name == "opaque-accessors" {
				original := r.Report()
				copy := r.Report()
				copy.Reason, copy.RecordID, copy.UnresolvedKind = helmjournal.AdmissionUnresolved, strings.Repeat("f", 32), helmjournal.UnresolvedUnknown
				copy.Records, copy.Bytes, copy.InventoryComplete = 99, 99, false
				if r.Report() != original {
					t.Fatal("Report accessor aliases internal state")
				}
				data, err := json.Marshal(r)
				if err != nil || string(data) != "{}" {
					t.Fatal("local review exposed private scope through serialization")
				}
				var missing *HelmJournalAdmissionReview
				for _, empty := range []*HelmJournalAdmissionReview{missing, {}} {
					if empty.Report().Reason == helmjournal.AdmissionNoBlocker || !empty.CheckedAt().IsZero() || empty.Matches(f.b, dir, "current-session", "team", row) {
						t.Fatal("nil/zero local review became an accepted snapshot")
					}
				}
			}
			helmAdmissionFilesUnchanged(t, watch, before)
			helmAdmissionNoHTTP(t, f)
		})
	}
	t.Run("grants-unchanged", func(t *testing.T) {
		f := newHelmExecutionFixture(t, false)
		if err := f.journal.Close(); err != nil {
			t.Fatal(err)
		}
		unconsumed := f.dry.consent(t)
		defer unconsumed.Discard()
		expires, preparationID := f.preflight.ExpiresAt(), f.p.ID()
		before := helmAdmissionFileFacts(t, f.journalDir)
		requireHelmAdmission(t, f.b, f.journalDir, "session", "team", selectedHelmRevision(), helmjournal.AdmissionNoBlocker, nil)
		requests, events, _ := f.inspect(t)
		if len(requests) != 0 || len(events) != 0 || f.preflight.ExpiresAt() != expires || f.p.ID() != preparationID ||
			!f.p.Matches(f.b, "session", "team", selectedHelmRevision(), time.Now()) ||
			!f.preflight.Matches(f.b, "session", "team", selectedHelmRevision(), time.Now()) {
			t.Fatal("local review made HTTP or changed independent preparation/dry-run")
		}
		helmAdmissionFilesUnchanged(t, f.journalDir, before)
		var err error
		f.journal, err = OpenHelmUninstallJournal(f.journalDir)
		if err != nil {
			t.Fatal("local reader retained its lock", err)
		}
		// Only this later explicit acknowledgement may consume the existing
		// successful dry-run's execution mint. No execution request follows.
		grant := f.consent(t)
		grant.Discard()
		if out, err := f.b.PreflightHelmUninstall(context.Background(), unconsumed, "session", "team", selectedHelmRevision()); err != nil || out == nil {
			t.Fatal("local review consumed an independent dry-run consent", err)
		}
		helmAdmissionFilesUnchanged(t, f.journalDir, before)
	})
}

func TestHelmUninstallJournalAdmissionScopeBinding(t *testing.T) {
	for _, name := range []string{"exact-presentation", "invalid-selection", "owner-lifetime", "same-local-guard", "frozen-connection"} {
		t.Run(name, func(t *testing.T) {
			f := newHelmReconciliationFixture(t, 1)
			dir := filepath.Join(t.TempDir(), "journal")
			id := helmAdmissionHistory(t, f, dir, 1, true)
			row := f.row
			before := helmAdmissionFileFacts(t, dir)
			r := requireHelmAdmission(t, f.b, dir, "current-session", "team", row, helmjournal.AdmissionUnresolved, helmjournal.ErrUnresolved)
			switch name {
			case "exact-presentation":
				for _, change := range []string{"path", "session", "namespace", "release", "number", "storage-name", "uid", "resource-version", "status"} {
					path, session, ns, selected := dir, "current-session", "team", row
					switch change {
					case "path":
						path += "-different"
					case "session":
						session = "next-session"
					case "namespace":
						ns = "other"
					case "release":
						selected.Release = "another"
					case "number":
						selected.Number++
					case "storage-name":
						selected.StorageName += "-other"
					case "uid":
						selected.UID += "-replacement"
					case "resource-version":
						selected.ResourceVersion += "-new"
					case "status":
						selected.Status = "deployed"
					}
					if r.Matches(f.b, path, session, ns, selected) {
						t.Fatal("old report matched a changed presentation scope", change)
					}
				}
			case "invalid-selection":
				for _, change := range []string{"nil-backend", "zero-backend", "empty-binding", "invalid-binding", "empty-session", "control-session", "long-session", "namespace", "release", "storage-name", "number", "status", "uid", "resource-version", "empty-path", "relative-path", "noncanonical-path"} {
					copy := *f.b
					b, path, session, ns, selected := &copy, dir, "current-session", "team", row
					switch change {
					case "nil-backend":
						b = nil
					case "zero-backend":
						b = &Backend{}
					case "empty-binding":
						b.helmJournalBinding = ""
					case "invalid-binding":
						b.helmJournalBinding = "PRIVATE_INVALID_BINDING"
					case "empty-session":
						session = ""
					case "control-session":
						session = "PRIVATE\nsession"
					case "long-session":
						session = strings.Repeat("s", 257)
					case "namespace":
						ns = "../PRIVATE"
					case "release":
						selected.Release = "../PRIVATE"
					case "storage-name":
						selected.StorageName = "PRIVATE_WRONG_STORAGE"
					case "number":
						selected.Number = 0
					case "status":
						selected.Status = "PRIVATE_UNSUPPORTED_STATUS"
					case "uid":
						selected.UID = "PRIVATE\nUID"
					case "resource-version":
						selected.ResourceVersion = "PRIVATE RV"
					case "empty-path":
						path = ""
					case "relative-path":
						path = "PRIVATE_RELATIVE_PATH"
					case "noncanonical-path":
						path += string(os.PathSeparator) + "."
					}
					if out, err := b.ReviewHelmUninstallJournalAdmission(context.Background(), path, session, ns, selected); out != nil || err != ErrHelmJournalAdmission {
						t.Fatal("invalid local scope did not fail with the fixed wrapper error", change, err)
					}
				}
			case "owner-lifetime":
				copy := *f.b
				if !r.Matches(&copy, dir, "current-session", "team", row) {
					t.Fatal("a backend copy lost its shared private owner identity")
				}
				fresh, err := New(f.config)
				if err != nil {
					t.Fatal(err)
				}
				copy.identity = &backendIdentity{marker: 1}
				for _, b := range []*Backend{nil, {}, &copy, fresh} {
					if r.Matches(b, dir, "current-session", "team", row) {
						t.Fatal("an old report matched a different owner")
					}
				}
				copy = *f.b
				copy.helmJournalBinding = strings.Repeat("0", 64)
				if r.Matches(&copy, dir, "current-session", "team", row) {
					t.Fatal("a changed frozen binding retained a current presentation")
				}
				copy = Backend{}
				if r.Matches(&copy, dir, "current-session", "team", row) {
					t.Fatal("a reassigned owner wrapper retained a current presentation")
				}
			case "same-local-guard":
				fresh, err := New(f.config)
				if err != nil {
					t.Fatal(err)
				}
				row.Status, row.ResourceVersion = "uninstalled", "later-storage-rv"
				current := requireHelmAdmission(t, fresh, dir, "restarted-session", "team", row, helmjournal.AdmissionUnresolved, helmjournal.ErrUnresolved)
				if current.Report().RecordID != id || current.Report().UnresolvedKind != helmjournal.UnresolvedUnknown || r.Matches(fresh, dir, "restarted-session", "team", row) {
					t.Fatal("fresh presentation scope bypassed or rewrote the historical guard")
				}
			case "frozen-connection":
				// The public Config copy is editable; it cannot renew or replace the
				// binding captured before this Backend's transports were built.
				config := f.b.Config()
				config.Host, config.BearerToken = "https://changed.invalid/PRIVATE", "PRIVATE_REPLACEMENT"
				current := requireHelmAdmission(t, f.b, dir, "current-session", "team", row, helmjournal.AdmissionUnresolved, helmjournal.ErrUnresolved)
				if current.Report().RecordID != id || !r.Matches(f.b, dir, "current-session", "team", row) {
					t.Fatal("an edited config projection rebound the journal guard")
				}
			}
			helmAdmissionFilesUnchanged(t, dir, before)
			helmAdmissionNoHTTP(t, f)
		})
	}
}

type helmAdmissionCancelContext struct {
	context.Context
	cancel context.CancelFunc
	left   int
}

func (c *helmAdmissionCancelContext) Err() error {
	c.left--
	if c.left <= 0 {
		c.cancel()
	}
	return c.Context.Err()
}

// The context observes real lock ownership, then cancels at the first boundary
// after the reader releases it. No sleep or exact number of Err calls is used.
type helmAdmissionReleasedContext struct {
	context.Context
	cancel         context.CancelFunc
	dir            string
	held, released bool
	probeErr       error
}

func (c *helmAdmissionReleasedContext) Err() error {
	if err := c.Context.Err(); err != nil {
		return err
	}
	owner, err := helmjournal.Open(c.dir)
	switch {
	case errors.Is(err, helmjournal.ErrBusy):
		c.held = true
	case err == nil:
		c.probeErr = owner.Close()
		if c.held {
			c.released = true
			c.cancel()
		}
	default:
		c.probeErr = err
	}
	if c.probeErr != nil {
		c.cancel()
	}
	return c.Context.Err()
}

func TestHelmUninstallJournalAdmissionCancellation(t *testing.T) {
	for _, name := range []string{"nil", "cancelled", "expired", "bounded-scan", "after-reader-close"} {
		t.Run(name, func(t *testing.T) {
			f := newHelmReconciliationFixture(t, 1)
			dir := filepath.Join(t.TempDir(), "journal")
			count := 1
			if name == "bounded-scan" {
				count = 24
			}
			helmAdmissionHistory(t, f, dir, count, false)
			before := helmAdmissionFileFacts(t, dir)
			var ctx context.Context
			var released *helmAdmissionReleasedContext
			wantErr := error(ErrHelmJournalAdmission)
			if name != "nil" {
				base, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx, wantErr = base, context.Canceled
				switch name {
				case "cancelled":
					cancel()
				case "expired":
					deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					defer stop()
					ctx, wantErr = deadline, context.DeadlineExceeded
				case "bounded-scan":
					ctx = &helmAdmissionCancelContext{Context: base, cancel: cancel, left: 12}
				case "after-reader-close":
					released = &helmAdmissionReleasedContext{Context: base, cancel: cancel, dir: dir}
					ctx = released
				}
			}
			out, err := f.b.ReviewHelmUninstallJournalAdmission(ctx, dir, "current-session", "team", f.row)
			if out != nil || !errors.Is(err, wantErr) {
				t.Fatal("cancelled local check returned a current snapshot", err)
			}
			if released != nil && (!released.held || !released.released || released.probeErr != nil) {
				t.Fatal("cancellation did not occur after actual reader lock release", released.probeErr)
			}
			helmAdmissionFilesUnchanged(t, dir, before)
			helmAdmissionNoHTTP(t, f)
			owner, err := helmjournal.Open(dir)
			if err != nil {
				t.Fatal("cancelled local reader retained the journal lock", err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
