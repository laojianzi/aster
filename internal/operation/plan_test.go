package operation

import (
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func target() resource.Identity {
	return resource.Identity{
		SessionID: "cluster-a",
		GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
		Namespace: "prod",
		Name: "api",
		UID: types.UID("uid-1"),
	}
}

func TestPlanBindsSessionAndCopiesPayload(t *testing.T) {
	body := []byte("replicas: 3")
	p, err := NewPlan(Apply, target(), Preconditions{UID: "uid-1", ResourceVersion: "10"}, body, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	body[0] = 'X'
	if string(p.Payload) != "replicas: 3" {
		t.Fatal("plan payload aliases caller memory")
	}
	if err := p.ValidateForSession("cluster-a"); err != nil {
		t.Fatal(err)
	}
	if err := p.ValidateForSession("cluster-b"); err == nil {
		t.Fatal("cross-session execution must fail")
	}
}

func TestPlanIDChangesWithPrecondition(t *testing.T) {
	a, _ := NewPlan(Delete, target(), Preconditions{ResourceVersion: "10"}, nil, time.Unix(1, 0))
	b, _ := NewPlan(Delete, target(), Preconditions{ResourceVersion: "11"}, nil, time.Unix(1, 0))
	if a.ID == b.ID {
		t.Fatal("different approved preconditions must produce different plan IDs")
	}
}
