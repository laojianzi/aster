package uiworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

func TestNativeRedesignConnectionsAreTaskScoped(t *testing.T) {
	w := New()
	w.status = "Live"
	t.Cleanup(w.Close)
	w.path = "/nonexistent/aster-native-redesign"
	tt := ui.NewTester(w.View, 1440, 900)
	if err := tt.Click("Connections"); err != nil {
		saveNativeScreenshot(t, tt)
		t.Fatal(err)
	}
	for _, label := range []string{"Kubeconfig path", "Load contexts", "Credentials", "Browser sign-in"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("connection task cannot reach %q", label)
		}
	}
	if err := tt.Click("Resources"); err != nil {
		t.Fatal(err)
	}
	if _, visible := tt.Find("Kubeconfig path"); visible {
		t.Fatal("connection form remains in the resource workspace")
	}
	if err := tt.Click("Filter resources"); err != nil {
		t.Fatal(err)
	}
	tt.Type("payments-工作负载")
	if err := tt.Click("Connections"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Resources"); err != nil {
		t.Fatal(err)
	}
	if w.filter != "payments-工作负载" || w.path != "/nonexistent/aster-native-redesign" {
		t.Fatal("switching tasks lost resource filter or connection draft")
	}
	saveNativeScreenshot(t, tt)
}

func TestNativeRedesignScopeAndStatusRemainVisible(t *testing.T) {
	for _, height := range []int{480, 499, 700} {
		for _, width := range []int{900, 936, 1100} {
			for _, scale := range []float32{1, 1.25, 1.5, 2} {
				t.Run(fmt.Sprintf("%dx%d-scale-%g", width, height, scale), func(t *testing.T) {
					w := New()
					t.Cleanup(w.Close)
					w.workspaceNumber = 1
					w.currentContext = "production-eu-platform-commerce-long-context-name"
					w.contexts = []string{w.currentContext}
					w.activeContext = w.currentContext
					w.activeNamespace = "commerce"
					tt := ui.NewTester(w.View, width, height)
					tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
					for _, label := range []string{"Cluster context", "Connection status", "Workspace identity", "Connections", "Resources", "New workspace"} {
						r, ok := tt.Find(label)
						if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > float32(width) || r.Y+r.H > float32(height) {
							saveNativeScreenshot(t, tt)
							t.Fatalf("%s unreachable at scale %g: %+v", label, scale, r)
						}
					}
					saveNativeScreenshot(t, tt)
				})
			}
		}
	}
}

func TestNativeRedesignLeavingResourcesCancelsPreviewKeepsDraft(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	w.setEditor("apiVersion: v1\nkind: ConfigMap\n# 工作草稿\n")
	draft, revision := w.editor, w.draftRevision
	preview, cancel := context.WithCancel(w.ctx)
	w.previewCancel, w.preparing = cancel, true
	tt := ui.NewTester(w.View, 1440, 900)
	if err := tt.Click("Connections"); err != nil {
		t.Fatal(err)
	}
	if preview.Err() == nil || w.preparing || w.plan != nil || w.draftRevision == revision {
		t.Fatal("leaving resources retained preview authorization")
	}
	if err := tt.Click("Resources"); err != nil {
		t.Fatal(err)
	}
	if w.editor != draft || w.editor.String() != "apiVersion: v1\nkind: ConfigMap\n# 工作草稿\n" {
		t.Fatal("task navigation replaced the native draft buffer")
	}
}

func TestNativeRedesignResourceGroupsCollapseAndKeyboardSelect(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	tt := ui.NewTester(w.View, 1100, 700)
	if err := tt.Click("Workloads"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.Find("Pods"); ok {
		t.Fatal("collapsed group kept its resources visible")
	}
	if err := tt.Click("Workloads"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Pods"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	if !tt.Focused("Deployments") || w.currentKind.GVR.Resource != "pods" {
		t.Fatal("tree navigation did not separate focus from resource selection")
	}
	tt.Key(0, ui.KeyEnter)
	if w.currentKind.GVR.Resource != "deployments" {
		t.Fatal("native tree keyboard navigation did not select Deployment")
	}
	saveNativeScreenshot(t, tt)
}

func TestNativeRedesignWrappedActionsKeepRecoveryReachable(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	w.helmJournalPath = t.TempDir()
	tt := ui.NewTester(w.View, 1100, 700)
	tt.SetPreferences(ui.Preferences{TextScale: 2, ReduceMotion: true})
	if _, ok := tt.Find("More"); ok {
		t.Fatal("resource actions still require a platform overflow menu")
	}
	if err := tt.Click("Helm recovery"); err != nil {
		t.Fatal(err)
	}
	if !w.helmUninstall.open || w.helmUninstall.mode != "history" {
		t.Fatal("overflow action did not open recovery")
	}
}

func TestNativeRedesignWorkspaceFailureStaysVisible(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	w.newWorkspace = func() error { return errors.New("Workspace limit reached") }
	tt := ui.NewTester(w.View, 1100, 700)
	if err := tt.Click("New workspace"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Workspace limit reached") {
		t.Fatal("workspace failure is hidden from the resource workspace")
	}
}

func TestNativeRedesignObjectUsesWorkspaceAndReturnsToFilteredList(t *testing.T) {
	w := New()
	w.status = "Live"
	t.Cleanup(w.Close)
	w.filter = "api-工作负载"
	w.detail, w.detailKind = modelObject("pod-uid", "api"), catalog()[0]
	w.detailText = "apiVersion: v1\nkind: Pod\n"
	w.detailMode = "YAML"
	tt := ui.NewTester(w.View, 1100, 700)
	r, ok := tt.Find("Object workspace")
	if !ok || r.W < 800 {
		t.Fatalf("object remains in a narrow inspector: %+v", r)
	}
	if r, ok := tt.Find("Filter resources"); ok && r.W > 0 && r.H > 0 {
		t.Fatal("resource list remains beside the object workspace")
	}
	saveNativeScreenshot(t, tt)
	if err := tt.Click("Close detail"); err != nil {
		t.Fatal(err)
	}
	if w.detail != nil || w.filter != "api-工作负载" {
		t.Fatal("return lost the list filter or kept the old object")
	}
	if _, ok := tt.Find("Filter resources"); !ok {
		t.Fatal("return did not restore the resource list")
	}
}

func TestNativeRedesignDiagnosticsRequireAnExplicitChoice(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	w.detail, w.detailKind, w.detailMode = modelObject("pod-uid", "api"), catalog()[0], "YAML"
	w.containers = []string{"app"}
	tt := ui.NewTester(w.View, 1100, 700)
	if _, ok := tt.Find("Command argv JSON"); ok {
		t.Fatal("diagnostic form opened before a choice")
	}
	if err := tt.Click("Diagnostics"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Logs"); err != nil {
		t.Fatal(err)
	}
	if w.detailMode != "Logs" || w.logCancel != nil {
		t.Fatal("opening logs started an unrequested stream")
	}
	if _, ok := tt.Find("Follow logs"); !ok {
		t.Fatal("log actions are unreachable")
	}
	if err := tt.Click("Diagnostics"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Command"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.Find("Command argv JSON"); !ok || w.commandActive {
		t.Fatal("command form is missing or execution started without confirmation")
	}
	saveNativeScreenshot(t, tt)
}

func TestNativeRedesignCompleteResourceNavigation(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	tt := ui.NewTester(w.View, 1100, 700)
	if err := tt.Click("Pods"); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for range 100 {
		seen[w.currentKind.GVR.Resource] = true
		tt.Key(0, ui.KeyDown)
		groupFocused := false
		for _, group := range []string{"Workloads", "Network", "Configuration", "Storage", "Access", "Cluster", "Custom resources"} {
			if tt.Focused(group) {
				groupFocused = true
				break
			}
		}
		if groupFocused {
			tt.Key(0, ui.KeyRight)
		} else {
			tt.Key(0, ui.KeyEnter)
		}
	}
	for _, resource := range []string{"pods", "deployments", "statefulsets", "daemonsets", "replicasets", "jobs", "cronjobs", "services", "ingresses", "networkpolicies", "endpointslices", "configmaps", "secrets", "persistentvolumeclaims", "persistentvolumes", "storageclasses", "nodes", "namespaces", "serviceaccounts", "roles", "rolebindings", "clusterroles", "clusterrolebindings", "customresourcedefinitions"} {
		if !seen[resource] {
			t.Errorf("resource %s cannot be selected through native navigation", resource)
		}
	}
	if seen["certificates"] || seen["issuers"] {
		t.Fatal("offline catalog assumes an optional CRD is installed")
	}
	saveNativeScreenshot(t, tt)
}

func TestNativeRedesignMutationsRequireActionSelection(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	w.detail, w.detailKind, w.detailMode = modelObject("uid", "api"), catalog()[1], "YAML"
	tt := ui.NewTester(w.View, 1100, 700)
	for _, label := range []string{"Replica count", "Preview scale", "Preview restart", "Preview delete"} {
		if _, ok := tt.Find(label); ok {
			t.Fatalf("mutation control %s is always visible", label)
		}
	}
	if err := tt.Click("Actions"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Scale"); err != nil {
		t.Fatal(err)
	}
	if w.preparing || w.plan != nil {
		t.Fatal("opening scale prepared a mutation")
	}
	for _, label := range []string{"Replica count", "Preview scale"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("scale form lacks %s", label)
		}
	}
	if err := tt.Click("YAML"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.Find("Replica count"); ok {
		t.Fatal("scale form survived leaving the task")
	}
	saveNativeScreenshot(t, tt)
}

func TestNativeRedesignOpensOverviewFromVerifiedObject(t *testing.T) {
	obj := modelObject("overview-uid", "payments")
	obj.Object["status"] = map[string]interface{}{"phase": "Running"}
	var reads, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			http.Error(w, "unexpected write", 405)
			return
		}
		reads.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(obj)
	}))
	defer server.Close()
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.openResourceKind(catalog()[0], project(obj))
	h.pump(func() bool { return h.w.detail != nil })
	if h.w.detailMode != "Overview" {
		t.Fatalf("object opens as %s", h.w.detailMode)
	}
	if _, ok := h.tt.Find("Resource document"); ok {
		t.Fatal("raw YAML is the default view")
	}
	for _, label := range []string{"Object overview", "Metadata", "Observed status"} {
		if _, ok := h.tt.Find(label); !ok {
			t.Fatalf("overview lacks %s", label)
		}
	}
	if _, ok := h.tt.Find("Resource UID"); ok {
		t.Fatal("overview exposes metadata before expansion")
	}
	h.click("Metadata")
	for _, label := range []string{"Resource UID", "API version", "Resource version"} {
		if _, ok := h.tt.Find(label); !ok {
			t.Fatalf("expanded metadata lacks %s", label)
		}
	}
	h.click("YAML")
	h.click("Read-only")
	if _, ok := h.tt.Find("Resource document"); !ok {
		t.Fatal("YAML is unreachable")
	}
	h.click("Overview")
	if _, ok := h.tt.Find("Resource UID"); !ok {
		t.Fatal("section switch lost metadata expansion")
	}
	if reads.Load() != 1 || writes.Load() != 0 {
		t.Fatal("local overview switch triggered network work")
	}
	saveNativeScreenshot(t, h.tt)
}

func TestNativeRedesignObjectTabsFollowKeyboardAndKeepDraft(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	w.detail, w.detailKind, w.detailMode = modelObject("tabs-uid", "payments"), catalog()[0], "Overview"
	w.setEditor("apiVersion: v1\nkind: Pod\n# 中文草稿\n")
	original := w.editor
	tt := ui.NewTester(w.View, 1100, 700)
	if err := tt.Click("Overview"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnd)
	if w.detailMode != "YAML" {
		t.Fatalf("End did not choose YAML: %s", w.detailMode)
	}
	for range 12 {
		if tt.Focused("Read-only") {
			break
		}
		tt.Key(0, ui.KeyTab)
	}
	if !tt.Focused("Read-only") {
		t.Fatal("local YAML toolbar is not keyboard reachable")
	}
	tt.Key(0, ui.KeyRight)
	if !tt.Focused("Edit") {
		t.Fatal("local Edit action is not keyboard reachable")
	}
	tt.Key(0, ui.KeyEnter)
	if w.detailMode != "Edit" {
		t.Fatalf("Enter did not choose Edit: %s", w.detailMode)
	}
	if w.editor != original || w.editorText() != "apiVersion: v1\nkind: Pod\n# 中文草稿\n" {
		t.Fatal("tab change replaced draft")
	}
	for range 12 {
		if tt.Focused("YAML") {
			break
		}
		tt.Key(ui.Shift, ui.KeyTab)
	}
	if !tt.Focused("YAML") {
		t.Fatal("cannot return to primary navigation")
	}
	tt.Key(0, ui.KeyHome)
	if w.detailMode != "Overview" {
		t.Fatal("Home did not return to overview")
	}
	saveNativeScreenshot(t, tt)
}

func TestNativeRedesignEventsRefreshKeepsObjectIdentity(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/namespaces/team/events" || r.URL.Query().Get("fieldSelector") != "involvedObject.uid=pod-uid" {
			t.Errorf("unexpected event request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", 400)
			return
		}
		n := reads.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"apiVersion":"v1","kind":"EventList","items":[{"apiVersion":"v1","kind":"Event","metadata":{"name":"sample"},"reason":"Observed","message":"revision-%d"}]}`, n)
	}))
	defer server.Close()
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.click("Events")
	h.pump(func() bool { return h.w.eventsText == "Observed: revision-1" })
	h.click("Refresh events")
	h.pump(func() bool { return h.w.eventsText == "Observed: revision-2" })
	if reads.Load() != 2 || h.w.detail.GetUID() != "pod-uid" {
		t.Fatal("refresh changed identity or duplicated reads")
	}
}

func TestNativeRedesignLogDockPreservesOverviewAndClosesStream(t *testing.T) {
	for _, scale := range []float32{1, 1.5, 2} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.detail, w.detailKind, w.detailMode = modelObject("dock-uid", "payments"), catalog()[0], "Overview"
			w.containers = []string{"app"}
			w.container = "app"
			w.logRows = []string{"stream connected", "request completed"}
			tt := ui.NewTester(w.View, 1100, 700)
			tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			if err := tt.Click("Diagnostics"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("Logs"); err != nil {
				t.Fatal(err)
			}
			overview, hasOverview := tt.Find("Object overview")
			dock, hasDock := tt.Find("Log dock")
			if !hasOverview || !hasDock || overview.H <= 0 || dock.H <= 0 || overview.Y+overview.H > dock.Y || dock.Y+dock.H > 700 {
				t.Fatalf("missing layered log workspace: overview=%+v dock=%+v", overview, dock)
			}
			for _, label := range []string{"Close logs", "Follow logs", "Previous logs", "Stop logs", "Container"} {
				r, ok := tt.Find(label)
				if !ok || r.H <= 0 || r.Y < dock.Y || r.Y+r.H > dock.Y+dock.H {
					t.Fatalf("log action %s outside dock: %+v dock=%+v", label, r, dock)
				}
			}
			output, ok := tt.Find("Log output")
			if !ok || output.H < 32 || output.Y+output.H > dock.Y+dock.H {
				t.Fatalf("log output is not readable: %+v", output)
			}
			if w.logCancel != nil {
				t.Fatal("opening dock started streaming")
			}
			ctx, cancel := context.WithCancel(context.Background())
			w.logCancel = cancel
			saveNativeScreenshot(t, tt)
			if err := tt.Click("Close logs"); err != nil {
				t.Fatal(err)
			}
			if ctx.Err() == nil || w.logCancel != nil || w.detailMode != "Overview" {
				t.Fatal("closing dock retained stream or lost overview")
			}
		})
	}
}

func TestNativeRedesignLogDockMinimumRemainsScrollable(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.detail, w.detailKind, w.detailMode = modelObject("resize-uid", "payments"), catalog()[0], "Logs"
			w.containers = []string{"app"}
			w.container = "app"
			w.logRows = []string{"log line"}
			tt := ui.NewTester(w.View, 1100, 700)
			tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			initialAction, _ := tt.Find("Stop logs")
			if err := tt.Click("Divider"); err != nil {
				t.Fatal(err)
			}
			for range 100 {
				tt.Key(0, ui.KeyDown)
			}
			tt.Frame()
			dock, ok := tt.Find("Log dock")
			if !ok || dock.H > 45*scale {
				t.Fatalf("keyboard did not shrink the dock: %+v", dock)
			}
			reachable := false
			for range 100 {
				action, ok := tt.Find("Stop logs")
				if ok && action.W > 0 && action.H >= initialAction.H && action.Y >= dock.Y && action.Y+action.H <= dock.Y+dock.H {
					reachable = true
					break
				}
				tt.Scroll(dock.X+2, dock.Y+dock.H/2, 0, 4)
				tt.Frame()
			}
			if !reachable {
				saveNativeScreenshot(t, tt)
				t.Fatal("Stop logs is clipped and cannot be reached by scrolling the minimum dock")
			}
			if err := tt.Click("Stop logs"); err != nil {
				t.Fatal(err)
			}
			if w.logStatus != "Stopped" {
				saveNativeScreenshot(t, tt)
				t.Fatalf("scrolled action did not activate: status=%q", w.logStatus)
			}
			if err := tt.Click("Divider"); err != nil {
				t.Fatal(err)
			}
			for range 100 {
				tt.Key(0, ui.KeyUp)
			}
			tt.Frame()
			output, ok := tt.Find("Log output")
			if !ok || output.H < 80 || w.detail.GetUID() != "resize-uid" {
				t.Fatalf("expanding the dock lost output or identity: %+v", output)
			}
			saveNativeScreenshot(t, tt)
		})
	}
}

func TestNativeRedesignDiagnosticDockKeepsObjectAndClosesSession(t *testing.T) {
	for _, mode := range []string{"Terminal", "Command", "Forward"} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprintf("%s/%g", mode, scale), func(t *testing.T) {
				w := New()
				t.Cleanup(w.Close)
				w.detail, w.detailKind, w.detailMode = modelObject("diagnostic-uid", "payments"), catalog()[0], "Overview"
				w.containers = []string{"app"}
				tt := ui.NewTester(w.View, 1100, 700)
				tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
				if err := tt.Click("Diagnostics"); err != nil {
					t.Fatal(err)
				}
				item := mode
				if mode == "Forward" {
					item = "Port forward"
				}
				if err := tt.Click(item); err != nil {
					t.Fatal(err)
				}
				overview, hasOverview := tt.Find("Object overview")
				dock, hasDock := tt.Find("Diagnostic dock")
				if !hasOverview || !hasDock || overview.H <= 0 || dock.H <= 0 || overview.Y+overview.H > dock.Y || dock.Y+dock.H > 700 {
					t.Fatalf("missing layered diagnostic workspace: overview=%+v dock=%+v", overview, dock)
				}
				if w.commandActive || w.terminalActive || w.forwardActive {
					t.Fatal("opening a diagnostic started a session")
				}
				if err := tt.Click("Expand diagnostic"); err != nil {
					t.Fatal(err)
				}
				expanded, ok := tt.Find("Diagnostic dock")
				if !ok || expanded.H <= dock.H {
					t.Fatal("diagnostic did not gain space")
				}
				if _, visible := tt.Find("Object overview"); visible {
					t.Fatal("expanded diagnostic still exposes overview")
				}
				dock = expanded
				close, ok := tt.Find("Close diagnostic")
				if !ok || close.W <= 0 || close.H <= 0 || close.Y < dock.Y || close.Y+close.H > dock.Y+dock.H {
					t.Fatalf("close control outside dock: %+v", close)
				}
				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(cancel)
				switch mode {
				case "Terminal":
					w.terminalCancel, w.terminalActive = cancel, true
				case "Command":
					w.commandCancel, w.commandActive = cancel, true
				case "Forward":
					w.forwardCancel, w.forwardActive = cancel, true
				}
				tt.Frame()
				saveNativeScreenshot(t, tt)
				if err := tt.Click("Close diagnostic"); err != nil {
					t.Fatal(err)
				}
				if ctx.Err() == nil || w.detailMode != "Overview" || w.detail.GetUID() != "diagnostic-uid" || w.commandActive || w.terminalActive || w.forwardActive {
					t.Fatal("closing diagnostic retained session or lost object")
				}
			})
		}
	}
}

func TestNativeRedesignDiagnosticOutputVisibleWithoutScrolling(t *testing.T) {
	for _, mode := range []string{"Command", "Terminal"} {
		for _, size := range [][2]int{{1100, 700}, {1440, 960}} {
			t.Run(fmt.Sprintf("%s/%dx%d", mode, size[0], size[1]), func(t *testing.T) {
				w := New()
				t.Cleanup(w.Close)
				w.detail, w.detailKind, w.detailMode = modelObject("output-uid", "payments"), catalog()[0], mode
				w.containers = []string{"app"}
				w.commandArgv, w.terminalArgv = `["id"]`, `["/bin/sh"]`
				tt := ui.NewTester(w.View, size[0], size[1])
				tt.Frame()
				dock, _ := tt.Find("Diagnostic dock")
				output, ok := tt.Find(mode + " output")
				if !ok || output.W < 200 || output.H < 100 || output.Y < dock.Y || output.Y+output.H > dock.Y+dock.H {
					saveNativeScreenshot(t, tt)
					t.Fatalf("output requires scrolling: output=%+v dock=%+v", output, dock)
				}
				labels := []string{"Command container", "Command argv JSON", "Confirm Pod for command", "Run command"}
				if mode == "Terminal" {
					labels = []string{"Terminal container", "Terminal argv JSON", "Confirm Pod for terminal", "Open terminal"}
				}
				for _, label := range labels {
					r, ok := tt.Find(label)
					if !ok || r.W < 40 || r.H < 12 || r.Y < dock.Y || r.Y+r.H > dock.Y+dock.H {
						t.Fatalf("input %s not visible: %+v", label, r)
					}
				}
				saveNativeScreenshot(t, tt)
			})
		}
	}
}

func TestNativeRedesignReviewDialogCancelsUnsentPlan(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.URL.Query().Get("dryRun") != "All" {
			writes.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(applyUIObject())
	}))
	defer server.Close()
	h := newRelationshipHarness(t, 10*time.Second)
	attachApplyUI(t, h, server.URL)
	data, err := json.Marshal(applyUIObject())
	if err != nil {
		t.Fatal(err)
	}
	h.w.setEditor(string(data))
	h.w.detailMode = "Edit"
	original := h.w.editor
	h.tt.Frame()
	h.click("Preview edit")
	h.pump(func() bool { return h.w.plan != nil })
	panel, ok := h.tt.Find("Review change")
	if !ok || panel.W <= 0 || panel.H <= 0 {
		t.Fatal("review is not a modal dialog")
	}
	h.tt.Type("apply-example")
	if h.w.confirmation != "apply-example" {
		t.Fatal("review did not focus confirmation")
	}
	h.tt.Key(0, ui.KeyEscape)
	h.tt.Frame()
	if h.w.plan != nil || h.w.confirmation != "" || h.w.diff != "" || h.w.detailMode != "Edit" || h.w.editor != original || writes.Load() != 0 {
		t.Fatal("Escape retained authorization, lost draft, or submitted write")
	}
	h.tt.Key(0, ui.KeyEnter)
	h.pump(func() bool { return h.w.plan != nil })
	saveNativeScreenshot(t, h.tt)
	h.click("Cancel review")
	if h.w.plan != nil || h.w.detailMode != "Edit" || writes.Load() != 0 {
		t.Fatal("cancel did not revoke preview")
	}
}

func TestNativeRedesignReviewClosePreservesSubmittedOutcome(t *testing.T) {
	for _, pending := range []int{0, 1} {
		for _, escape := range []bool{false, true} {
			t.Run(fmt.Sprintf("pending-%d/escape-%t", pending, escape), func(t *testing.T) {
				w := New()
				t.Cleanup(w.Close)
				w.detail, w.detailKind, w.detailMode = modelObject("outcome-uid", "payments"), catalog()[0], "Diff"
				w.reviewReturnMode = "Overview"
				w.pendingWrites = pending
				w.detailMessage = "Unknown: response lost"
				if pending > 0 {
					w.detailMessage = "Submitting reviewed change…"
				}
				message := w.detailMessage
				tt := ui.NewTester(w.View, 1100, 700)
				if escape {
					tt.Key(0, ui.KeyEscape)
				} else if err := tt.Click("Close review"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				if w.detailMode != "Overview" || w.pendingWrites != pending || w.detailMessage != message || w.plan != nil {
					t.Fatal("closing review erased outcome or changed submitted write")
				}
			})
		}
	}
}

func TestNativeRedesignReviewKeepsDiagnosticBackground(t *testing.T) {
	for mode, label := range map[string]string{"Logs": "Log output", "Terminal": "Terminal container", "Command": "Command container", "Forward": "Remote port"} {
		t.Run(mode, func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.detail, w.detailKind, w.detailMode = modelObject("review-uid", "payments"), catalog()[0], "Diff"
			w.reviewReturnMode = mode
			w.containers = []string{"app"}
			tt := ui.NewTester(w.View, 1100, 700)
			if _, ok := tt.Find(label); !ok {
				t.Fatalf("review lost %s background", mode)
			}
		})
	}
}

func TestNativeRedesignReviewMinimumWindowAndFontScale(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 1.5, 2} {
		for _, rtl := range []bool{false, true} {
			t.Run(fmt.Sprintf("scale-%g/rtl-%t", scale, rtl), func(t *testing.T) {
				w := New()
				t.Cleanup(w.Close)
				w.detail, w.detailKind, w.detailMode = modelObject("scaled-review-uid", "payments"), catalog()[0], "Diff"
				w.reviewReturnMode = "Overview"
				w.detailMessage = "Unknown: the server response was lost. Verify the resource before preparing another change."
				w.diff = "spec.replicas: 2 → 3\nmetadata.labels.owner: 平台团队\n"
				tt := ui.NewTester(w.View, 1100, 700)
				tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
				if rtl {
					tt.SetDirection(ui.RightToLeft)
				}
				tt.Frame()
				panel, ok := tt.Find("Review change")
				if !ok || panel.W <= 0 || panel.H <= 0 || panel.X < 0 || panel.Y < 0 || panel.X+panel.W > 1100 || panel.Y+panel.H > 700 {
					t.Fatalf("review outside window: %+v", panel)
				}
				for _, label := range []string{"Change diff", "Close review"} {
					r, ok := tt.Find(label)
					if !ok || r.W <= 0 || r.H < 12 || r.X < panel.X || r.Y < panel.Y || r.X+r.W > panel.X+panel.W || r.Y+r.H > panel.Y+panel.H {
						t.Fatalf("review control %s clipped: %+v panel=%+v", label, r, panel)
					}
					if label == "Change diff" && r.H < 80 {
						t.Fatalf("diff unreadable: %+v", r)
					}
				}
				for _, label := range []string{"Confirm resource name", "Execute reviewed change"} {
					if _, ok := tt.Find(label); ok {
						t.Fatalf("submitted result retains inactive %s", label)
					}
				}
				saveNativeScreenshot(t, tt)
				tt.Key(0, ui.KeyEscape)
				tt.Frame()
				if w.detailMode != "Overview" || w.detailMessage == "" {
					t.Fatal("scaled review lost result on Escape")
				}
			})
		}
	}
}

func TestNativeDiagnosticCommandKeepsSelectedPodIdentity(t *testing.T) {
	var reads atomic.Int32
	pod := modelObject("pod-uid", "pod")
	pod.Object["spec"] = map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "app", "image": "fixture"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/team/pods/pod" {
			reads.Add(1)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(pod)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/team/pods/pod/exec" {
			http.Error(w, "fixture rejects exec", 400)
			return
		}
		t.Errorf("diagnostic retargeted: %s %s", r.Method, r.URL)
		http.Error(w, "unexpected target", 400)
	}))
	defer server.Close()
	h := newRelationshipHarness(t, 10*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.detail = pod
	h.w.containers = []string{"app"}
	h.click("Diagnostics")
	if err := h.tt.Click("Command"); err != nil {
		t.Fatal(err)
	}
	h.click("Confirm Pod for command")
	h.tt.Type("pod")
	h.w.detail = modelObject("next-uid", "next-pod")
	h.w.containers = []string{"other"}
	h.tt.Frame()
	h.click("Run command")
	h.pump(func() bool { return reads.Load() > 0 && !h.w.commandActive })
	if h.w.detail.GetUID() != "next-uid" || h.w.commandContainer != "app" {
		t.Fatal("diagnostic changed the browsed object or container")
	}
}

func TestNativeResourceFiltersPreserveDraftAndSearch(t *testing.T) {
	w := New()
	w.status = "Live"
	t.Cleanup(w.Close)
	w.store = newRowStore()
	w.store.event(kube.ResourceEvent{Type: watch.Added, Object: modelObject("api", "api-工作负载")})
	w.store.event(kube.ResourceEvent{Type: watch.Added, Object: modelObject("worker", "worker")})
	tt := ui.NewTester(w.View, 1100, 700)
	if _, ok := tt.Find("Label selector"); ok {
		t.Fatal("advanced filter takes space before requested")
	}
	if err := tt.Click("Filter resources"); err != nil {
		t.Fatal(err)
	}
	tt.Type("工作负载")
	snapshot, _ := w.store.snapshot()
	if len(snapshot.Rows) != 1 || snapshot.Rows[0].Name != "api-工作负载" {
		t.Fatal("native search did not update the resource projection")
	}
	tt.Key(0, ui.KeyEscape)
	snapshot, _ = w.store.snapshot()
	if w.filter != "" || len(snapshot.Rows) != 2 {
		t.Fatal("Escape did not clear search and restore the projection")
	}
	if err := tt.Click("Filters"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Label selector"); err != nil {
		t.Fatal(err)
	}
	tt.Type("app=api")
	epoch := w.scopeEpoch
	if err := tt.Click("Filters"); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.Find("Label selector"); ok {
		t.Fatal("filter did not collapse")
	}
	if err := tt.Click("Filters"); err != nil {
		t.Fatal(err)
	}
	if w.labelSelector != "app=api" || w.scopeEpoch != epoch {
		t.Fatal("disclosure lost the draft or applied it without consent")
	}
	if err := tt.Click("Filter resources"); err != nil {
		t.Fatal(err)
	}
	tt.Type("worker")
	if err := tt.Click("Clear"); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = w.store.snapshot()
	if w.filter != "" || len(snapshot.Rows) != 2 {
		t.Fatal("clear button did not update projection")
	}
	tt.Type("api")
	if w.filter != "api" {
		t.Fatal("clear button lost keyboard focus")
	}
	saveNativeScreenshot(t, tt)
}

func TestNativeSidebarReplacesDuplicateResourceSelect(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		t.Run(fmt.Sprintf("scale-%g", scale), func(t *testing.T) {
			h := newRelationshipHarness(t, 15*time.Second)
			for i := range 50 {
				h.w.kinds = append(h.w.kinds, kube.ResourceKind{GVR: schema.GroupVersionResource{Group: "fixture.example", Version: "v1", Resource: fmt.Sprintf("fixtures%d", i)}, Kind: fmt.Sprintf("Fixture%02d", i), Namespaced: true})
			}
			h.tt.SetSize(1100, 700)
			h.tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			h.selectSidebarResource("Fixture49")
			if h.w.currentKind.GVR.Resource != "fixtures49" {
				t.Fatal("sidebar did not select the distant CRD")
			}
			h.tt.Key(0, ui.KeyUp)
			h.tt.Key(0, ui.KeyEnter)
			if h.w.currentKind.GVR.Resource != "fixtures48" {
				t.Fatal("sidebar keyboard input did not select the previous CRD")
			}
			h.selectSidebarResource("ConfigMaps")
			if h.w.currentKind.GVR.Resource != "configmaps" {
				t.Fatal("sidebar did not return to standard resources")
			}
			if _, ok := h.tt.Find("Resource kind"); ok {
				t.Fatal("duplicate resource picker remains")
			}
			saveNativeScreenshot(t, h.tt)
		})
	}
}

func TestNativeLabelSelectorRequiresExplicitApply(t *testing.T) {
	requests := make(chan string, 20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
			http.Error(w, "unexpected", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("watch") == "true" {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		requests <- r.URL.Query().Get("labelSelector")
		fmt.Fprint(w, `{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"1"},"items":[]}`)
	}))
	t.Cleanup(server.Close)
	h := newRelationshipHarness(t, 15*time.Second)
	attachRelationshipBackend(t, h, server.URL)
	h.w.namespace = "team"
	h.w.clearDetail()
	h.tt.Frame()
	waitQuery := func(want string) {
		t.Helper()
		got := "not received"
		h.pump(func() bool {
			select {
			case got = <-requests:
				return true
			default:
				return false
			}
		})
		if got != want {
			t.Fatalf("query selector=%q, want %q", got, want)
		}
		h.pump(func() bool { return h.w.status == "Live" })
	}
	h.click("Filters")
	h.click("Label selector")
	h.tt.Type("app=api")
	if h.tt.HasText("Filters (1)") {
		t.Fatal("unsubmitted draft is shown as an applied filter")
	}
	h.click("Refresh")
	waitQuery("")
	h.click("Apply selector")
	waitQuery("app=api")
	if !h.tt.HasText("Filters (1)") {
		t.Fatal("applied selector has no collapsed-state indicator")
	}
	h.click("Label selector")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("app=worker")
	h.click("Filters")
	h.selectSidebarResource("ConfigMaps")
	waitQuery("app=api")
	h.click("Filters")
	if !h.tt.HasText("Not applied") {
		t.Fatal("changed draft is not distinguished from the active selector")
	}
	h.click("Apply selector")
	waitQuery("app=worker")
	h.click("Label selector")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Key(0, ui.KeyBackspace)
	h.click("Refresh")
	waitQuery("app=worker")
	h.click("Apply selector")
	waitQuery("")
	if h.tt.HasText("Filters (1)") {
		t.Fatal("cleared applied selector retains an active count")
	}
	h.click("Label selector")
	h.tt.Type("app=retain-draft")
	h.click("Disconnect")
	if h.w.labelSelector != "app=retain-draft" || h.tt.HasText("Filters (1)") {
		t.Fatal("disconnect lost draft or retained an active filter badge")
	}
	saveNativeScreenshot(t, h.tt)
}

func TestNativeWorkbenchTypographyFollowsTextScale(t *testing.T) {
	for _, object := range []bool{false, true} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("object-%t-dark-%t", object, dark), func(t *testing.T) {
				w := New()
				t.Cleanup(w.Close)
				w.activeContext, w.activeNamespace = "lab", "team"
				w.status = "Live"
				w.pendingWrites = 1
				labels := []string{"Pod", "0 / 0 resources", "1 active write(s)"}
				if object {
					w.detail, w.detailKind = modelObject("uid", "api-worker"), catalog()[0]
					w.detailMode = "Overview"
					labels = []string{"api-worker", "lab / team / Pod", "1 active write(s)"}
				}
				tt := ui.NewTester(w.View, 1100, 700)
				tt.SetDark(dark)
				heights := map[string]float32{}
				for _, scale := range []float32{1, 1.25, 1.5, 2} {
					tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
					for _, label := range labels {
						r, ok := tt.Find(label)
						if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1100 || r.Y+r.H > 700 {
							saveNativeScreenshot(t, tt)
							t.Fatalf("%q outside viewport at %g: %+v", label, scale, r)
						}
						if scale == 1 {
							heights[label] = r.H
						} else if r.H < heights[label]*scale*0.9 {
							saveNativeScreenshot(t, tt)
							t.Fatalf("%q did not scale: base=%g scale=%g height=%g", label, heights[label], scale, r.H)
						}
					}
				}
				if object {
					if err := tt.Click("YAML"); err != nil {
						t.Fatal(err)
					}
					if w.detailMode != "YAML" {
						t.Fatal("scaled object tabs cannot be used")
					}
				}
				saveNativeScreenshot(t, tt)
			})
		}
	}
}

func TestNativeObjectHeaderReservesTitleAndKeepsActions(t *testing.T) {
	for _, scale := range []float32{1, 2, 2.5} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			w := New()
			t.Cleanup(w.Close)
			w.detail, w.detailKind = modelObject("header-uid", "api-worker"), catalog()[0]
			w.detailMode = "Overview"
			tt := ui.NewTester(w.View, 1100, 700)
			tt.SetPreferences(ui.Preferences{TextScale: scale, ReduceMotion: true})
			title, ok := tt.Find("api-worker")
			workspace, _ := tt.Find("Object workspace")
			if !ok || title.W < workspace.W-40 {
				saveNativeScreenshot(t, tt)
				t.Fatalf("actions squeeze the object title: title=%+v workspace=%+v", title, workspace)
			}
			if err := tt.Click("Diagnostics"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click("Logs"); err != nil {
				t.Fatal(err)
			}
			if w.diagnostic == nil || w.diagnostic.target.UID != "header-uid" {
				t.Fatal("native toolbar menu did not open the bound diagnostic")
			}
			saveNativeScreenshot(t, tt)
			if _, ok := tt.Find("More"); ok {
				if err := tt.Click("More"); err != nil {
					t.Fatal(err)
				}
				if err := tt.ChooseMenuItem("Close detail"); err != nil {
					t.Fatal(err)
				}
			} else if err := tt.Click("Close detail"); err != nil {
				t.Fatal(err)
			}
			if w.detail != nil || w.diagnostic != nil {
				t.Fatal("toolbar close retained object or diagnostic")
			}
		})
	}
}

func TestNativeLogDockKeepsContainerRowVisible(t *testing.T) {
	w := New()
	t.Cleanup(w.Close)
	w.detail, w.detailKind, w.detailMode = modelObject("dock-container", "worker"), catalog()[0], "Overview"
	w.detail.Object["spec"] = map[string]any{"containers": []any{map[string]any{"name": "app", "image": "fixture"}}}
	w.detail.Object["status"] = map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "app", "ready": false, "restartCount": int64(1), "state": map[string]any{"running": map[string]any{}}}}}
	w.containers, w.container = []string{"app"}, "app"
	tt := ui.NewTester(w.View, 1440, 900)
	if err := tt.Click("Diagnostics"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Logs"); err != nil {
		t.Fatal(err)
	}
	overview, _ := tt.Find("Object overview")
	row, ok := tt.Find("Inspect container app")
	if !ok || row.H < 20 || row.Y < overview.Y || row.Y+row.H > overview.Y+overview.H {
		saveNativeScreenshot(t, tt)
		t.Fatalf("default split hides container row: row=%+v overview=%+v", row, overview)
	}
	output, ok := tt.Find("Log output")
	if !ok || output.H < 80 {
		t.Fatalf("log output lost space: %+v", output)
	}
	saveNativeScreenshot(t, tt)
}
