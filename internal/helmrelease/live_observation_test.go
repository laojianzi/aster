package helmrelease

import (
	"errors"
	"testing"
	"time"
)

func liveTestPreview() (*UninstallPreview, Snapshot, []LiveItem, time.Time) {
	now := time.Now()
	r := Revision{Release: "example", Number: 2, Status: "deployed", StorageName: "sh.helm.release.v1.example.v2", UID: "uid2", ResourceVersion: "7"}
	item := UninstallItem{APIVersion: "v1", Kind: "ConfigMap", Name: "sample"}
	p := &UninstallPreview{id: "opaque", session: "session", namespace: "team", revision: r, created: now.Add(-time.Second), expires: now.Add(PreviewLifetime), items: []UninstallItem{item}}
	s := Snapshot{Namespace: "team", ResourceVersion: "opaque/a", ObservedAt: now, Revisions: []Revision{r}}
	rows := []LiveItem{{Stored: item, Resource: "configmaps", Namespace: "team", UID: "live-uid", ResourceVersion: "opaque-z", State: "present", Markers: "match", ObservedAt: now}}
	return p, s, rows, now
}
func TestHelmLiveLatestRevisionConsistency(t *testing.T) {
	for _, mode := range []string{"same", "newer", "replaced", "changed-status", "missing", "duplicate", "wrong-namespace", "invalid-rv", "unrelated", "lexical", "deleted-history", "changed-history", "reordered-history"} {
		t.Run(mode, func(t *testing.T) {
			p, a, rows, now := liveTestPreview()
			b := a
			b.Revisions = append([]Revision(nil), a.Revisions...)
			old := p.revision
			old.Number = 1
			old.StorageName = "sh.helm.release.v1.example.v1"
			old.UID = "uid1"
			old.Status = "superseded"
			a.Revisions = append(a.Revisions, old)
			b.Revisions = append(b.Revisions, old)
			switch mode {
			case "newer":
				r := p.revision
				r.Number = 3
				r.StorageName = "sh.helm.release.v1.example.v3"
				r.UID = "uid3"
				b.Revisions = append(b.Revisions, r)
			case "replaced":
				b.Revisions[0].UID = "replacement"
			case "changed-status":
				b.Revisions[0].Status = "uninstalling"
			case "missing":
				b.Revisions = b.Revisions[1:]
			case "duplicate":
				b.Revisions = append(b.Revisions, b.Revisions[0])
			case "wrong-namespace":
				b.Namespace = "other"
			case "invalid-rv":
				b.Revisions[0].ResourceVersion = "private\n"
			case "unrelated":
				b.ResourceVersion = "completely-different"
				r := old
				r.Release = "other"
				r.StorageName = "sh.helm.release.v1.other.v1"
				r.UID = "other"
				b.Revisions = append(b.Revisions, r)
			case "lexical":
				p.revision.Number = 10
				p.revision.StorageName = "sh.helm.release.v1.example.v10"
				a.Revisions[0] = p.revision
				b.Revisions[0] = p.revision
				old.Number = 9
				old.StorageName = "sh.helm.release.v1.example.v9"
				a.Revisions[1] = old
				b.Revisions[1] = old
			case "deleted-history":
				b.Revisions = b.Revisions[:1]
			case "changed-history":
				b.Revisions[1].ResourceVersion = "different"
			case "reordered-history":
				b.Revisions[0], b.Revisions[1] = b.Revisions[1], b.Revisions[0]
			}
			o, e := FinishLiveObservation(p, "session", a, b, rows, now, now)
			good := mode == "same" || mode == "unrelated" || mode == "lexical" || mode == "reordered-history"
			if good {
				if e != nil || o == nil {
					t.Fatal(e)
				}
			} else if e == nil || o != nil {
				t.Fatal("inconsistent history accepted")
			}
		})
	}
}
func TestHelmLiveObservationImmutableExpiry(t *testing.T) {
	p, s, rows, now := liveTestPreview()
	o, e := FinishLiveObservation(p, "session", s, s, rows, now, now)
	if e != nil || !o.Matches(p, "session", "team", p.revision, now) {
		t.Fatal(e)
	}
	rows[0].UID = "changed"
	copy := o.Items()
	copy[0].State = "absent"
	if o.Items()[0].UID != "live-uid" || o.Items()[0].State != "present" {
		t.Fatal("aliased result")
	}
	for _, when := range []time.Time{now.Add(-time.Nanosecond), o.ExpiresAt(), p.ExpiresAt()} {
		if o.Matches(p, "session", "team", p.revision, when) {
			t.Fatal("invalid lifetime")
		}
	}
	if o.Matches(p, "other", "team", p.revision, now) || o.Matches(p, "session", "other", p.revision, now) {
		t.Fatal("scope escaped")
	}
	clone := *p
	if o.Matches(&clone, "session", "team", p.revision, now) {
		t.Fatal("different preview accepted")
	}
	p.expires = now.Add(time.Second)
	o, e = FinishLiveObservation(p, "session", s, s, o.Items(), now, now)
	if e != nil || !o.ExpiresAt().Equal(p.expires) {
		t.Fatal("extended source expiry", e)
	}
	for _, mode := range []string{"session", "expired", "backwards", "missing-row", "wrong-item", "bad-state", "bad-marker", "control-uid", "future-row", "absent-has-uid", "foreign-has-data"} {
		t.Run(mode, func(t *testing.T) {
			p, s, rows, now := liveTestPreview()
			session := "session"
			end := now
			switch mode {
			case "session":
				session = "other"
			case "expired":
				end = p.expires
			case "backwards":
				end = now.Add(-time.Second)
			case "missing-row":
				rows = nil
			case "wrong-item":
				rows[0].Stored.Name = "other"
			case "bad-state":
				rows[0].State = "safe-to-delete"
			case "bad-marker":
				rows[0].Markers = "private"
			case "control-uid":
				rows[0].UID = "a\nb"
			case "future-row":
				rows[0].ObservedAt = now.Add(time.Second)
			case "absent-has-uid":
				rows[0].State = "absent"
			case "foreign-has-data":
				p.items[0].ForeignNamespace = true
				rows[0].Stored = p.items[0]
				rows[0].State = "outside-namespace"
			}
			if r, err := FinishLiveObservation(p, session, s, s, rows, now, end); r != nil || err == nil {
				t.Fatal("invalid projection accepted")
			}
		})
	}
	var absent *LiveObservation
	if absent.Items() != nil || !absent.ExpiresAt().IsZero() || absent.Matches(nil, "", "", Revision{}, now) {
		t.Fatal("nil receiver")
	}
	if _, e = LatestRevisionSet(Snapshot{}, "team", p.revision); !errors.Is(e, ErrLiveInvalid) {
		t.Fatal(e)
	}
}
