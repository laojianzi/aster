package uiworkbench

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/delivery"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/operation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
)

type deliveryUIRequest struct {
	method, name, body string
	dryRun             bool
}

type deliveryUIServer struct {
	t                 *testing.T
	server            *httptest.Server
	mu                sync.Mutex
	objects           map[string]*unstructured.Unstructured
	requests          []deliveryUIRequest
	discoveries       int
	failureName, mode string
	failed            bool
	block, started    chan struct{}
	blockName         string
	blocked           bool
	discoveryFail     bool
	errorMessage      string
}

func deliveryUIObject(name, uid, value string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": name, "namespace": "team", "uid": uid, "resourceVersion": "10"},
		"data":     map[string]any{"value": value},
	}}
}

func newDeliveryUIServer(t *testing.T) *deliveryUIServer {
	t.Helper()
	f := &deliveryUIServer{t: t, objects: map[string]*unstructured.Unstructured{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *deliveryUIServer) replyError(w http.ResponseWriter, code int, reason metav1.StatusReason, name string) {
	message := "fixture request " + string(reason)
	if f.errorMessage != "" {
		message = f.errorMessage
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Code: int32(code), Reason: reason, Message: message, Details: &metav1.StatusDetails{Kind: "configmaps", Name: name}})
}

func (f *deliveryUIServer) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/apis" {
		f.mu.Lock()
		f.discoveries++
		fail := f.discoveryFail
		f.mu.Unlock()
		if fail {
			f.replyError(w, http.StatusServiceUnavailable, metav1.StatusReasonServiceUnavailable, "")
			return
		}
		_ = json.NewEncoder(w).Encode(metav1.APIGroupList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "APIGroupList"}})
		return
	}
	if r.URL.Path == "/api/v1" {
		_ = json.NewEncoder(w).Encode(metav1.APIResourceList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "APIResourceList"}, GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: []string{"get", "list", "watch", "create", "patch"}}}})
		return
	}
	if r.URL.Path == "/api/v1/namespaces/team" {
		_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "team", "uid": "namespace-uid", "resourceVersion": "1"}})
		return
	}
	const base = "/api/v1/namespaces/team/configmaps"
	if r.URL.Path != base && !strings.HasPrefix(r.URL.Path, base+"/") {
		f.replyError(w, http.StatusNotFound, metav1.StatusReasonNotFound, "")
		return
	}
	name := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, base), "/")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Error(err)
		return
	}
	_ = r.Body.Close()
	var desired *unstructured.Unstructured
	if r.Method == http.MethodPost || r.Method == http.MethodPatch {
		desired = &unstructured.Unstructured{}
		if err := desired.UnmarshalJSON(body); err != nil {
			f.t.Error(err)
			return
		}
		name = desired.GetName()
	}
	dryRun := r.URL.Query().Get("dryRun") == "All"
	f.mu.Lock()
	f.requests = append(f.requests, deliveryUIRequest{r.Method, name, string(body), dryRun})
	current := f.objects[name].DeepCopy()
	mode, fail := f.mode, name == f.failureName && !f.failed
	block := f.block != nil && name == f.blockName && !f.blocked && (dryRun || mode == "block-write" && r.Method != http.MethodGet)
	if block {
		f.blocked = true
		close(f.started)
	}
	if fail && r.Method != http.MethodGet && !dryRun {
		f.failed = true
	}
	f.mu.Unlock()
	if block {
		select {
		case <-f.block:
		case <-r.Context().Done():
			return
		}
	}
	if r.Method == http.MethodGet {
		if mode == "forbidden-preview" {
			f.replyError(w, http.StatusForbidden, metav1.StatusReasonForbidden, name)
		} else if current == nil {
			f.replyError(w, http.StatusNotFound, metav1.StatusReasonNotFound, name)
		} else {
			_ = json.NewEncoder(w).Encode(current)
		}
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPatch {
		f.replyError(w, http.StatusMethodNotAllowed, metav1.StatusReasonMethodNotAllowed, name)
		return
	}
	if r.URL.Query().Get("fieldValidation") != "Strict" {
		f.t.Error("delivery omitted strict field validation")
	}
	if r.Method == http.MethodPost {
		if r.URL.Query().Get("fieldManager") != "aster-desktop" {
			f.t.Error("delivery changed the create manager")
		}
		if current != nil {
			f.replyError(w, http.StatusConflict, metav1.StatusReasonAlreadyExists, name)
			return
		}
	} else {
		if r.Header.Get("Content-Type") != "application/apply-patch+yaml" || r.URL.Query().Get("force") != "false" || r.URL.Query().Get("fieldManager") != "aster-apply" {
			f.t.Error("delivery bypassed the reviewed SSA options")
		}
		if current == nil || desired.GetUID() != current.GetUID() || desired.GetResourceVersion() != current.GetResourceVersion() {
			f.replyError(w, http.StatusConflict, metav1.StatusReasonConflict, name)
			return
		}
	}
	if fail && mode == "conflict" && !dryRun {
		f.replyError(w, http.StatusConflict, metav1.StatusReasonConflict, name)
		return
	}
	result := desired.DeepCopy()
	result.SetUID(types.UID(name + "-uid"))
	result.SetResourceVersion("11")
	if current != nil {
		result.SetUID(current.GetUID())
	}
	if !dryRun {
		f.mu.Lock()
		f.objects[name] = result.DeepCopy()
		f.mu.Unlock()
		if fail && mode == "unknown" {
			f.replyError(w, http.StatusServiceUnavailable, metav1.StatusReasonServiceUnavailable, name)
			return
		}
	}
	_ = json.NewEncoder(w).Encode(result)
}

func (f *deliveryUIServer) counts() (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	previews, writes := 0, 0
	for _, r := range f.requests {
		if r.method != http.MethodGet {
			if r.dryRun {
				previews++
			} else {
				writes++
			}
		}
	}
	return f.discoveries, previews, writes
}

func deliveryHarness(t *testing.T, f *deliveryUIServer, name string) *relationshipHarness {
	t.Helper()
	h := newRelationshipHarness(t, 35*time.Second)
	b, err := kube.New(&rest.Config{Host: f.server.URL, QPS: 10000, Burst: 10000})
	if err != nil {
		t.Fatal(err)
	}
	h.w.backend, h.w.sessionID, h.w.namespace = b, name+"-session", "team"
	h.w.activeContext, h.w.currentContext, h.w.activeNamespace = name, name, "team"
	h.w.contexts = []string{name}
	h.w.ops = operation.NewService(b, h.w.sessionID)
	h.tt.Frame()
	h.click("Deliver manifests")
	return h
}

func deliveryYAML(name, value string) string {
	return fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\n  namespace: team\ndata:\n  value: %s\n", name, value)
}

func pasteDelivery(h *relationshipHarness, source string) {
	h.t.Helper()
	h.click("Delivery manifests")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(source)
}

func importDeliveryUI(h *relationshipHarness) {
	h.t.Helper()
	h.click("Import manifests")
	h.pump(func() bool { return h.w.delivery.busy == "" })
	if h.w.delivery.set == nil {
		h.t.Fatalf("delivery import: %s", h.w.delivery.status)
	}
}

func previewDeliveryUI(h *relationshipHarness, label string) {
	h.t.Helper()
	h.click(label)
	h.pump(func() bool { return h.w.delivery.busy == "" })
	if h.w.delivery.plan == nil {
		h.t.Fatalf("delivery preview: %s", h.w.delivery.status)
	}
}

func submitDeliveryUI(h *relationshipHarness, name string) {
	h.t.Helper()
	h.click("Delivery confirmation")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(name)
	h.click("Submit reviewed object")
	h.pump(func() bool { return h.w.delivery.busy == "" })
}

// Select the table through an ordinary text cell, not its object button. The
// public table then owns keyboard navigation, independently of button handlers.
func clickDeliveryKindCell(h *relationshipHarness, objectLabel string) {
	h.t.Helper()
	kind, kindOK := h.tt.Find("ConfigMap")
	object, objectOK := h.tt.Find(objectLabel)
	if !kindOK || !objectOK {
		h.t.Fatal("delivery inventory cell is not visible")
	}
	h.tt.ClickAt(kind.X+kind.W/2, object.Y+object.H/2)
}

func TestNativeDeliveryImportPreviewAndConfirm(t *testing.T) {
	f := newDeliveryUIServer(t)
	f.objects["existing"] = deliveryUIObject("existing", "existing-uid", "old")
	h := deliveryHarness(t, f, "delivery-cluster")
	source := "# 中文 🚢 — rendered manifests\n" + deliveryYAML("first", "original") + "---\n" + deliveryYAML("existing", "updated")
	pasteDelivery(h, source)
	buffer, snapshot := h.w.delivery.editor, h.w.delivery.editor.Snapshot()
	importDeliveryUI(h)
	setID := h.w.delivery.set.ID()
	if h.w.delivery.set.Len() != 2 || h.w.delivery.set.Items()[0].Name != "first" || h.w.delivery.set.Items()[1].Name != "existing" {
		t.Fatal("import changed source order")
	}
	h.click("2. existing")
	previewDeliveryUI(h, "Preview next object")
	firstPlan := h.w.delivery.plan
	before, after := firstPlan.Preview()
	if firstPlan.Kind() != "create" || firstPlan.Target().Name != "first" || !strings.Contains(h.w.delivery.diff, "CREATE ONLY · aster-desktop") || h.w.plan != nil {
		t.Fatal("arbitrary inventory selection replaced the first unfinished target")
	}
	submitDeliveryUI(h, "wrong-name")
	_, _, writes := f.counts()
	if writes != 0 || h.w.delivery.plan != firstPlan {
		t.Fatal("wrong exact-name confirmation submitted a change")
	}
	h.click("2. existing")
	if h.w.delivery.plan != nil || h.w.delivery.confirmation != "" || h.w.delivery.diff != "" {
		t.Fatal("selection retained another object's approval")
	}
	previewDeliveryUI(h, "Preview next object")
	h.click("Delivery confirmation")
	h.tt.Type("first")
	clickDeliveryKindCell(h, "2. existing")
	if h.w.delivery.selected != 1 || h.w.delivery.plan != nil || h.w.delivery.confirmation != "" || h.w.delivery.diff != "" {
		t.Fatal("ordinary table-cell selection retained approval")
	}
	clickDeliveryKindCell(h, "1. first")
	if h.w.delivery.selected != 0 || h.w.delivery.plan != nil || h.w.delivery.confirmation != "" {
		t.Fatal("returning through an ordinary cell restored the old approval")
	}
	previewDeliveryUI(h, "Preview next object")
	h.click("Delivery confirmation")
	h.tt.Type("first")
	clickDeliveryKindCell(h, "1. first")
	if !h.tt.Focused("Delivery inventory") || h.w.delivery.plan == nil || h.w.delivery.confirmation != "first" {
		t.Fatal("focusing the current table row changed its review")
	}
	h.tt.Key(0, ui.KeyDown)
	if h.w.delivery.selected != 1 || h.w.delivery.plan != nil || h.w.delivery.confirmation != "" || h.w.delivery.diff != "" {
		t.Fatal("table keyboard navigation retained approval")
	}
	h.tt.Key(0, ui.KeyHome)
	if h.w.delivery.selected != 0 || h.w.delivery.plan != nil || h.w.delivery.confirmation != "" {
		t.Fatal("returning by keyboard restored the old approval")
	}
	previewDeliveryUI(h, "Preview next object")
	h.click("Change manifests")
	if h.w.delivery.plan != nil || h.w.delivery.set != nil || h.w.delivery.editor != buffer || snapshot.String() != source {
		t.Fatal("changing manifests retained approval or rewrote native input")
	}
	changed := strings.Replace(source, "value: original", "value: changed-中文", 1)
	pasteDelivery(h, changed)
	importDeliveryUI(h)
	if h.w.delivery.set.ID() == setID {
		t.Fatal("changed source retained its immutable import identity")
	}
	previewDeliveryUI(h, "Preview next object")
	oldBefore, oldAfter := firstPlan.Preview()
	if string(before) != string(oldBefore) || string(after) != string(oldAfter) || h.w.delivery.plan == firstPlan || !strings.Contains(h.w.delivery.diff, "changed-中文") {
		t.Fatal("new input mutated a retained Prepared preview")
	}
	h.click("Delivery confirmation")
	h.tt.Type("first")
	h.click("Submit reviewed object")
	clickDeliveryKindCell(h, "2. existing")
	h.tt.Key(0, ui.KeyEnd)
	if h.w.delivery.busy != "Submitting" || h.w.delivery.selected != 0 || h.w.pendingWrites != 1 {
		t.Fatal("submitting delivery allowed its inventory selection to change")
	}
	h.pump(func() bool { return h.w.delivery.busy == "" })
	if h.w.delivery.outcomes[0].State != "Succeeded" || h.w.delivery.outcomes[1].State != "Unreviewed" || h.w.delivery.plan != nil {
		t.Fatalf("unexpected ordered result: %s", h.w.delivery.outcomeText())
	}
	h.click("Preview next object")
	h.pump(func() bool { return h.w.delivery.busy == "" })
	if !h.w.delivery.halted || h.w.delivery.plan != nil || !strings.Contains(h.w.delivery.status, "acknowledge") {
		t.Fatal("existing SSA did not require explicit omission acknowledgement")
	}
	h.click("I understand omitted fields may be removed")
	previewDeliveryUI(h, "Review remaining again")
	if h.w.delivery.plan.Kind() != "apply" || !strings.Contains(h.w.delivery.diff, "FIELD OWNERSHIP") || !strings.Contains(h.w.delivery.diff, "aster-apply") {
		t.Fatal("existing object lost SSA/ownership review")
	}
	submitDeliveryUI(h, "existing")
	if h.w.delivery.next() != -1 || h.w.pendingWrites != 0 || h.w.delivery.editor != buffer || h.w.delivery.editor.Snapshot().String() != changed {
		t.Fatal("ordered completion changed input or retained an active write")
	}
	discovery, _, writes := f.counts()
	if discovery != 2 || writes != 2 {
		t.Fatalf("fresh import or explicit writes: discovery=%d writes=%d", discovery, writes)
	}
	saveNativeScreenshot(t, h.tt)
	h.click("Back to resources")
	if h.w.delivery.open || h.w.delivery.set == nil || h.w.delivery.halted || h.w.delivery.editor != buffer {
		t.Fatal("returning after completion lost outcomes or demanded impossible re-review")
	}
	h.click("Deliver manifests")
	h.click("Inspect accepted resource")
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detail.GetUID() != "existing-uid" || h.w.delivery.open || h.w.delivery.set == nil {
		t.Fatal("accepted-resource inspection lost its UID or retained delivery outcomes")
	}
	h.click("Disconnect")
	if h.w.delivery.editor != nil || h.w.delivery.set != nil || h.w.delivery.plan != nil {
		t.Fatal("disconnect retained private delivery input or capability")
	}
}

func TestNativeDeliveryPartialUnknownAndReviewAgain(t *testing.T) {
	for _, mode := range []string{"conflict", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			f := newDeliveryUIServer(t)
			f.failureName, f.mode = "second", mode
			h := deliveryHarness(t, f, "partial-cluster")
			source := deliveryYAML("first", "one") + "---\n" + deliveryYAML("second", "two") + "---\n" + deliveryYAML("third", "three")
			pasteDelivery(h, source)
			importDeliveryUI(h)
			previewDeliveryUI(h, "Preview next object")
			submitDeliveryUI(h, "first")
			previewDeliveryUI(h, "Preview next object")
			failedPlan := h.w.delivery.plan.ID()
			submitDeliveryUI(h, "second")
			want := "Conflict"
			if mode == "unknown" {
				want = "Unknown"
			}
			if !h.w.delivery.halted || h.w.delivery.outcomes[0].State != "Succeeded" || h.w.delivery.outcomes[1].State != want || h.w.delivery.outcomes[2].State != "Unreviewed" || h.w.delivery.plan != nil || h.w.pendingWrites != 0 {
				t.Fatalf("partial outcome was lost or advanced: %s", h.w.delivery.outcomeText())
			}
			h.click("Preview next object")
			h.click("Submit reviewed object")
			_, _, writes := f.counts()
			if writes != 2 {
				t.Fatal("halted delivery retried or advanced automatically")
			}
			h.click("Delivery outcomes")
			if !strings.Contains(h.w.delivery.outcomeText(), want) || !strings.Contains(strings.Join(h.w.history, "\n"), "[partial-cluster]") {
				t.Fatal("result history lost the original identity or failure")
			}
			h.click("Back to resources")
			h.click("Deliver manifests")
			if !h.w.delivery.halted || h.w.delivery.outcomes[0].State != "Succeeded" || h.w.delivery.editor.Snapshot().String() != source {
				t.Fatal("pause/resume discarded input or a completed outcome")
			}
			if mode == "unknown" {
				// The fake API committed before losing its response. Fresh review
				// must observe that object and require SSA acknowledgement.
				h.click("Review remaining again")
				h.pump(func() bool { return h.w.delivery.busy == "" })
				if !h.w.delivery.halted || h.w.delivery.plan != nil {
					t.Fatal("unknown create was replayed without observing existing state")
				}
				h.click("I understand omitted fields may be removed")
			}
			previewDeliveryUI(h, "Review remaining again")
			if h.w.delivery.plan.ID() == failedPlan || h.w.delivery.plan.Target().Name != "second" || mode == "unknown" && h.w.delivery.plan.Kind() != "apply" {
				t.Fatal("explicit re-review reused the consumed plan or skipped its target")
			}
			submitDeliveryUI(h, "second")
			if h.w.delivery.outcomes[0].State != "Succeeded" || h.w.delivery.outcomes[1].State != "Succeeded" || h.w.delivery.outcomes[2].State != "Unreviewed" || !strings.Contains(h.w.delivery.outcomeText(), want) {
				t.Fatal("fresh review erased an earlier outcome or automatically submitted the remainder")
			}
			previewDeliveryUI(h, "Preview next object")
			cancelledPlan := h.w.delivery.plan.ID()
			h.click("Cancel delivery work")
			if !h.w.delivery.halted || h.w.delivery.plan != nil || h.w.delivery.outcomes[2].State != "Cancelled" {
				t.Fatal("cancelled preview retained its approval")
			}
			previewDeliveryUI(h, "Review remaining again")
			if h.w.delivery.plan.ID() == cancelledPlan {
				t.Fatal("cancelled plan was reused")
			}
			submitDeliveryUI(h, "third")
			_, _, writes = f.counts()
			if writes != 4 || h.w.delivery.next() != -1 {
				t.Fatalf("unexpected explicit request count: %d", writes)
			}
			saveNativeScreenshot(t, h.tt)
		})
	}
}

func TestNativeDeliveryStaleWorkAndWorkspaceIsolation(t *testing.T) {
	f, other := newDeliveryUIServer(t), newDeliveryUIServer(t)
	f.block, f.started, f.blockName = make(chan struct{}), make(chan struct{}), "delayed"
	h := deliveryHarness(t, f, "original-cluster")
	h2 := deliveryHarness(t, other, "independent-cluster")
	pasteDelivery(h2, deliveryYAML("independent", "other-window"))
	importDeliveryUI(h2)
	previewDeliveryUI(h2, "Preview next object")
	otherPlan, otherSet, otherSource := h2.w.delivery.plan, h2.w.delivery.set, h2.w.delivery.editor.Snapshot()
	pasteDelivery(h, deliveryYAML("delayed", "first-window"))
	importDeliveryUI(h)
	h.click("Preview next object")
	select {
	case <-f.started:
	case <-h.ctx.Done():
		t.Fatal("delayed dry-run was not entered")
	}
	h.click("Back to resources")
	h.click("Deliver manifests")
	if !h.w.delivery.halted || h.w.delivery.plan != nil {
		t.Fatal("returning during preview did not pause the old action")
	}
	close(f.block)
	previewDeliveryUI(h, "Review remaining again")
	if h2.w.delivery.plan != otherPlan || h2.w.delivery.set != otherSet || h2.w.delivery.editor.Snapshot() != otherSource {
		t.Fatal("one workspace replaced another workspace's prepared identity")
	}
	if h.w.delivery.plan == nil || h.w.delivery.plan.Target().Name != "delayed" || len(h.w.delivery.outcomes[0].Attempts) != 1 || h.w.delivery.outcomes[0].Attempts[0].State != "Cancelled" {
		t.Fatal("late preview callback replaced a fresh review or erased cancellation")
	}
	// The next explicit submission is suspended in transport. Namespace change
	// cancels it, and its late Unknown belongs only to the original history.
	f.mu.Lock()
	f.block, f.started, f.blocked, f.mode = make(chan struct{}), make(chan struct{}), false, "block-write"
	f.mu.Unlock()
	h.click("Delivery confirmation")
	h.tt.Type("delayed")
	h.click("Submit reviewed object")
	select {
	case <-f.started:
	case <-h.ctx.Done():
		t.Fatal("submitted request was not entered")
	}
	if h.w.pendingWrites != 1 {
		t.Fatal("write was not tracked before dispatch")
	}
	h.click("Namespace")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("different")
	h.pump(func() bool { return h.w.pendingWrites == 0 })
	if h.w.delivery.set != nil || h.w.delivery.plan != nil || h.w.delivery.busy != "" || !strings.Contains(strings.Join(h.w.history, "\n"), "[original-cluster] create team/delayed · Unknown") {
		t.Fatal("stale submission polluted a new namespace or lost original-context history")
	}
	if h2.w.delivery.plan != otherPlan || h2.w.pendingWrites != 0 {
		t.Fatal("late write completion crossed windows")
	}
	h.click("Disconnect")
	if h.w.delivery.editor != nil || h.w.delivery.set != nil || h.w.delivery.open {
		t.Fatal("disconnect retained private source")
	}
	// Let the real service finish while its UI dispatch is still queued. A user
	// cancelling the last in-flight display cannot turn that accepted result into
	// an impossible request to review an empty remainder.
	h2.click("Delivery confirmation")
	h2.tt.Type("independent")
	h2.click("Submit reviewed object")
	var accepted func()
	select {
	case accepted = <-h2.updates:
	case <-h2.ctx.Done():
		t.Fatal("independent submission did not finish")
	}
	h2.click("Cancel delivery work")
	if h2.w.pendingWrites != 1 || !h2.w.delivery.halted || h2.w.delivery.busy != "Submitting" {
		t.Fatal("cancelling a displayed submission lost its pending request")
	}
	accepted()
	h2.tt.Frame()
	if h2.w.delivery.outcomes[0].State != "Succeeded" || h2.w.delivery.halted || h2.w.delivery.next() != -1 || h2.w.pendingWrites != 0 || h2.w.delivery.plan != nil || !strings.Contains(h2.w.delivery.status, "All imported objects have been accepted") || strings.Contains(h2.w.delivery.status, "review remaining again") {
		t.Fatal("late final success demanded re-review or revived an approval")
	}
	h2.w.Close()
	if h2.w.delivery.editor != nil || h2.w.delivery.set != nil {
		t.Fatal("workspace close retained delivery input")
	}
}

func TestNativeDeliveryMinimumWindowUnicodeAndBudget(t *testing.T) {
	t.Run("minimum-window", func(t *testing.T) {
		f := newDeliveryUIServer(t)
		h := deliveryHarness(t, f, "unicode-cluster")
		h.tt = ui.NewTester(h.w.View, 1100, 700)
		source := "# 中文 🚢 é\n" + deliveryYAML("unicode", "中文🚢é")
		pasteDelivery(h, source)
		buffer, original := h.w.delivery.editor, h.w.delivery.editor.Snapshot()
		importDeliveryUI(h)
		previewDeliveryUI(h, "Preview next object")
		if h.w.delivery.editor != buffer || h.w.delivery.editor.Snapshot().String() != source || original.String() != source || !strings.Contains(h.w.delivery.diff, "中文🚢é") {
			t.Fatal("native import/review changed Unicode source or its buffer")
		}
		for _, label := range []string{"Back to resources", "Cancel delivery work", "I understand omitted fields may be removed", "Review details", "Delivery outcomes", "Delivery confirmation", "Submit reviewed object", "Preview next object", "Review remaining again", "Inspect accepted resource", "Change manifests"} {
			r, ok := h.tt.Find(label)
			if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1100 || r.Y+r.H > 700 {
				t.Errorf("%s outside minimum window: %+v found=%v", label, r, ok)
			}
		}
		saveNativeScreenshot(t, h.tt)
		h.click("Change manifests")
		h.click("Delivery manifests")
		// Returning to source preserves its bytes and buffer identity. The
		// remounted native control supports Undo for new edits; its previous
		// widget history is not retained across the inventory view or Back.
		h.tt.Type("\n# appended")
		h.tt.Key(ui.Cmd, ui.KeyZ)
		if h.w.delivery.editor != buffer || h.w.delivery.editor.Snapshot().String() != source {
			t.Fatal("Undo of a new edit changed the retained source or buffer")
		}
	})
	t.Run("byte-budget-retains-input", func(t *testing.T) {
		f := newDeliveryUIServer(t)
		h := deliveryHarness(t, f, "budget-cluster")
		// Exercise direct native text input with bounded paragraphs. MyGo's
		// synchronous caret calculation for a giant single paragraph remains
		// an upstream limitation; clipboard preflight is qualified separately.
		oversized := strings.Repeat(strings.Repeat("x", 127)+"\n", delivery.MaxBytes/128) + "x"
		pasteDelivery(h, oversized)
		_, budgetVisible := h.tt.Find("Delivery budget exceeded")
		if len(oversized) != delivery.MaxBytes+1 || h.w.delivery.editor.Snapshot().String() != oversized || !budgetVisible {
			t.Fatal("oversized input was truncated or kept in native layout")
		}
		h.click("Import manifests")
		discovery, previews, writes := f.counts()
		if discovery != 0 || previews != 0 || writes != 0 || h.w.delivery.set != nil {
			t.Fatal("oversized input entered discovery or mutation")
		}
		h.click("Clear oversized manifests")
		if h.w.delivery.editor.Snapshot().ByteLen() != 0 {
			t.Fatal("explicit clear did not restore an empty native input")
		}
	})
	t.Run("newline-budget-retains-input", func(t *testing.T) {
		f := newDeliveryUIServer(t)
		h := deliveryHarness(t, f, "newline-cluster")
		oversized := strings.Repeat("\n", delivery.MaxNewlines+1)
		pasteDelivery(h, oversized)
		_, budgetVisible := h.tt.Find("Delivery budget exceeded")
		if h.w.delivery.editor.Snapshot().String() != oversized || !budgetVisible {
			t.Fatal("newline overflow was discarded or still laid out")
		}
		h.click("Import manifests")
		discovery, previews, writes := f.counts()
		if discovery != 0 || previews != 0 || writes != 0 {
			t.Fatal("newline overflow reached a remote request")
		}
		h.click("Clear oversized manifests")
		if h.w.delivery.editor.Snapshot().ByteLen() != 0 {
			t.Fatal("explicit clear failed after newline overflow")
		}
	})
	t.Run("clipboard-preflight-retains-input", func(t *testing.T) {
		f := newDeliveryUIServer(t)
		h := deliveryHarness(t, f, "clipboard-cluster")
		h.tt = ui.NewTester(h.w.View, 1100, 700)
		base := deliveryYAML("clipboard", "base") + "# " + strings.Repeat("x", 4096) + "\n"
		pasteDelivery(h, base)
		buffer := h.w.delivery.editor
		ordinary := "# ordinary clipboard 中文 🚢 é\n"
		h.tt.SetClipboard(ordinary)
		h.tt.Key(ui.Cmd, ui.KeyV)
		original := buffer.Snapshot()
		if original.String() != base+ordinary || h.w.delivery.retainedPaste != "" {
			t.Fatal("ordinary clipboard paste bypassed or failed native insertion")
		}
		inputs := []string{
			strings.Repeat("x", delivery.MaxBytes+1),
			strings.Repeat("\n", delivery.MaxNewlines+1),
			strings.Repeat("x", maxDeliveryClipboardLine+1),
			strings.Repeat("x", 4097), // each side fits, but their possible joined line does not
		}
		for i, incoming := range inputs {
			h.click("Delivery manifests")
			h.tt.Key(ui.Cmd, ui.KeyA)
			h.tt.SetClipboard(incoming)
			if i%2 == 0 {
				h.tt.Key(ui.Cmd, ui.KeyV)
			} else {
				h.tt.Command("paste")
			}
			_, visible := h.tt.Find("Delivery retained paste")
			if !visible || h.w.delivery.retainedPaste != incoming || h.w.delivery.editor != buffer || buffer.Snapshot() != original {
				t.Fatalf("clipboard preflight %d changed the draft or discarded incoming text", i)
			}
			h.tt.Key(ui.Cmd, ui.KeyC)
			if h.tt.Clipboard() != original.String() {
				t.Fatal("retained paste changed the native selection")
			}
			h.tt.SetClipboard("second paste must not replace the retained input")
			h.tt.Key(ui.Cmd, ui.KeyV)
			if h.w.delivery.retainedPaste != incoming || buffer.Snapshot() != original {
				t.Fatal("a second blocked paste overwrote retained input or the draft")
			}
			h.click("Import manifests")
			discovery, previews, writes := f.counts()
			if discovery != 0 || previews != 0 || writes != 0 || h.w.delivery.set != nil {
				t.Fatal("retained clipboard input did not pause import")
			}
			if i == 0 {
				saveNativeScreenshot(t, h.tt)
			}
			h.click("Discard retained paste")
			if h.w.delivery.retainedPaste != "" || h.w.delivery.editor != buffer || buffer.Snapshot() != original {
				t.Fatal("discarding the retained paste changed the original draft")
			}
		}
		h.click("Delivery manifests")
		h.tt.Key(ui.Cmd, ui.KeyZ)
		if buffer.Snapshot().String() != base {
			t.Fatal("clipboard interception changed the existing native undo history")
		}
		h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
		if buffer.Snapshot().String() != original.String() {
			t.Fatal("clipboard interception changed native redo")
		}
		importDeliveryUI(h)
		h.click("Change manifests")
		h.click("Delivery manifests")
		h.tt.SetClipboard(inputs[0])
		h.tt.Key(ui.Cmd, ui.KeyV)
		if h.w.delivery.retainedPaste != inputs[0] {
			t.Fatal("clipboard preflight did not resume after explicit input change")
		}
		h.click("Back to resources")
		h.click("Deliver manifests")
		if h.w.delivery.retainedPaste != inputs[0] || h.w.delivery.editor != buffer || buffer.Snapshot().String() != original.String() {
			t.Fatal("pausing delivery discarded retained input or rewrote the draft")
		}
		h.click("Disconnect")
		if h.w.delivery.editor != nil || h.w.delivery.retainedPaste != "" || h.w.delivery.set != nil {
			t.Fatal("disconnect retained private clipboard or draft input")
		}
	})
	t.Run("bounded-failed-attempts", func(t *testing.T) {
		f := newDeliveryUIServer(t)
		f.mode = "forbidden-preview"
		f.errorMessage = strings.Repeat("服务器拒绝\n", 900)
		h := deliveryHarness(t, f, "failure-cluster")
		h.tt = ui.NewTester(h.w.View, 1100, 700)
		pasteDelivery(h, deliveryYAML("forbidden", "value"))
		importDeliveryUI(h)
		for i := 0; i < maxDeliveryAttempts+3; i++ {
			label := "Review remaining again"
			if i == 0 {
				label = "Preview next object"
			}
			h.click(label)
			h.pump(func() bool { return h.w.delivery.busy == "" })
			if h.w.delivery.plan != nil || !h.w.delivery.halted {
				t.Fatal("failed preview escaped the halted state")
			}
		}
		outcome := h.w.delivery.outcomes[0]
		if len(outcome.Attempts) != maxDeliveryAttempts || outcome.Dropped != 3 || !strings.Contains(h.w.delivery.outcomeText(), "3 earlier attempt records omitted") {
			t.Fatal("failed preview results grew without a visible bound")
		}
		if !strings.HasSuffix(h.w.delivery.status, "… (status shortened)") || utf8.RuneCountInString(h.w.delivery.status) > 2100 {
			t.Fatal("long API failure did not keep its visible status bound")
		}
		for _, label := range []string{"Back to resources", "Delivery confirmation", "Review remaining again", "Change manifests"} {
			r, ok := h.tt.Find(label)
			if !ok || r.Y < 0 || r.Y+r.H > 700 {
				t.Errorf("long error displaced %s: %+v found=%v", label, r, ok)
			}
		}
		_, _, writes := f.counts()
		if writes != 0 {
			t.Fatal("a lookup failure fell back to a write")
		}
	})
}
