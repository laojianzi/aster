package uiworkbench

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/operation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

func applyUIObject() *unstructured.Unstructured {
	o := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "apply-example", "namespace": "team", "uid": "original", "resourceVersion": "10"}, "data": map[string]interface{}{"foreign": "do-not-import"}}}
	o.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "controller", Operation: metav1.ManagedFieldsOperationUpdate, APIVersion: "v1", FieldsType: "FieldsV1", FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:data":{"f:foreign":{}}}`)}}})
	return o
}
func attachApplyUI(t *testing.T, h *relationshipHarness, host string) {
	t.Helper()
	b, err := kube.New(&rest.Config{Host: host})
	if err != nil {
		t.Fatal(err)
	}
	h.w.backend = b
	h.w.sessionID = "apply-ui"
	h.w.activeContext = "apply-cluster"
	h.w.activeNamespace = "team"
	h.w.detail = applyUIObject()
	for _, kind := range catalog() {
		if kind.GVR.Resource == "configmaps" {
			h.w.detailKind = kind
		}
	}
	h.w.ops = operation.NewService(b, h.w.sessionID)
	h.tt.Frame()
}
func TestNativeApplyAcknowledgmentReviewAndDraftIsolation(t *testing.T) {
	var patches, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		o := applyUIObject()
		if r.Method == http.MethodPatch {
			patches.Add(1)
			if r.URL.Query().Get("dryRun") != "All" {
				writes.Add(1)
			}
			if r.Header.Get("Content-Type") != "application/apply-patch+yaml" || r.URL.Query().Get("force") != "false" || r.URL.Query().Get("fieldManager") != "aster-apply" {
				t.Error("unsafe apply transport")
			}
			body, _ := io.ReadAll(r.Body)
			intent := &unstructured.Unstructured{}
			if err := intent.UnmarshalJSON(body); err != nil {
				t.Error(err)
			}
			if intent.GetUID() != "original" || intent.GetResourceVersion() != "10" {
				t.Error("missing preconditions")
			}
			o.Object["data"] = map[string]interface{}{"foreign": "do-not-import", "managed": "new-value"}
		}
		_ = json.NewEncoder(w).Encode(o)
	}))
	defer server.Close()
	h := newRelationshipHarness(t, 15*time.Second)
	attachApplyUI(t, h, server.URL)
	h.click("Owners")
	if !strings.Contains(h.w.ownersText, "controller") || strings.Contains(h.w.ownersText, "do-not-import") {
		t.Fatal("bad ownership display")
	}
	h.click("Edit")
	h.click("Apply")
	if strings.Contains(h.w.applyDraft, "do-not-import") || strings.Contains(h.w.applyDraft, "resourceVersion") || h.w.applyAcknowledged {
		t.Fatal("implicitly imported live state or trust")
	}
	h.click("Apply intent YAML")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: apply-example\n  namespace: team\ndata:\n  managed: new-value\n")
	h.w.prepare("apply")
	if h.w.preparing || patches.Load() != 0 {
		t.Fatal("unacknowledged apply preview sent")
	}
	h.click("I understand omitted fields may be removed")
	h.click("Preview apply")
	h.pump(func() bool { return h.w.plan != nil || !h.w.preparing })
	if h.w.plan == nil || h.w.plan.Kind() != "apply" || !strings.Contains(h.w.diff, "FIELD OWNERSHIP") || writes.Load() != 0 {
		t.Fatalf("preview: %s", h.w.detailMessage)
	}
	h.click("Edit")
	h.click("Apply")
	if h.w.plan != nil || h.w.applyAcknowledged {
		t.Fatal("returning to intent retained old approval")
	}
	h.click("I understand omitted fields may be removed")
	h.click("Apply intent YAML")
	h.tt.Type("\n")
	if h.w.applyAcknowledged || h.w.plan != nil {
		t.Fatal("edited intent retained approval")
	}
	h.click("Close detail")
	if h.w.applyDraft != "" || h.w.ownersText != "" || h.w.applyAcknowledged {
		t.Fatal("apply state leaked to next resource")
	}
}
func TestNativeApplyLatePreviewCannotCrossDetails(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && once.CompareAndSwap(false, true) {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(applyUIObject())
	}))
	defer server.Close()
	h := newRelationshipHarness(t, 15*time.Second)
	attachApplyUI(t, h, server.URL)
	h.w.beginApply()
	h.w.applyDraft = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: apply-example\n  namespace: team\ndata:\n  x: y\n"
	h.w.applyAcknowledged = true
	h.w.prepare("apply")
	select {
	case <-started:
	case <-h.ctx.Done():
		t.Fatal("dry-run not started")
	}
	h.w.clearDetail()
	h.w.detail = applyUIObject()
	h.w.detail.SetUID("new")
	close(release)
	var completion func()
	select {
	case completion = <-h.updates:
	case <-h.ctx.Done():
		t.Fatal("completion missing")
	}
	completion()
	h.tt.Frame()
	if h.w.plan != nil || h.w.diff != "" || h.w.applyDraft != "" {
		t.Fatal("old preview crossed resource identity")
	}
}
func TestNativeApplyControlsAtMinimumWindow(t *testing.T) {
	w := New()
	w.detail = applyUIObject()
	for _, kind := range catalog() {
		if kind.GVR.Resource == "configmaps" {
			w.detailKind = kind
		}
	}
	w.beginApply()
	tt := ui.NewTester(w.View, 1100, 700)
	for _, label := range []string{"Edit", "Owners", "Apply intent YAML", "I understand omitted fields may be removed", "Preview apply", "Close detail", "Preview delete"} {
		r, ok := tt.Find(label)
		if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 {
			t.Errorf("%s out of bounds: %+v found=%v", label, r, ok)
		}
	}
	saveNativeScreenshot(t, tt)
}

func TestNativeApplyCloseCancelsDryRun(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	shutdown := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch {
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				return
			}
			_ = r.Body.Close()
			close(started)
			select {
			case <-r.Context().Done():
				close(canceled)
			case <-shutdown:
			}
			return
		}
		_ = json.NewEncoder(w).Encode(applyUIObject())
	}))
	defer func() { close(shutdown); server.Close() }()
	h := newRelationshipHarness(t, 10*time.Second)
	attachApplyUI(t, h, server.URL)
	h.w.beginApply()
	h.w.applyDraft = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: apply-example\n  namespace: team\ndata:\n  x: y\n"
	h.w.applyAcknowledged = true
	h.w.prepare("apply")
	select {
	case <-started:
	case <-h.ctx.Done():
		t.Fatal("preview did not start")
	}
	h.w.clearDetail()
	select {
	case <-canceled:
	case <-h.ctx.Done():
		t.Fatal("detail teardown did not cancel dry-run")
	}
	if h.w.previewCancel != nil || h.w.preparing || h.w.plan != nil {
		t.Fatal("preview state retained")
	}
}
