package uiworkbench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
)

// Explicit test inspection only; production consumers use bounded snapshots.
func (w *Workbench) editorText() string {
	if w.editor == nil {
		return ""
	}
	return w.editor.String()
}

// Tests below exercise the actual indexed input. Old string-based assertions
// remain in the regression suites, adapted only to explicit test inspection.
func TestManifestBufferImmutableConcurrentSnapshotsAndExportBounds(t *testing.T) {
	w := New()
	defer w.Close()
	const original = "# 中文 🚢\r\n" + schemaDraft
	w.setEditor(original)
	snapshot := w.editor.Snapshot()
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 50 {
				got, err := manifestSnapshotText(context.Background(), snapshot)
				if err != nil || got != original {
					t.Error("worker snapshot changed")
				}
			}
		})
	}
	for range 50 {
		w.editor.Replace(0, 0, "x")
		w.syncEditor()
	}
	workers.Wait()
	if w.editorText() == original {
		t.Fatal("edits not committed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manifestSnapshotText(ctx, snapshot); err == nil {
		t.Fatal("canceled export")
	}
	for _, text := range []string{strings.Repeat("x", manifest.MaxBytes+1), strings.Repeat("\n", 5001)} {
		w.setEditor(text)
		if got, err := manifestSnapshotText(context.Background(), w.editor.Snapshot()); err == nil || got != "" {
			t.Fatal("unbounded export")
		}
		if w.editorText() != text {
			t.Fatal("oversized draft was silently replaced/truncated")
		}
	}
}

func TestManifestBufferIdleRevisionChecksAllocateNothing(t *testing.T) {
	for _, size := range []int{16, 64 << 10, manifest.MaxBytes} {
		w := New()
		w.setEditor(strings.Repeat("a", size))
		id := w.draftIdentity()
		allocs := testing.AllocsPerRun(1000, func() {
			w.syncEditor()
			if w.draftIdentity() != id {
				t.Fatal("idle revision changed")
			}
		})
		if allocs != 0 {
			t.Fatalf("idle checks at %d bytes allocate %g times", size, allocs)
		}
		w.Close()
	}
}

func TestNativeManifestBufferStableIdentitySnapshotAndUndo(t *testing.T) {
	h := schemaHarness(t, "https://example.invalid")
	h.click("Manifest editor")
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	h.tt.Type("# 中文 🚢\n")
	edited := h.w.editorText()
	for range 5 {
		h.tt.Frame()
	}
	if h.w.editor != buffer || original.String() != schemaDraft || edited == schemaDraft {
		t.Fatal("ordinary edit replaced stable buffer or historical snapshot")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != schemaDraft {
		t.Fatal("native buffer Undo")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != edited {
		t.Fatal("native buffer redo")
	}
	// Same bytes, intentionally different document identity: never resurrect its history.
	h.w.setEditor(edited)
	h.tt.Frame()
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editor == buffer || h.w.editorText() != edited {
		t.Fatal("replacement retained old Undo")
	}
}

func TestNativeManifestBufferPreeditNoOpPreservesSemanticRevision(t *testing.T) {
	h := findHarness(t)
	findSearch(h)
	h.click("Manifest editor")
	before, id, result := h.w.editorText(), h.w.draftIdentity(), h.w.find.result
	h.tt.Compose("未提交", 3)
	h.w.syncEditor()
	if h.w.editorText() != before || h.w.draftIdentity() != id || h.w.find.result != result {
		t.Fatal("preedit root publication invalidated committed text")
	}
	h.w.queueFindJump(h.w.find.epoch, 1, false)
	h.tt.Frame()
	if h.w.find.jump == nil || !h.w.find.jump.deferred {
		t.Fatal("preedit lost deferred navigation")
	}
	h.tt.Compose("", 0)
	h.tt.Frame()
	if h.w.draftIdentity() != id || h.w.find.result != result || h.w.find.jump != nil {
		t.Fatal("canceled preedit changed semantic revision")
	}
	h.tt.Type("commit")
	if h.w.draftIdentity() == id || h.w.find.result != nil {
		t.Fatal("actual native edit retained results")
	}
}

func TestNativeManifestBufferQueuedSchemaSeesUnrenderedEdit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(schemaServer))
	defer server.Close()
	h := schemaHarness(t, server.URL)
	h.w.startSchema(true)
	var done func()
	select {
	case done = <-h.updates:
	case <-h.ctx.Done():
		t.Fatal("no schema result")
	}
	h.w.editor.Replace(0, 0, "# unrendered\n")
	done() // no intervening native frame
	if h.w.schemaSource != nil || h.w.schemaText != "" || h.w.schemaActive {
		t.Fatal("worker published old draft or retained loading state")
	}
	h.w.startSchema(true)
	h.pump(func() bool { return !h.w.schemaActive })
	if h.w.schemaSource == nil {
		t.Fatal("new draft did not get schema result")
	}
}

func TestNativeManifestBufferUnrenderedChangeRevokesPreparedPlan(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.URL.Query().Get("dryRun") != "All" {
			writes.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(applyUIObject())
	}))
	defer server.Close()
	h := newRelationshipHarness(t, 15*time.Second)
	attachApplyUI(t, h, server.URL)
	text, err := manifest.Display(h.w.detail, false)
	if err != nil {
		t.Fatal(err)
	}
	h.w.setEditor(text)
	h.w.detailMode = "Edit"
	// A result queued before a programmatic buffer edit must not produce a plan.
	h.w.prepare("edit")
	var done func()
	select {
	case done = <-h.updates:
	case <-h.ctx.Done():
		t.Fatal("no preview result")
	}
	h.w.editor.Replace(0, 0, "# changed\n")
	done()
	if h.w.plan != nil || h.w.preparing {
		t.Fatal("stale preview published")
	}
	// A completed plan must be invalidated even when execution is called without a frame.
	h.w.prepare("edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan == nil {
		t.Fatal(h.w.detailMessage)
	}
	h.w.confirmation = h.w.plan.Target().Name
	h.w.editor.Replace(0, 0, "# after preview\n")
	h.w.execute()
	if h.w.plan != nil || h.w.pendingWrites != 0 || writes.Load() != 0 {
		t.Fatal("unrendered edit executed old review")
	}
}

func TestNativeManifestBufferOversizeCannotBecomeEmptyPreview(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unexpected", 500) }))
	defer server.Close()
	h := newRelationshipHarness(t, 15*time.Second)
	attachApplyUI(t, h, server.URL)
	text := strings.Repeat("x", manifest.MaxBytes+1)
	h.w.setEditor(text)
	h.w.detailMode = "Edit"
	h.w.prepare("edit")
	if h.w.plan != nil || h.w.preparing || h.w.editorText() != text || calls.Load() != 0 || !strings.Contains(h.w.detailMessage, "budget") {
		t.Fatal("oversized edit lost source or reached preview")
	}
	h.w.startSchema(true)
	h.pump(func() bool { return !h.w.schemaActive })
	if h.w.schemaSource != nil || calls.Load() != 0 || h.w.editorText() != text {
		t.Fatal("oversized schema export reached server or lost source")
	}
	h.w.openFind()
	h.w.find.query = "x"
	h.w.startFind()
	if h.w.find.active || h.w.find.result != nil || h.w.editorText() != text {
		t.Fatal("oversized Find accepted")
	}
}

func BenchmarkManifestBufferIdle(b *testing.B) {
	for _, size := range []int{64 << 10, 256 << 10} {
		b.Run(map[int]string{64 << 10: "64KiB", 256 << 10: "256KiB"}[size], func(b *testing.B) {
			w := New()
			w.setEditor(strings.Repeat("x", size))
			defer w.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				w.syncEditor()
				_ = w.draftIdentity()
			}
		})
	}
}
func BenchmarkManifestBufferSmallEdit(b *testing.B) {
	for _, size := range []int{64 << 10, 256 << 10} {
		b.Run(map[int]string{64 << 10: "64KiB", 256 << 10: "256KiB"}[size], func(b *testing.B) {
			w := New()
			w.setEditor(strings.Repeat("x", size))
			defer w.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				text := "a"
				if i%2 == 0 {
					text = "b"
				}
				w.editor.Replace(0, 1, text)
				w.syncEditor()
			}
		})
	}
}

func TestNativeManifestBufferOversizeIsPreservedUntilExplicitRestore(t *testing.T) {
	h := schemaHarness(t, "https://example.invalid")
	initial := h.w.editorText()
	// An out-of-frame write simulates a native input publication before render.
	h.w.editor.Replace(0, 0, strings.Repeat("x", manifest.MaxBytes+1))
	h.tt.Frame()
	if _, ok := h.tt.Find("Manifest budget exceeded"); !ok {
		t.Fatal("unbounded layout was not suspended")
	}
	if h.w.editor.ByteLen() <= manifest.MaxBytes {
		t.Fatal("oversized draft silently truncated")
	}
	h.tt.SetSize(1100, 700)
	h.tt.Frame()
	saveNativeScreenshot(t, h.tt)
	h.click("Restore prior draft")
	if h.w.editorText() != initial || h.w.plan != nil {
		t.Fatal("explicit restoration failed")
	}
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != initial {
		t.Fatal("restore resurrected superseded oversized history")
	}
}
