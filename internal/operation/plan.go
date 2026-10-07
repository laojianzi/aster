package operation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/laojianzi/aster/internal/resource"
)

type Kind string

const (
	Apply  Kind = "apply"
	Delete Kind = "delete"
	Scale  Kind = "scale"
)

type Preconditions struct {
	UID             string `json:"uid,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

type Plan struct {
	ID            string
	Kind          Kind
	Target        resource.Identity
	Preconditions Preconditions
	Payload       []byte
	CreatedAt     time.Time
}

func NewPlan(kind Kind, target resource.Identity, pre Preconditions, payload []byte, now time.Time) (Plan, error) {
	if err := target.Validate(); err != nil {
		return Plan{}, err
	}
	if kind == "" {
		return Plan{}, fmt.Errorf("operation: empty kind")
	}
	if now.IsZero() {
		return Plan{}, fmt.Errorf("operation: zero creation time")
	}
	canonical, err := json.Marshal(struct {
		Kind Kind
		Key  string
		Pre  Preconditions
		Body []byte
		Unix int64
	}{kind, target.Key(), pre, payload, now.UTC().UnixNano()})
	if err != nil {
		return Plan{}, err
	}
	sum := sha256.Sum256(canonical)
	body := append([]byte(nil), payload...)
	return Plan{
		ID:            hex.EncodeToString(sum[:16]),
		Kind:          kind,
		Target:        target,
		Preconditions: pre,
		Payload:       body,
		CreatedAt:     now.UTC(),
	}, nil
}

func (p Plan) ValidateForSession(sessionID string) error {
	if sessionID == "" || p.Target.SessionID != sessionID {
		return fmt.Errorf("operation: plan belongs to session %q, not %q", p.Target.SessionID, sessionID)
	}
	return nil
}
