package uiworkbench

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/relationship"
	"github.com/laojianzi/aster/internal/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestNativeRelationshipLongNamesStayInsideMinimumWindow(t *testing.T) {
	w := New()
	w.sessionID, w.activeContext, w.activeNamespace = "layout-session", "cluster", "team"
	w.detail, w.detailKind, w.detailMode = relatedPod(), catalog()[0], "Related"
	name := strings.Repeat("a", 253)
	typ := relationship.Type{GVR: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, Kind: "ReplicaSet", Namespaced: true}
	w.relatedResult = relationship.Snapshot{Target: w.target(), SourceVersion: "1", Links: []relationship.Link{{Target: resource.Identity{SessionID: w.sessionID, GVR: typ.GVR, Namespace: "team", Name: name, UID: "owner"}, Type: typ, Relation: "Controlled by", State: relationship.Verified}}}
	tt := ui.NewTester(w.View, 1100, 700)
	r, ok := tt.Find("Open ReplicaSet/" + name)
	if !ok || r.W <= 0 || r.X < 0 || r.X+r.W > 1088 || r.Y < 0 || r.Y+r.H > 700 {
		t.Fatalf("long resource link escaped minimum window: %+v found=%v", r, ok)
	}
	saveNativeScreenshot(t, tt)
}
