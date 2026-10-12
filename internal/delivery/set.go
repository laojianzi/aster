// Package delivery prepares bounded manifest sets through the existing reviewed
// operation service. A Set owns input, not execution progress: its caller must
// enforce ordered explicit review, confirmation, invalidation and reconciliation.
package delivery

import "github.com/laojianzi/aster/internal/kube"

const (
	MaxBytes    = 2 << 20
	MaxObjects  = 32
	MaxNewlines = 160000
	MaxSegments = 4096
	MaxKinds    = 8192
)

// Item is a resolved, namespaced target. It contains no manifest payload.
type Item struct {
	Kind      kube.ResourceKind
	Namespace string
	Name      string
}

type document struct {
	item    Item
	payload []byte
}

// Set is an immutable import. Source bytes, namespace, session and resolved
// targets contribute to its identity. Its zero value is not a usable import.
type Set struct {
	id, sessionID, namespace string
	documents                []document
}

func (s *Set) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

func (s *Set) SessionID() string {
	if s == nil {
		return ""
	}
	return s.sessionID
}

func (s *Set) Namespace() string {
	if s == nil {
		return ""
	}
	return s.namespace
}

func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.documents)
}

// Items returns a separate descriptor slice. All descriptor fields are values;
// callers cannot mutate the resolved targets or reach the owned payloads.
func (s *Set) Items() []Item {
	if s == nil {
		return nil
	}
	items := make([]Item, len(s.documents))
	for i := range s.documents {
		items[i] = s.documents[i].item
	}
	return items
}
