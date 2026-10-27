package helmjournal

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	strictjson "sigs.k8s.io/json"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHelmInstallJournalDowngradeAndMixedVersion(t *testing.T) {
	p := testInstallPlan()
	data := installFixture(t, p)
	payload := data[len(journalMagic)+4:]
	var e envelope
	mustJournal(t, json.Unmarshal(payload, &e))
	// Freeze the old decoder shape and strict mode; a downgrade cannot silently
	// drop install identities or treat version 2 as an empty legacy operation.
	var legacy struct {
		Version                 int        `json:"version"`
		Sequence                int        `json:"sequence"`
		Previous                string     `json:"previous"`
		Event                   string     `json:"event"`
		ID                      string     `json:"id,omitempty"`
		Plan                    *Plan      `json:"plan,omitempty"`
		Index                   *int       `json:"index,omitempty"`
		ResourceVersion         string     `json:"resourceVersion,omitempty"`
		Outcome                 string     `json:"outcome,omitempty"`
		ObservedResourceVersion string     `json:"observedResourceVersion,omitempty"`
		Finished                *time.Time `json:"finished,omitempty"`
	}
	issues, err := strictjson.UnmarshalStrict(e.Payload, &legacy)
	if err == nil && len(issues) == 0 && legacy.Version == 1 {
		t.Fatal("old strict payload decoder accepted install")
	}
	old := testPlan()
	mixed := fixtureFrames(t, old, frame{Version: 2, Event: "install-intent", Index: intPointer(0), RequestDigest: p.StorageDigest})
	snap := parse(mixed, fixtureID).snapshot
	if !snap.Corrupt || snap.Version != 1 || snap.Install != nil {
		t.Fatal("v1 prefix reinterpreted after mixed v2 tail")
	}
	// A well-checksummed unknown first version has no usable plan.
	f := frame{Version: 3, Sequence: 1, Event: "install-start", ID: fixtureID, Install: &p}
	raw, _, err := encodeFrame(f)
	mustJournal(t, err)
	if snap := parse(append([]byte(journalMagic), raw...), fixtureID).snapshot; !snap.Corrupt || snap.Install != nil {
		t.Fatal("unknown version exposed plan")
	}
	if binary.BigEndian.Uint32(data[len(journalMagic):]) == 0 {
		t.Fatal("missing framing")
	}
}

func TestHelmInstallJournalFileIntegrity(t *testing.T) {
	for _, mode := range []string{"prefix-rewrite", "file-replacement", "lock-replacement", "lock-content", "corrupt-unrelated"} {
		t.Run(mode, func(t *testing.T) {
			s, dir := testStore(t)
			w := testInstallBegin(t, s, testInstallPlan())
			path := filepath.Join(dir, w.ID()+".wal")
			raw, err := os.ReadFile(path)
			mustJournal(t, err)
			switch mode {
			case "prefix-rewrite":
				raw[len(raw)-5] ^= 1
				mustJournal(t, os.WriteFile(path, raw, 0600))
			case "file-replacement", "lock-replacement":
				name, held := path, w.raw().file
				if mode == "lock-replacement" {
					name, held = filepath.Join(dir, lockName), s.lock
				}
				// Capture identity through the held handle before attempting rename.
				identity, err := held.Stat()
				mustJournal(t, err)
				moved := name + ".saved"
				if err := os.Rename(name, moved); err != nil {
					// ERROR_SHARING_VIOLATION is an expected Windows protection,
					// not permission to ignore arbitrary rename failures elsewhere.
					if runtime.GOOS != "windows" || !errors.Is(err, syscall.Errno(32)) {
						t.Fatalf("unexpected rename failure: %v", err)
					}
					assertInstallRenamePrevented(t, s, w, name, moved, identity, raw)
					return
				}
				f, err := s.root.openFile(filepath.Base(name), true, true)
				mustJournal(t, err)
				if mode == "file-replacement" {
					_, err = f.Write(raw)
					mustJournal(t, err)
				}
				mustJournal(t, f.Close())
			case "lock-content":
				f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_WRONLY, 0600)
				mustJournal(t, err)
				// Byte zero is locked on Windows; alteration outside it must still
				// invalidate the permanent lock and forbid an install intent.
				_, err = f.WriteAt([]byte{'!'}, 1)
				mustJournal(t, err)
				mustJournal(t, f.Close())
			case "corrupt-unrelated":
				mustJournal(t, w.Abort())
				id := strings.Repeat("1", 32)
				admissionWriteFixture(t, s, id, []byte("damaged"))
				if snap, err := s.Load(id); !errors.Is(err, ErrCorrupt) || !snap.Corrupt {
					t.Fatalf("fixture did not reach record parsing: %v", err)
				}
				p := testInstallPlan()
				p.Release = "other"
				p.StorageName = "sh.helm.release.v1.other.v1"
				if _, err := s.BeginInstall(p); !errors.Is(err, ErrCorrupt) {
					t.Fatalf("unrelated corruption not detected at the parsing boundary: %v", err)
				}
				return
			}
			before, err := os.ReadFile(path)
			mustJournal(t, err)
			if err := installIntent(w, 0); !errors.Is(err, ErrUnavailable) {
				t.Fatal("changed journal identity/content accepted")
			}
			after, err := os.ReadFile(path)
			mustJournal(t, err)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected intent altered the WAL")
			}
		})
	}
}

func TestHelmInstallJournalMaxTargetsAndCancellation(t *testing.T) {
	s, dir := testStore(t)
	p := testInstallPlan()
	p.Targets = nil
	for i := 0; i < 32; i++ {
		p.Targets = append(p.Targets, InstallTarget{Name: fmt.Sprintf("item-%02d", i), Digest: strings.Repeat("e", 64)})
	}
	w := testInstallBegin(t, s, p)
	for i := 0; i < 34; i++ {
		digest, uid, rv := strings.Repeat("e", 64), fmt.Sprintf("actual-%d", i), fmt.Sprintf("%d", 100+i)
		if i == 0 {
			digest = p.StorageDigest
		}
		if i == 33 {
			uid = "actual-0"
			digest = strings.Repeat("f", 64)
		}
		mustJournal(t, w.Intent(i, digest))
		mustJournal(t, w.Response(i, AcceptedPending, uid, rv))
		mustJournal(t, w.Observe(i, uid, rv))
	}
	mustJournal(t, w.Finish(time.Now()))
	snap, err := s.Load(w.ID())
	mustJournal(t, err)
	if unresolved(snap) || len(snap.Effects) != 34 {
		t.Fatal("max profile not fully representable")
	}
	path := filepath.Join(dir, w.ID()+".wal")
	before, err := os.ReadFile(path)
	mustJournal(t, err)
	if len(before) > MaxOperationBytes {
		t.Fatal("full profile exceeded reserved bytes")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := s.ReviewAdmission(ctx, AdmissionScope{Binding: p.Binding, Namespace: p.Namespace, Release: p.Release})
	if !errors.Is(err, context.Canceled) || report.Reason != AdmissionCancelled {
		t.Fatal("cancelled local admission succeeded")
	}
	after, err := os.ReadFile(path)
	mustJournal(t, err)
	if !bytes.Equal(before, after) {
		t.Fatal("cancelled review changed history")
	}
}

func TestHelmInstallJournalLegacyReleaseScope(t *testing.T) {
	s, _ := testStore(t)
	p := testPlan()
	p.Revision.Release = "legacy.name"
	p.Revision.Number = 1
	p.Revision.StorageName = "sh.helm.release.v1.legacy.name.v1"
	q := AdmissionScope{Binding: p.Binding, Namespace: p.Namespace, StorageUID: p.Revision.UID, Release: p.Revision.Release}
	if !validPlan(p) {
		t.Fatal("fixture must be supported by existing uninstall selection")
	}
	report, err := s.ReviewAdmission(context.Background(), q)
	if err != nil || report.Reason != AdmissionNoBlocker {
		t.Fatalf("legacy dotted release incorrectly rejected: %s %v", report.Reason, err)
	}
	w, err := s.Begin(p)
	mustJournal(t, err)
	mustJournal(t, w.Finish(time.Now()))
	report, err = s.ReviewAdmission(context.Background(), q)
	if err != nil || report.Reason != AdmissionNoBlocker {
		t.Fatal("closed legacy history changed release validation")
	}
	// The install capture deliberately admits labels only. Preserving a legacy
	// uninstall name must not broaden this new-release profile.
	i := testInstallPlan()
	i.Release, i.StorageName = p.Revision.Release, p.Revision.StorageName
	if _, err := s.BeginInstall(i); !errors.Is(err, ErrInvalid) {
		t.Fatal("legacy name broadened install schema")
	}
	q.StorageUID = ""
	if _, err := s.ReviewAdmission(context.Background(), q); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid pre-create release scope accepted")
	}
}

// A denied replacement must preserve the path, held identity, complete original
// bytes and exclusive store lock. Only then may the original writer continue.
func assertInstallRenamePrevented(t *testing.T, s *Store, w *InstallWriter, name, moved string, identity os.FileInfo, original []byte) {
	t.Helper()
	after, err := os.Stat(name)
	mustJournal(t, err)
	if !os.SameFile(identity, after) {
		t.Fatal("denied rename changed the original file identity")
	}
	if _, err := os.Stat(moved); !os.IsNotExist(err) {
		t.Fatal("denied rename left a destination")
	}
	path := filepath.Join(s.root.path, w.ID()+".wal")
	data, err := os.ReadFile(path)
	mustJournal(t, err)
	if !bytes.Equal(data, original) {
		t.Fatal("denied rename changed the WAL")
	}
	mustJournal(t, s.root.checkFile(lockName, s.lock))
	second, err := Open(s.root.path)
	if second != nil {
		mustJournal(t, second.Close())
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("denied rename lost store exclusion: %v", err)
	}
	mustJournal(t, installIntent(w, 0))
	snap, err := s.Load(w.ID())
	mustJournal(t, err)
	if snap.Version != 2 || snap.Effects[0].Outcome != Unknown {
		t.Fatal("original writer did not preserve the unknown install intent")
	}
	data, err = os.ReadFile(path)
	mustJournal(t, err)
	if len(data) <= len(original) || !bytes.HasPrefix(data, original) {
		t.Fatal("continued writer replaced or lost the original WAL prefix")
	}
}
