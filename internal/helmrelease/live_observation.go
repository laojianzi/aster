package helmrelease

import (
	"errors"
	"sort"
	"time"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/util/validation"
)

const LiveLifetime = 30 * time.Second

var (
	ErrLiveConsent     = errors.New("Live Helm observations require separate metadata-read consent.")
	ErrLiveStale       = errors.New("The selected Helm revision is no longer the exact latest stored revision; refresh and preview again.")
	ErrLiveExpired     = errors.New("The stored Helm preview expired or changed; explicitly preview again.")
	ErrLiveRouting     = errors.New("Live Helm resource routing is unsupported or ambiguous; no partial observation is shown.")
	ErrLiveInvalid     = errors.New("Live Helm metadata is malformed or inconsistent; no partial observation is shown.")
	ErrLiveForbidden   = errors.New("Live Helm metadata is unavailable to the current identity; no partial observation is shown.")
	ErrLiveUnsupported = errors.New("The server did not provide metadata-only objects; no full-object fallback was attempted.")
	ErrLiveBudget      = errors.New("Live Helm metadata exceeded the bounded read budget; no partial observation is shown.")
	ErrLiveUnavailable = errors.New("Live Helm metadata could not be read; no retry or write was attempted.")
)

// LiveItem is a sampled identity, never an executable target or proof that it is
// the object originally installed by Helm. UID/RV and markers may change as soon
// as the GET completes. No arbitrary label, annotation or object body survives.
type LiveItem struct {
	Stored                          UninstallItem
	Resource, Namespace             string
	UID, ResourceVersion            string
	State                           string // present, absent, or outside-namespace (not requested)
	Markers                         string // match, missing, mismatch, or empty for non-present objects
	Terminating, Finalizers, Owners bool
	Keep, Hook                      bool
	ObservedAt                      time.Time
}

// LiveObservation is read-only and expires no later than its original preview.
// Two revision snapshots are not a transaction over the intervening object GETs.
type LiveObservation struct {
	preview                    *UninstallPreview
	items                      []LiveItem
	started, finished, expires time.Time
}

func (o *LiveObservation) Items() []LiveItem {
	if o == nil {
		return nil
	}
	return append([]LiveItem(nil), o.items...)
}
func (o *LiveObservation) ExpiresAt() time.Time {
	if o == nil {
		return time.Time{}
	}
	return o.expires
}
func (o *LiveObservation) Matches(p *UninstallPreview, session, ns string, row Revision, now time.Time) bool {
	return o != nil && o.preview == p && p.Matches(session, ns, row, now) && !now.Before(o.finished) && now.Before(o.expires)
}

// LatestRevisionSet validates the complete namespace metadata projection and
// returns only this release's history. Compare revision numbers numerically but
// treat Kubernetes resourceVersion as opaque; collection RVs need not match
// between independent reads (unrelated objects can advance them).
func LatestRevisionSet(s Snapshot, ns string, selected Revision) ([]Revision, error) {
	if !ValidSelection(ns, selected) || s.Namespace != ns || !safeMetadataID(s.ResourceVersion, 256) || s.ObservedAt.IsZero() || len(s.Revisions) > MaxRevisions {
		return nil, ErrLiveInvalid
	}
	seenNames, seenUIDs := map[string]bool{}, map[string]bool{}
	rows := []Revision{}
	found := false
	for _, r := range s.Revisions {
		if !ValidSelection(ns, r) || !safeMetadataID(r.UID, 128) || !safeMetadataID(r.ResourceVersion, 256) || seenNames[r.StorageName] || seenUIDs[r.UID] {
			return nil, ErrLiveInvalid
		}
		seenNames[r.StorageName], seenUIDs[r.UID] = true, true
		if r.Release != selected.Release {
			continue
		}
		if r.Number > selected.Number {
			return nil, ErrLiveStale
		}
		if r == selected {
			found = true
		}
		rows = append(rows, r)
	}
	if !found {
		return nil, ErrLiveStale
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Number < rows[j].Number })
	return rows, nil
}

// FinishLiveObservation fails closed on *any* history change or projection
// inconsistency. Inputs and accessors are copied, never retained as aliases.
func FinishLiveObservation(p *UninstallPreview, session string, before, after Snapshot, items []LiveItem, started, finished time.Time) (*LiveObservation, error) {
	if p == nil || !p.Matches(session, p.namespace, p.revision, started) || !p.Matches(session, p.namespace, p.revision, finished) || finished.Before(started) {
		return nil, ErrLiveExpired
	}
	if before.ObservedAt.Before(started) || after.ObservedAt.Before(before.ObservedAt) || after.ObservedAt.After(finished) {
		return nil, ErrLiveInvalid
	}
	a, err := LatestRevisionSet(before, p.namespace, p.revision)
	if err != nil {
		return nil, err
	}
	b, err := LatestRevisionSet(after, p.namespace, p.revision)
	if err != nil {
		return nil, err
	}
	if len(a) != len(b) {
		return nil, ErrLiveStale
	}
	for i := range a {
		if a[i] != b[i] {
			return nil, ErrLiveStale
		}
	}
	if len(items) != len(p.items) {
		return nil, ErrLiveInvalid
	}
	seen := map[string]bool{}
	for i, r := range items {
		if r.Stored != p.items[i] || r.ObservedAt.Before(started) || r.ObservedAt.After(finished) {
			return nil, ErrLiveInvalid
		}
		if r.Stored.ForeignNamespace {
			if r.State != "outside-namespace" || r.Resource != "" || r.Namespace != "" || r.UID != "" || r.ResourceVersion != "" || r.Markers != "" || r.Terminating || r.Finalizers || r.Owners || r.Keep || r.Hook {
				return nil, ErrLiveInvalid
			}
			continue
		}
		if len(validation.IsDNS1123Label(r.Resource)) != 0 || r.Resource == "" || (r.Namespace != "" && r.Namespace != p.namespace) {
			return nil, ErrLiveInvalid
		}
		// GVR aliases are rejected by the transport; preserve uniqueness in projection.
		key := r.Stored.APIVersion + "\x00" + r.Resource + "\x00" + r.Namespace + "\x00" + r.Stored.Name
		if seen[key] {
			return nil, ErrLiveInvalid
		}
		seen[key] = true
		switch r.State {
		case "present":
			if !safeMetadataID(r.UID, 128) || !safeMetadataID(r.ResourceVersion, 256) || (r.Markers != "match" && r.Markers != "missing" && r.Markers != "mismatch") {
				return nil, ErrLiveInvalid
			}
		case "absent":
			if r.UID != "" || r.ResourceVersion != "" || r.Markers != "" || r.Terminating || r.Finalizers || r.Owners || r.Keep || r.Hook {
				return nil, ErrLiveInvalid
			}
		default:
			return nil, ErrLiveInvalid
		}
	}
	expires := finished.Add(LiveLifetime)
	if p.expires.Before(expires) {
		expires = p.expires
	}
	return &LiveObservation{preview: p, items: append([]LiveItem(nil), items...), started: started, finished: finished, expires: expires}, nil
}

func safeMetadataID(s string, n int) bool {
	if s == "" || len(s) > n || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
