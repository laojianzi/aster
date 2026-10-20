// Package helmjournal records the bounded identities and ordered effects of one
// restricted Helm uninstall. Records are inert history, never credentials,
// prepared operations, authority, an automatic resume token, or an audit service.
package helmjournal

import (
	"errors"
	"strings"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	MaxFrameBytes     = 64 << 10
	MaxOperationBytes = 256 << 10
	MaxStoreBytes     = 16 << 20
	MaxRecords        = 128
	IntegrityValid    = "Valid"
	IntegrityCorrupt  = "Corrupt"
	NotStarted        = "NotStarted"
	AcceptedPending   = "AcceptedPending"
	Rejected          = "Rejected"
	Unknown           = "Unknown"
	Completed         = "Completed"
)

var (
	ErrInvalid     = errors.New("invalid Helm recovery journal input")
	ErrUnavailable = errors.New("private Helm recovery journal unavailable; execution stopped")
	ErrBusy        = errors.New("Helm recovery journal is in use")
	ErrClosed      = errors.New("Helm recovery journal is closed")
	ErrCorrupt     = errors.New("Helm recovery journal integrity is uncertain; no recovery action is authorized")
	ErrUnresolved  = errors.New("this Helm release has an unresolved journal; independently reconcile before a new execution")
	ErrBudget      = errors.New("Helm recovery journal exceeded its storage budget")
)

// Plan contains only public identities. Binding is a purpose-bound digest, not
// proof of cluster identity. No session, transport, request body or secret belongs
// in this format. A recovered Plan must never mint a consent.
type Plan struct {
	Binding       string                        `json:"binding"`
	PreparationID string                        `json:"preparationID"`
	Namespace     string                        `json:"namespace"`
	Revision      helmrelease.Revision          `json:"revision"`
	Targets       []helmrelease.UninstallTarget `json:"targets"`
	Started       time.Time                     `json:"started"`
	Expires       time.Time                     `json:"expires"`
}

type Effect struct {
	Kind                    string
	Namespace               string
	Name                    string
	UID                     string
	ResourceVersion         string
	ObservedResourceVersion string
	Outcome                 string
}

// Snapshot is a defensive copy. Closed means a terminal frame was stored; it
// does not imply server success. Unknown/Pending outcomes remain unresolved even
// in a Closed attempt. Corrupt snapshots preserve only the checked prefix and
// must not be used to authorize a network observation or mutation.
type Snapshot struct {
	ID        string
	Plan      Plan
	Effects   []Effect
	Closed    bool
	Finished  time.Time
	Integrity string
	Corrupt   bool
	Digest    string
}

func clonePlan(p Plan) Plan {
	p.Targets = append([]helmrelease.UninstallTarget(nil), p.Targets...)
	return p
}

func cloneSnapshot(s Snapshot) Snapshot {
	s.Plan = clonePlan(s.Plan)
	s.Effects = append([]Effect(nil), s.Effects...)
	return s
}

func validHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validIdentity(s string, n int) bool {
	if len(s) == 0 || len(s) > n {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func validTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 1970 && t.Year() <= 9999 }

func validPlan(p Plan) bool {
	if !validHex(p.Binding, 64) || !validHex(p.PreparationID, 32) || !helmrelease.ValidSelection(p.Namespace, p.Revision) ||
		p.Revision.Status != "deployed" || !validIdentity(p.Revision.UID, 128) || !validIdentity(p.Revision.ResourceVersion, 256) ||
		!validTime(p.Started) || !validTime(p.Expires) || !p.Expires.After(p.Started) || p.Expires.Sub(p.Started) > helmrelease.PreparationLifetime ||
		len(p.Targets) < 1 || len(p.Targets) > 32 {
		return false
	}
	seen := make(map[string]bool, len(p.Targets))
	seenUID := make(map[string]bool, len(p.Targets))
	for _, t := range p.Targets {
		if t.APIVersion != "v1" || t.Kind != "ConfigMap" || t.Resource != "configmaps" || t.Namespace != p.Namespace ||
			len(t.Name) == 0 || len(t.Name) > 253 || len(validation.IsDNS1123Subdomain(t.Name)) != 0 ||
			!validIdentity(t.UID, 128) || !validIdentity(t.ResourceVersion, 256) || seen[t.Name] || seenUID[t.UID] {
			return false
		}
		seen[t.Name], seenUID[t.UID] = true, true
	}
	return true
}

func initialSnapshot(id string, p Plan) Snapshot {
	p = clonePlan(p)
	s := Snapshot{ID: id, Plan: p, Integrity: IntegrityValid}
	e := Effect{Kind: "begin", Namespace: p.Namespace, Name: p.Revision.StorageName, UID: p.Revision.UID,
		ResourceVersion: p.Revision.ResourceVersion, Outcome: NotStarted}
	s.Effects = append(s.Effects, e)
	for _, t := range p.Targets {
		s.Effects = append(s.Effects, Effect{Kind: "delete", Namespace: t.Namespace, Name: t.Name,
			UID: t.UID, ResourceVersion: t.ResourceVersion, Outcome: NotStarted})
	}
	e.Kind, e.ResourceVersion = "finish", ""
	s.Effects = append(s.Effects, e)
	return s
}

func unresolved(s Snapshot) bool {
	blocked, _ := explainUnresolved(s)
	return blocked
}

func explainUnresolved(s Snapshot) (bool, UnresolvedKind) {
	// A corrupt prefix cannot be explained as a complete historical outcome.
	// Admission rejects its load before using this classification.
	if s.Corrupt {
		return true, ""
	}
	if !s.Closed {
		return true, UnresolvedUnclosed
	}
	any := false
	all := len(s.Effects) > 0
	unknown, pending := false, false
	for _, e := range s.Effects {
		any = any || e.Outcome == Completed || e.Outcome == AcceptedPending || e.Outcome == Unknown
		all = all && e.Outcome == Completed
		unknown = unknown || e.Outcome == Unknown
		pending = pending || e.Outcome == AcceptedPending
	}
	// A complete success or a confirmed attempt with no accepted/unknown effect
	// is historical. Partial, pending and unknown effects cannot be bypassed.
	if !any || all {
		return false, ""
	}
	if unknown {
		return true, UnresolvedUnknown
	}
	if pending {
		return true, UnresolvedPending
	}
	return true, UnresolvedPartial
}

func recordID(name string) (string, bool) {
	id, ok := strings.CutSuffix(name, ".wal")
	return id, ok && validHex(id, 32)
}
