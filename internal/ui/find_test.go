package uiworkbench

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
)

const findDraft = "# 中文 🚢\r\napiVersion: v1\r\nkind: Pod\r\nmetadata:\r\n  name: 🚢match\r\nspec: {label: 🚢match}\r\n"

func findHarness(t *testing.T) *relationshipHarness {
	t.Helper()
	h := schemaHarness(t, "https://example.invalid")
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(findDraft)
	h.click("Find")
	h.click("Find text")
	h.tt.Type("🚢match")
	if h.w.find.active || h.w.find.result != nil {
		t.Fatal("typing implicitly searched")
	}
	return h
}
func findSearch(h *relationshipHarness) {
	h.t.Helper()
	h.click("Search")
	h.pump(func() bool { return !h.w.find.busy })
	if h.w.find.result == nil {
		h.t.Fatal(h.w.find.status)
	}
}

func TestNativeFindLiteralNavigationUndoAndWrap(t *testing.T) {
	h := findHarness(t)
	findSearch(h)
	before := h.w.editorText()
	if h.w.find.result.Len() != 2 || !h.tt.Focused("Manifest editor") || h.w.plan != nil {
		t.Fatal("search did not select first result without a plan")
	}
	first, _ := h.w.find.result.At(0)
	h.w.detailMessage = "Unrelated status"
	h.tt.Frame()
	h.tt.Type("fixed")
	if h.w.editorText() != before[:first.ByteStart]+"fixed"+before[first.ByteEnd:] {
		t.Fatal("selection not Unicode-correct")
	}
	if h.w.find.result != nil || h.w.find.jump != nil {
		t.Fatal("typing kept stale matches")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("find broke same-document Undo")
	}
	findSearch(h)
	h.click("Next match")
	if h.w.find.selected != 1 {
		t.Fatal("Next did not advance")
	}
	h.click("Next match")
	if h.w.find.selected != 0 || !strings.Contains(h.w.find.status, "wrapped") {
		t.Fatal("forward wrap not shown")
	}
	h.click("Previous match")
	if h.w.find.selected != 1 || !strings.Contains(h.w.find.status, "wrapped") {
		t.Fatal("reverse wrap not shown")
	}
	h.click("Close find")
	if h.w.find.open || h.w.find.query != "" || h.w.find.result != nil {
		t.Fatal("Close retained query/results")
	}
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft {
		t.Fatal("Find reset original editor history")
	}
}
func TestNativeFindQueuedResultsCannotCrossEpochs(t *testing.T) {
	for _, change := range []string{"query", "draft", "raw-draft", "detail", "connection", "revision", "session", "tab", "expiry", "credentials", "close-find", "window"} {
		t.Run(change, func(t *testing.T) {
			h := findHarness(t)
			h.click("Search")
			var complete func()
			select {
			case complete = <-h.updates:
			case <-h.ctx.Done():
				t.Fatal("completion missing")
			}
			switch change {
			case "query":
				h.click("Find text")
				h.tt.Type("x")
			case "draft":
				h.click("Manifest editor")
				h.tt.Type("x")
			case "raw-draft":
				h.w.setEditor(h.w.editorText() + "unrendered")
			case "detail":
				h.w.clearDetail()
			case "connection":
				h.w.contextEpoch++
			case "revision":
				h.w.draftRevision++
			case "session":
				h.w.sessionID = "new-identity"
			case "tab":
				h.w.detailMode = "YAML"
			case "expiry":
				h.w.expireConnection(h.w.contextEpoch)
			case "credentials":
				h.w.vaultOpen = true
			case "close-find":
				h.click("Close find")
			case "window":
				h.w.Close()
			}
			before := h.w.editorText()
			complete()
			h.tt.Frame()
			if h.w.editorText() != before || h.w.find.result != nil || h.w.find.jump != nil || h.w.find.active {
				t.Fatal("stale result/navigation leaked")
			}
		})
	}
}
func TestNativeFindSingleAdmissionAndOneShotSelection(t *testing.T) {
	h := findHarness(t)
	h.click("Search")
	var old func()
	select {
	case old = <-h.updates:
	case <-h.ctx.Done():
		t.Fatal("completion missing")
	}
	h.click("Close find")
	h.click("Find")
	h.click("Find text")
	h.tt.Type("kind")
	epoch := h.w.find.epoch
	h.w.closeFind(epoch - 1)
	if !h.w.find.open || h.w.find.query != "kind" {
		t.Fatal("stale Close cleared a newer query")
	}
	h.w.startFind()
	if h.w.find.epoch != epoch || !h.w.find.busy {
		t.Fatal("cancel reopened admission before join")
	}
	old()
	h.tt.Frame()
	if h.w.find.busy || h.w.find.result != nil {
		t.Fatal("old completion retained admission or published")
	}
	findSearch(h)
	h.click("Find text")
	h.tt.Frame()
	if h.tt.Focused("Manifest editor") {
		t.Fatal("selection repeated on a later frame")
	}
}
func TestNativeFindCompositionDefersAndRejectsChangedDraft(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "commit"}[commit], func(t *testing.T) {
			h := findHarness(t)
			findSearch(h)
			h.click("Manifest editor")
			h.tt.Compose("未提交", 3)
			before := h.w.editorText()
			h.w.queueFindJump(h.w.find.epoch, 1, false)
			h.tt.Frame()
			if h.w.editorText() != before || h.w.find.jump == nil || !h.w.find.jump.deferred {
				t.Fatal("composition not deferred")
			}
			h.tt.Compose("", 0)
			if commit {
				h.tt.Type("提交")
			}
			h.tt.Frame()
			if h.w.find.jump != nil {
				t.Fatal("navigation never settled")
			}
			if commit && h.w.find.result != nil {
				t.Fatal("committed composition kept old matches")
			}
			if !commit && (h.w.find.result == nil || !h.tt.Focused("Manifest editor")) {
				t.Fatal("canceled composition failed to apply deferred intent")
			}
		})
	}
}
func TestNativeFindReadOnlyBudgetsAndMinimumWindow(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); schemaServer(w, r) }))
	defer s.Close()
	h := schemaHarness(t, s.URL)
	h.click("Check draft")
	h.pump(func() bool { return !h.w.schemaActive })
	count := calls.Load()
	h.click("Find")
	h.click("Find text")
	h.tt.Type("name")
	findSearch(h)
	h.tt.SetSize(1100, 700)
	h.tt.Frame()
	for _, label := range []string{"Manifest editor", "Find text", "Search", "Previous match", "Next match", "Close find", "Find status", "Schema assistance", "Go to location", "Preview edit", "Apply", "Close detail", "Preview delete"} {
		r, ok := h.tt.Find(label)
		if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1100 || r.Y+r.H > 654 {
			t.Errorf("%s outside minimum window: %+v %v", label, r, ok)
		}
	}
	saveNativeScreenshot(t, h.tt)
	if calls.Load() != count || h.w.plan != nil || h.w.editorText() != schemaDraft {
		t.Fatal("Find read network, changed draft, or created a plan")
	}
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(strings.Repeat("x", manifest.MaxFindMatches+1))
	h.click("Find text")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("x")
	findSearch(h)
	if h.w.find.result.Len() != manifest.MaxFindMatches || !strings.Contains(h.w.find.status, "truncated") {
		t.Fatal("truncation not shown")
	}
	h.click("Previous match")
	if !strings.Contains(h.w.find.status, "wrapped") {
		t.Fatal("retained-set wrap not shown")
	}
	h.click("Find text")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(strings.Repeat("PRIVATE", 200))
	h.click("Search")
	h.pump(func() bool { return !h.w.find.busy })
	if strings.Contains(h.w.find.status, "PRIVATE") || h.w.find.result != nil {
		t.Fatal("untrusted query reflected or excess budget accepted")
	}
	h.click("YAML")
	if h.w.find.open || h.w.find.result != nil || h.w.find.query != "" {
		t.Fatal("hidden Find retained state")
	}
}
func TestNativeFindQueuedSelectionCannotCrossDraftOrQuery(t *testing.T) {
	for _, change := range []string{"draft", "query", "connection", "close", "secret"} {
		t.Run(change, func(t *testing.T) {
			h := findHarness(t)
			findSearch(h)
			h.w.queueFindJump(h.w.find.epoch, 1, false)
			switch change {
			case "draft":
				h.w.setEditor("new document")
			case "query":
				h.w.find.query = "new query"
			case "connection":
				h.w.contextEpoch++
			case "close":
				h.w.clearDetail()
			case "secret":
				h.w.detail.SetKind("Secret")
			}
			before := h.w.editorText()
			h.tt.Frame()
			if h.w.editorText() != before || h.w.find.jump != nil || h.w.find.result != nil {
				t.Fatal("queued selection crossed identity")
			}
		})
	}
}

func TestNativeFindDeferredSelectionLosesFocusAndPreservesTruncation(t *testing.T) {
	h := findHarness(t)
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(strings.Repeat("🚢match", manifest.MaxFindMatches+1))
	findSearch(h)
	h.click("Manifest editor")
	h.tt.Compose("暂", 1)
	h.w.stepFind(h.w.find.epoch, -1)
	h.tt.Frame()
	if h.w.find.jump == nil || !h.w.find.jump.deferred || !strings.Contains(h.w.find.status, "truncated") || !strings.Contains(h.w.find.status, "wrapped") {
		t.Fatal("deferred navigation obscured truncation/wrap")
	}
	h.tt.Compose("", 0)
	h.tt.Frame()
	if !strings.Contains(h.w.find.status, "truncated") || !strings.Contains(h.w.find.status, "wrapped") {
		t.Fatal("settled navigation obscured retained-range boundaries")
	}
	h.click("Find text")
	h.w.queueFindJump(h.w.find.epoch, 0, false)
	h.w.find.jump.deferred = true
	before := h.w.editorText()
	h.tt.Frame()
	if h.tt.Focused("Manifest editor") || h.w.editorText() != before || h.w.find.jump != nil {
		t.Fatal("deferred selection stole focus or changed text")
	}
}
