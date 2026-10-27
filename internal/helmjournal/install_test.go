package helmjournal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func testInstallPlan() InstallPlan {
	old := testPlan()
	return InstallPlan{Binding: old.Binding, CandidateID: strings.Repeat("c", 32), PreparationID: old.PreparationID,
		Namespace: old.Namespace, NamespaceUID: "namespace-uid", NamespaceResourceVersion: "100", Release: old.Revision.Release,
		StorageName: "sh.helm.release.v1.example.v1", StorageDigest: strings.Repeat("d", 64),
		Targets: []InstallTarget{{Name: "first", Digest: strings.Repeat("e", 64)}, {Name: "second", Digest: strings.Repeat("f", 64)}}, Started: old.Started, Expires: old.Expires}
}
func testInstallBegin(t *testing.T, s *Store, p InstallPlan) *InstallWriter {
	t.Helper()
	w, err := s.BeginInstall(p)
	mustJournal(t, err)
	t.Cleanup(func() { mustJournal(t, w.Abort()) })
	return w
}
func installDigest(i int) string { return strings.Repeat([]string{"d", "e", "f", "1"}[i], 64) }
func installIntent(w *InstallWriter, i int) error {
	digest := installDigest(0)
	if i >= 0 && i < 4 {
		digest = installDigest(i)
	}
	return w.Intent(i, digest)
}
func installStep(t *testing.T, w *InstallWriter, i int) {
	t.Helper()
	uid, rv := fmt.Sprintf("actual-%d", i), fmt.Sprintf("%d", 10+i)
	if i == 3 {
		uid = "actual-0"
	}
	mustJournal(t, installIntent(w, i))
	mustJournal(t, w.Response(i, AcceptedPending, uid, rv))
	mustJournal(t, w.Observe(i, uid, rv))
}
func installAll(t *testing.T, w *InstallWriter) {
	t.Helper()
	for i := 0; i < 4; i++ {
		installStep(t, w, i)
	}
	mustJournal(t, w.Finish(time.Now()))
}
func installFixture(t *testing.T, p InstallPlan, extra ...frame) []byte {
	t.Helper()
	all := append([]frame{{Event: "install-start", ID: fixtureID, Install: &p}}, extra...)
	data := []byte(journalMagic)
	prev := ""
	for i, f := range all {
		if f.Version == 0 {
			f.Version = 2
		}
		if f.Sequence == 0 {
			f.Sequence = i + 1
		}
		if f.Previous == "" {
			f.Previous = prev
		}
		encoded, digest, err := encodeFrame(f)
		mustJournal(t, err)
		data = append(data, encoded...)
		prev = digest
	}
	return data
}

func TestHelmInstallJournalRoundTrip(t *testing.T) {
	s, dir := testStore(t)
	p := testInstallPlan()
	w := testInstallBegin(t, s, p)
	id := w.ID()
	p.Targets[0].Name = "changed"
	p.StorageName = "changed"
	r, err := s.RecoverInstall(id, false)
	mustJournal(t, err)
	snap := r.Snapshot()
	if snap.Version != 2 || snap.Action != "install" || snap.Plan.Binding != "" || snap.Install.Targets[0].Name != "first" {
		t.Fatal("wrong typed immutable start")
	}
	for _, e := range snap.Effects {
		if e.UID != "" || e.ResourceVersion != "" || e.ObservedResourceVersion != "" {
			t.Fatal("fabricated pre-create identity")
		}
	}
	snap.Install.Targets[0].Name = "mutated"
	snap.Effects[0].UID = "fake"
	if r.Snapshot().Install.Targets[0].Name != "first" || r.Snapshot().Effects[0].UID != "" {
		t.Fatal("recovery aliases returned projection")
	}
	mustJournal(t, installIntent(w, 0))
	snap, err = s.Load(id)
	mustJournal(t, err)
	if snap.Effects[0].Outcome != Unknown || snap.Effects[0].UID != "" {
		t.Fatal("intent must be uncertain without server identity")
	}
	mustJournal(t, w.Response(0, AcceptedPending, "actual-0", "10"))
	snap, err = s.Load(id)
	mustJournal(t, err)
	if snap.Effects[0].Outcome != AcceptedPending || snap.Effects[0].UID != "actual-0" {
		t.Fatal("response promoted completion or lost identity")
	}
	mustJournal(t, w.Observe(0, "actual-0", "10"))
	installStep(t, w, 1)
	installStep(t, w, 2)
	mustJournal(t, installIntent(w, 3))
	snap, err = s.Load(id)
	mustJournal(t, err)
	if e := snap.Effects[3]; e.UID != "actual-0" || e.ResourceVersion != "10" || e.Outcome != Unknown || e.ContentDigest != installDigest(3) {
		t.Fatal("deployment did not bind observed pending Secret and patch digest")
	}
	mustJournal(t, w.Response(3, AcceptedPending, "actual-0", "13"))
	mustJournal(t, w.Observe(3, "actual-0", "13"))
	mustJournal(t, w.Finish(time.Now()))
	original, err := os.ReadFile(filepath.Join(dir, id+".wal"))
	mustJournal(t, err)
	mustJournal(t, s.Close())
	other, err := OpenExisting(dir)
	mustJournal(t, err)
	defer other.Close()
	rr, err := other.RecoverInstall(id, false)
	mustJournal(t, err)
	done := rr.Snapshot()
	if !done.Closed || done.Corrupt || unresolved(done) || done.Effects[3].ObservedResourceVersion != "13" {
		t.Fatal("lost complete inert history")
	}
	after, err := os.ReadFile(filepath.Join(dir, id+".wal"))
	mustJournal(t, err)
	if !bytes.Equal(original, after) {
		t.Fatal("recovery wrote history")
	}
	if err := installIntent(w, 0); !errors.Is(err, ErrClosed) {
		t.Fatal("closed writer reused")
	}
}

func TestHelmInstallJournalSchema(t *testing.T) {
	changes := map[string]func(*InstallPlan){
		"binding": func(p *InstallPlan) { p.Binding = "credential" }, "candidate": func(p *InstallPlan) { p.CandidateID = "fake" },
		"preparation": func(p *InstallPlan) { p.PreparationID = "session" }, "namespace": func(p *InstallPlan) { p.Namespace = "../other" },
		"namespace-uid": func(p *InstallPlan) { p.NamespaceUID = "" }, "namespace-rv": func(p *InstallPlan) { p.NamespaceResourceVersion = "unsafe\n" },
		"release": func(p *InstallPlan) { p.Release = "UPPER" }, "storage-name": func(p *InstallPlan) { p.StorageName = "sh.helm.release.v1.example.v2" },
		"storage-digest": func(p *InstallPlan) { p.StorageDigest = "PRIVATE_PAYLOAD" }, "target-digest": func(p *InstallPlan) { p.Targets[0].Digest = "payload" },
		"target-name": func(p *InstallPlan) { p.Targets[0].Name = "a/b" }, "no-target": func(p *InstallPlan) { p.Targets = nil },
		"duplicates": func(p *InstallPlan) { p.Targets[1] = p.Targets[0] }, "too-many": func(p *InstallPlan) {
			for len(p.Targets) < 33 {
				p.Targets = append(p.Targets, InstallTarget{Name: fmt.Sprintf("t%d", len(p.Targets)), Digest: strings.Repeat("a", 64)})
			}
		},
		"zero-time": func(p *InstallPlan) { p.Started = time.Time{} }, "expiry": func(p *InstallPlan) { p.Expires = p.Started },
		"extended-expiry": func(p *InstallPlan) { p.Expires = p.Started.Add(time.Minute) }, "rv-budget": func(p *InstallPlan) { p.NamespaceResourceVersion = strings.Repeat("a", 257) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			s, _ := testStore(t)
			p := testInstallPlan()
			change(&p)
			if _, err := s.BeginInstall(p); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid plan got writer")
			}
			ids, err := s.List()
			mustJournal(t, err)
			if len(ids) != 0 {
				t.Fatal("invalid input allocated WAL")
			}
			if !parse(installFixture(t, p), fixtureID).snapshot.Corrupt {
				t.Fatal("malformed persisted plan accepted")
			}
		})
	}
}

func TestHelmInstallJournalTransitions(t *testing.T) {
	cases := map[string]func(*testing.T, *InstallWriter) error{
		"negative-index":         func(t *testing.T, w *InstallWriter) error { return installIntent(w, -1) },
		"out-of-range":           func(t *testing.T, w *InstallWriter) error { return installIntent(w, 4) },
		"out-of-order":           func(t *testing.T, w *InstallWriter) error { return installIntent(w, 1) },
		"wrong-request-digest":   func(t *testing.T, w *InstallWriter) error { return w.Intent(0, installDigest(1)) },
		"invalid-request-digest": func(t *testing.T, w *InstallWriter) error { return w.Intent(0, "PRIVATE_PAYLOAD") },
		"duplicate-intent": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			return installIntent(w, 0)
		},
		"response-without-intent": func(t *testing.T, w *InstallWriter) error { return w.Response(0, AcceptedPending, "actual-0", "10") },
		"completion-as-response": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			return w.Response(0, Completed, "actual-0", "10")
		},
		"observe-without-response": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			return w.Observe(0, "actual-0", "10")
		},
		"wrong-index": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			return w.Response(1, AcceptedPending, "actual-1", "10")
		},
		"missing-uid": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			return w.Response(0, AcceptedPending, "", "10")
		},
		"missing-rv": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			return w.Response(0, AcceptedPending, "actual-0", "")
		},
		"rejection-identity": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			return w.Response(0, Rejected, "actual-0", "10")
		},
		"unknown-identity": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			return w.Response(0, Unknown, "actual-0", "10")
		},
		"replay-unknown": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			mustJournal(t, w.Response(0, Unknown, "", ""))
			return installIntent(w, 0)
		},
		"clear-unknown": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			mustJournal(t, w.Response(0, Unknown, "", ""))
			return w.Observe(0, "actual-0", "10")
		},
		"replay-rejected": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			mustJournal(t, w.Response(0, Rejected, "", ""))
			return installIntent(w, 0)
		},
		"wrong-observed-uid": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			mustJournal(t, w.Response(0, AcceptedPending, "actual-0", "10"))
			return w.Observe(0, "replacement", "10")
		},
		"wrong-observed-rv": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			mustJournal(t, w.Response(0, AcceptedPending, "actual-0", "10"))
			return w.Observe(0, "actual-0", "11")
		},
		"response-twice": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			mustJournal(t, w.Response(0, AcceptedPending, "actual-0", "10"))
			return w.Response(0, AcceptedPending, "actual-0", "10")
		},
		"advance-pending": func(t *testing.T, w *InstallWriter) error {
			mustJournal(t, installIntent(w, 0))
			mustJournal(t, w.Response(0, AcceptedPending, "actual-0", "10"))
			return installIntent(w, 1)
		},
		"duplicate-object-uid": func(t *testing.T, w *InstallWriter) error {
			installStep(t, w, 0)
			mustJournal(t, installIntent(w, 1))
			return w.Response(1, AcceptedPending, "actual-0", "11")
		},
		"deploy-replacement": func(t *testing.T, w *InstallWriter) error {
			for i := 0; i < 3; i++ {
				installStep(t, w, i)
			}
			mustJournal(t, installIntent(w, 3))
			return w.Response(3, AcceptedPending, "replacement", "13")
		},
		"deploy-unchanged-rv": func(t *testing.T, w *InstallWriter) error {
			for i := 0; i < 3; i++ {
				installStep(t, w, i)
			}
			mustJournal(t, installIntent(w, 3))
			return w.Response(3, AcceptedPending, "actual-0", "10")
		},
		"finish-before-start": func(t *testing.T, w *InstallWriter) error { return w.Finish(time.Unix(1, 0)) },
		"legacy-intent":       func(t *testing.T, w *InstallWriter) error { return w.writer.Intent(0, "7") },
		"legacy-result":       func(t *testing.T, w *InstallWriter) error { return w.writer.Record(0, Completed, "8") },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := testStore(t)
			w := testInstallBegin(t, s, testInstallPlan())
			if err := run(t, w); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid transition accepted: %v", err)
			}
			snap, err := s.Load(w.ID())
			mustJournal(t, err)
			if snap.Corrupt {
				t.Fatal("rejected call damaged valid prefix")
			}
		})
	}
}

func TestHelmInstallJournalCheckedPrefix(t *testing.T) {
	p := testInstallPlan()
	start := installFixture(t, p)
	intent := frame{Event: "install-intent", Index: intPointer(0), RequestDigest: p.StorageDigest}
	response := frame{Event: "install-response", Index: intPointer(0), Outcome: AcceptedPending, UID: "actual-0", ObservedResourceVersion: "10"}
	good := installFixture(t, p, intent, response)
	prefix := installFixture(t, p, intent)
	for n := len(prefix); n < len(good); n++ {
		snap := parse(good[:n], fixtureID).snapshot
		if snap.Corrupt != (n != len(prefix)) || snap.Effects[0].Outcome != Unknown || snap.Effects[0].UID != "" {
			t.Fatalf("torn response at %d promoted facts", n)
		}
	}
	cases := map[string][]byte{
		"unknown-version": installFixture(t, p, frame{Version: 3, Event: "install-intent", Index: intPointer(0)}),
		"legacy-action":   installFixture(t, p, frame{Event: "intent", Index: intPointer(0), ResourceVersion: "7"}),
		"mixed-version":   installFixture(t, p, frame{Version: 1, Event: "intent", Index: intPointer(0), ResourceVersion: "7"}),
		"precreate-uid":   installFixture(t, p, frame{Event: "install-intent", Index: intPointer(0), RequestDigest: p.StorageDigest, UID: "dry-run"}),
		"precreate-rv":    installFixture(t, p, frame{Event: "install-intent", Index: intPointer(0), RequestDigest: p.StorageDigest, ResourceVersion: "dry-run"}),
		"extra-plan":      installFixture(t, p, frame{Event: "install-intent", Index: intPointer(0), Plan: func() *Plan { x := testPlan(); return &x }()}),
		"wrong-previous":  installFixture(t, p, frame{Event: "install-intent", Index: intPointer(0), Previous: strings.Repeat("f", 64)}),
		"concatenated":    append(bytes.Clone(good), start...),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			snap := parse(b, fixtureID).snapshot
			if !snap.Corrupt || snap.Version != 2 || snap.Install == nil {
				t.Fatal("invalid tail lost typed checked prefix")
			}
		})
	}
	f := frame{Version: 2, Sequence: 1, Event: "install-start", ID: fixtureID, Install: &p}
	raw, err := json.Marshal(f)
	mustJournal(t, err)
	for _, extra := range []string{`"credential":"PRIVATE_TOKEN",`, `"uid":"fabricated",`, `"version":2,`} {
		data := append([]byte(journalMagic), rawPayloadFrame(t, append([]byte("{"+extra), raw[1:]...))...)
		if !parse(data, fixtureID).snapshot.Corrupt {
			t.Fatal("unknown/duplicate/sensitive field accepted")
		}
	}
}

func TestHelmInstallJournalSharedAdmission(t *testing.T) {
	for _, mode := range []string{"install-to-install", "install-to-uninstall", "uninstall-to-install", "legacy-uid-only", "different-binding", "different-namespace", "different-release", "namespace-replaced", "legacy-different-uid"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := testStore(t)
			p := testInstallPlan()
			u := testPlan()
			if mode == "uninstall-to-install" || mode == "legacy-different-uid" {
				w := testBegin(t, s, u)
				mustJournal(t, w.Intent(0, "7"))
				mustJournal(t, w.Abort())
			} else {
				w := testInstallBegin(t, s, p)
				mustJournal(t, installIntent(w, 0))
				mustJournal(t, w.Abort())
			}
			blocked := true
			switch mode {
			case "different-binding":
				p.Binding = strings.Repeat("1", 64)
				blocked = false
			case "different-namespace":
				p.Namespace = "other"
				blocked = false
			case "different-release":
				p.Release = "other"
				p.StorageName = "sh.helm.release.v1.other.v1"
				blocked = false
			case "namespace-replaced":
				p.NamespaceUID = "replacement"
			case "legacy-different-uid":
				u.Revision.UID = "replacement"
				blocked = false
			}
			var err error
			if mode == "legacy-uid-only" {
				_, err = s.ReviewAdmission(context.Background(), AdmissionScope{Binding: p.Binding, Namespace: p.Namespace, StorageUID: "not-yet-known"})
			} else if mode == "install-to-uninstall" || mode == "legacy-different-uid" {
				w, e := s.Begin(u)
				err = e
				if w != nil {
					mustJournal(t, w.Abort())
				}
			} else {
				w, e := s.BeginInstall(p)
				err = e
				if w != nil {
					mustJournal(t, w.Abort())
				}
			}
			if blocked && !errors.Is(err, ErrUnresolved) || !blocked && err != nil {
				t.Fatalf("guard mismatch: %v", err)
			}
		})
	}
}

func TestHelmInstallJournalArchiveAndQuotas(t *testing.T) {
	for _, mode := range []string{"completed", "rejected-no-effect", "not-started", "unknown", "pending", "partial", "unclosed", "forged-archive", "record-limit"} {
		t.Run(mode, func(t *testing.T) {
			s, dir := testStore(t)
			w := testInstallBegin(t, s, testInstallPlan())
			switch mode {
			case "completed":
				installAll(t, w)
			case "rejected-no-effect":
				mustJournal(t, installIntent(w, 0))
				mustJournal(t, w.Response(0, Rejected, "", ""))
				mustJournal(t, w.Finish(time.Now()))
			case "not-started":
				mustJournal(t, w.Finish(time.Now()))
			case "unknown", "forged-archive":
				mustJournal(t, installIntent(w, 0))
				mustJournal(t, w.Finish(time.Now()))
			case "pending":
				mustJournal(t, installIntent(w, 0))
				mustJournal(t, w.Response(0, AcceptedPending, "actual-0", "10"))
				mustJournal(t, w.Finish(time.Now()))
			case "partial":
				installStep(t, w, 0)
				mustJournal(t, w.Finish(time.Now()))
			default:
				mustJournal(t, w.Abort())
			}
			if mode == "record-limit" {
				// Use valid, individually identified legacy records and the private
				// creator, so this tests shared quota rather than ACL/corruption.
				for i := 0; i < MaxRecords-1; i++ {
					id := fmt.Sprintf("%032x", i)
					admissionWriteFixture(t, s, id, archiveRecordBytes(t, id, testPlan()))
					if snap, err := s.Load(id); err != nil || !snap.Closed || snap.Version != 1 {
						t.Fatalf("invalid private quota fixture: %v", err)
					}
				}
				ids, err := s.List()
				mustJournal(t, err)
				if len(ids) != MaxRecords {
					t.Fatal("fixture did not fill the shared active count")
				}
				if _, err := s.BeginInstall(testInstallPlan()); !errors.Is(err, ErrBudget) {
					t.Fatalf("install did not reject the shared active count: %v", err)
				}
				return
			}
			if mode == "forged-archive" {
				mustJournal(t, os.Rename(filepath.Join(dir, w.ID()+".wal"), filepath.Join(dir, w.ID()+archivedSuffix)))
				if _, err := s.List(); !errors.Is(err, ErrCorrupt) {
					t.Fatal("uncertainty hidden in archive")
				}
				return
			}
			review, err := s.PrepareArchive(context.Background(), w.ID())
			eligible := mode == "completed" || mode == "rejected-no-effect" || mode == "not-started"
			if !eligible {
				if !errors.Is(err, ErrUnresolved) {
					t.Fatal("unresolved install archivable")
				}
				return
			}
			mustJournal(t, err)
			consent, err := review.Confirm(w.ID())
			mustJournal(t, err)
			before, err := os.ReadFile(filepath.Join(dir, w.ID()+".wal"))
			mustJournal(t, err)
			result, err := s.Archive(context.Background(), consent)
			mustJournal(t, err)
			if result.Outcome != ArchiveCompleted {
				t.Fatal("archive not completed")
			}
			after, err := os.ReadFile(filepath.Join(dir, w.ID()+archivedSuffix))
			mustJournal(t, err)
			if !bytes.Equal(before, after) {
				t.Fatal("archive changed v2 bytes")
			}
			recovery, err := s.RecoverInstall(w.ID(), true)
			mustJournal(t, err)
			if recovery.Snapshot().Action != "install" {
				t.Fatal("archive lost type")
			}
			inv, err := s.Inventory(context.Background())
			mustJournal(t, err)
			if inv.Records != 0 || inv.ArchivedRecords != 1 || inv.ArchivedBytes != int64(len(after)) {
				t.Fatal("archive accounting diverged")
			}
		})
	}
}

func TestHelmInstallJournalDurableFailures(t *testing.T) {
	for _, phase := range []string{"start", "intent", "response", "observation", "finish"} {
		for _, kind := range []string{"short-write", "write-error", "sync-error"} {
			t.Run(phase+"/"+kind, func(t *testing.T) {
				s, dir := testStore(t)
				var err error
				if phase == "start" {
					s.newSink = func(f *os.File) durableSink { return &faultSink{File: f, kind: kind} }
					_, err = s.BeginInstall(testInstallPlan())
				} else {
					w := testInstallBegin(t, s, testInstallPlan())
					if phase == "response" || phase == "observation" {
						mustJournal(t, installIntent(w, 0))
					}
					if phase == "observation" {
						mustJournal(t, w.Response(0, AcceptedPending, "actual-0", "10"))
					}
					w.writer.sink = &faultSink{File: w.writer.file, kind: kind}
					switch phase {
					case "intent":
						err = installIntent(w, 0)
					case "response":
						err = w.Response(0, AcceptedPending, "actual-0", "10")
					case "observation":
						err = w.Observe(0, "actual-0", "10")
					case "finish":
						err = w.Finish(time.Now())
					}
					if !s.poisoned {
						t.Fatal("failed durable storage left usable writer")
					}
					if next := installIntent(w, 1); !errors.Is(next, ErrUnavailable) {
						t.Fatal("retried after uncertain storage")
					}
				}
				if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "PRIVATE") {
					t.Fatal("storage failure or privacy contract lost")
				}
				entries, e := os.ReadDir(dir)
				mustJournal(t, e)
				if len(entries) != 2 {
					t.Fatal("failed write retried/removed record")
				}
			})
		}
	}
	t.Run("directory-sync", func(t *testing.T) {
		s, _ := testStore(t)
		s.syncRoot = func() error { return errors.New("PRIVATE") }
		if _, err := s.BeginInstall(testInstallPlan()); !errors.Is(err, ErrUnavailable) {
			t.Fatal("ignored directory sync")
		}
	})
}

func TestHelmInstallJournalConcurrentLease(t *testing.T) {
	s, _ := testStore(t)
	w := testInstallBegin(t, s, testInstallPlan())
	copyWriter := *w
	copyStore := *s
	if _, err := copyStore.Begin(testPlan()); !errors.Is(err, ErrBusy) {
		t.Fatal("v1 writer bypassed v2 lease")
	}
	if err := s.Close(); !errors.Is(err, ErrBusy) {
		t.Fatal("store closed under live v2 writer")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, x := range []*InstallWriter{w, &copyWriter} {
		wg.Add(1)
		go func(x *InstallWriter) { defer wg.Done(); results <- installIntent(x, 0) }(x)
	}
	wg.Wait()
	close(results)
	ok := 0
	for err := range results {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if ok != 1 {
		t.Fatal("copies duplicated intent")
	}
	mustJournal(t, w.Abort())
	mustJournal(t, copyWriter.Abort())
}

func TestHelmInstallJournalLegacyEncodingAndWrongRecovery(t *testing.T) {
	p := testPlan()
	old := struct {
		Version  int    `json:"version"`
		Sequence int    `json:"sequence"`
		Previous string `json:"previous"`
		Event    string `json:"event"`
		ID       string `json:"id,omitempty"`
		Plan     *Plan  `json:"plan,omitempty"`
	}{1, 1, "", "start", fixtureID, &p}
	want, err := json.Marshal(old)
	mustJournal(t, err)
	got, err := json.Marshal(frame{Version: 1, Sequence: 1, Event: "start", ID: fixtureID, Plan: &p})
	mustJournal(t, err)
	if !bytes.Equal(got, want) {
		t.Fatal("version 1 canonical bytes changed")
	}
	s, _ := testStore(t)
	w := testBegin(t, s, p)
	mustJournal(t, w.Finish(time.Now()))
	if _, err := s.RecoverInstall(w.ID(), false); !errors.Is(err, ErrInvalid) {
		t.Fatal("legacy record reinterpreted as install")
	}
	iw := testInstallBegin(t, s, testInstallPlan())
	snap, err := s.Load(iw.ID())
	mustJournal(t, err)
	if !reflect.DeepEqual(snap.Plan, Plan{}) {
		t.Fatal("install populated legacy uninstall identity")
	}
	var nilWriter *InstallWriter
	if nilWriter.ID() != "" || !errors.Is(installIntent(nilWriter, 0), ErrClosed) || nilWriter.Abort() != nil {
		t.Fatal("nil handles unsafe")
	}
}
