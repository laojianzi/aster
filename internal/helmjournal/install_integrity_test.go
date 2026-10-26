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
	strictjson "sigs.k8s.io/json"
	"strings"
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
	for _, mode := range []string{"prefix-rewrite", "file-replacement", "lock-replacement", "corrupt-unrelated"} {
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
			case "file-replacement":
				mustJournal(t, os.Rename(path, path+".saved"))
				mustJournal(t, os.WriteFile(path, raw, 0600))
			case "lock-replacement":
				mustJournal(t, os.Rename(filepath.Join(dir, lockName), filepath.Join(dir, "old.lock")))
				mustJournal(t, os.WriteFile(filepath.Join(dir, lockName), nil, 0600))
			case "corrupt-unrelated":
				mustJournal(t, w.Abort())
				mustJournal(t, os.WriteFile(filepath.Join(dir, strings.Repeat("1", 32)+".wal"), []byte("damaged"), 0600))
				p := testInstallPlan()
				p.Release = "other"
				p.StorageName = "sh.helm.release.v1.other.v1"
				if _, err := s.BeginInstall(p); !errors.Is(err, ErrCorrupt) {
					t.Fatal("unrelated corruption bypassed")
				}
				return
			}
			if err := installIntent(w, 0); !errors.Is(err, ErrUnavailable) {
				t.Fatal("changed journal identity/content accepted")
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
