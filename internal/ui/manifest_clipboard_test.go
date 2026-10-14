package uiworkbench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/delivery"
	"github.com/laojianzi/aster/internal/manifest"
)

func TestClipboardBudgetConservativeBounds(t *testing.T) {
	for _, limits := range []struct {
		name                string
		bytes, lines, width int
	}{{"resource", manifest.MaxBytes, 5000, maxManifestClipboardLine}, {"delivery", delivery.MaxBytes, delivery.MaxNewlines, maxDeliveryClipboardLine}} {
		t.Run(limits.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, before, incoming string
				risky                  bool
			}{
				{"empty", "", "", false},
				{"unicode-crlf", "# 中文 🚢\r\n", "# é\r\n", false},
				{"byte-limit", "", strings.Repeat("a\n", limits.bytes/2), limits.bytes/2 > limits.lines},
				{"byte-overflow", "", strings.Repeat("x", limits.bytes+1), true},
				{"existing-overflow", strings.Repeat("x", limits.bytes+1), "y", true},
				{"empty-oversized-noop", strings.Repeat("x", limits.bytes+1), "", false},
				{"newline-limit", "\n", strings.Repeat("\n", limits.lines-1), false},
				{"newline-overflow", "\n", strings.Repeat("\n", limits.lines), true},
				{"line-limit", strings.Repeat("a", limits.width/2), strings.Repeat("b", limits.width/2), false},
				{"joined-line-overflow", strings.Repeat("a", limits.width/2), strings.Repeat("b", limits.width/2+1), true},
				{"crlf-counts-bytes", strings.Repeat("a", limits.width-1), "\r\n", false},
				{"unicode-byte-width", strings.Repeat("船", limits.width/3), "🚢", true},
				{"selected-replacement-is-not-subtracted", strings.Repeat("x", limits.width), "y", true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					snapshot := ui.NewTextBuffer(tc.before).Snapshot()
					if got := clipboardPasteExceedsBudget(snapshot, tc.incoming, limits.bytes, limits.lines, limits.width); got != tc.risky {
						t.Fatal("incorrect conservative clipboard limit")
					}
					if snapshot.String() != tc.before {
						t.Fatal("budget check mutated its snapshot")
					}
				})
			}
			// Independent split-line reference over small inputs and deliberately small
			// limits. This also exercises empty trailing lines, Unicode bytes and CRLF.
			values := []string{"", "a", "ab", "a\nb", "\n\n", "\r\n", "船", "🚢", "é", "a\n"}
			for _, a := range values {
				for _, b := range values {
					for bytes := 1; bytes <= 12; bytes++ {
						width := func(s string) int {
							n := 0
							for _, line := range strings.Split(s, "\n") {
								if len(line) > n {
									n = len(line)
								}
							}
							return n
						}
						want := b != "" && (len(a)+len(b) > bytes || len(strings.Split(a, "\n"))+len(strings.Split(b, "\n"))-2 > 2 || width(a)+width(b) > 5)
						if clipboardPasteExceedsBudget(ui.NewTextBuffer(a).Snapshot(), b, bytes, 2, 5) != want {
							t.Fatal("independent clipboard bound disagreement")
						}
					}
				}
			}
		})
	}
	// Oversized incoming text is not flattened/scanned or copied by preflight.
	snapshot := ui.NewTextBuffer(strings.Repeat("x\n", 1024)).Snapshot()
	huge := strings.Repeat("PRIVATE_CLIPBOARD_", manifest.MaxBytes)
	if allocations := testing.AllocsPerRun(30, func() {
		if !clipboardPasteExceedsBudget(snapshot, huge, manifest.MaxBytes, 5000, maxManifestClipboardLine) {
			t.Fatal("accepted giant paste")
		}
	}); allocations != 0 {
		t.Fatalf("oversized preflight allocated %g times", allocations)
	}
}

func TestNativeManifestClipboardPreservesHistoryAndSelection(t *testing.T) {
	h := schemaHarness(t, "https://example.invalid")
	base := "# loaded 中文 🚢\r\n" + schemaDraft
	h.w.setEditor(base)
	h.tt.Frame()
	buffer, original := h.w.editor, h.w.editor.Snapshot()
	h.click("Manifest editor")
	h.tt.Key(ui.Cmd, ui.KeyEnd)
	h.tt.SetClipboard("# clipboard é\r\n")
	h.tt.Key(ui.Cmd, ui.KeyV)
	before := base + "# clipboard é\n"
	if h.w.editorText() != before || h.w.editorRetainedPaste != "" {
		t.Fatal("ordinary paste did not use native insertion/normalization")
	}
	snapshot := buffer.Snapshot()
	for i, incoming := range []string{strings.Repeat("x", manifest.MaxBytes+1), strings.Repeat("\n", 5001), strings.Repeat("PRIVATE_CLIPBOARD_", 600), strings.Repeat("x", 8192)} {
		currentSnapshot, id := buffer.Snapshot(), h.w.draftIdentity()
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.SetClipboard(incoming)
		if i%2 == 0 {
			h.tt.Key(ui.Cmd, ui.KeyV)
		} else {
			h.tt.Command("paste")
		}
		if h.w.editorRetainedPaste != incoming || h.w.editor != buffer || buffer.Snapshot() != currentSnapshot || h.w.draftIdentity() != id {
			t.Fatalf("paste %d: retained=%t buffer=%t root=%t identity=%t text=%t", i, h.w.editorRetainedPaste == incoming, h.w.editor == buffer, buffer.Snapshot() == currentSnapshot, h.w.draftIdentity() == id, h.w.editorText() == before)
		}
		h.tt.Key(ui.Cmd, ui.KeyC)
		if h.tt.Clipboard() != before {
			t.Fatal("clipboard interception changed the native selection")
		}
		h.tt.SetClipboard("second paste")
		h.tt.Command("paste")
		if h.w.editorRetainedPaste != incoming || buffer.Snapshot() != currentSnapshot {
			t.Fatal("second paste replaced the retained input")
		}
		if strings.Contains(h.w.detailMessage, "PRIVATE_CLIPBOARD_") {
			t.Fatal("retained content leaked into status")
		}
		h.tt.Key(ui.Cmd, ui.KeyZ)
		if h.w.editorText() != base || h.w.editorRetainedPaste != incoming {
			t.Fatal("pending retention destroyed native Undo")
		}
		h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
		if h.w.editorText() != before {
			t.Fatal("pending retention destroyed native Redo")
		}
		h.click("Discard retained editor paste")
		if h.w.editorRetainedPaste != "" || h.w.editorText() != before || h.w.editor != buffer {
			t.Fatal("discard replaced the original editor")
		}
		h.click("Manifest editor")
	}
	if original.String() != base || snapshot.String() != before {
		t.Fatal("clipboard handling rewrote an immutable snapshot")
	}
	h.tt.Key(ui.Cmd, ui.KeyZ)
	if h.w.editorText() != base {
		t.Fatal("discard reset mounted history")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	if h.w.editorText() != before {
		t.Fatal("discard reset mounted redo")
	}
	saveNativeScreenshot(t, h.tt)
}

func TestNativeManifestClipboardMinimumWindowAndComposition(t *testing.T) {
	for _, create := range []bool{false, true} {
		name := "edit"
		if create {
			name = "create"
		}
		t.Run(name, func(t *testing.T) {
			h := diagnosticHarness(t)
			h.w.creating = create
			h.tt.SetSize(1100, 700)
			h.click("Manifest editor")
			h.tt.Key(ui.Cmd, ui.KeyHome)
			before, id := h.w.editorText(), h.w.draftIdentity()
			h.tt.Compose("未提交", 3)
			incoming := strings.Repeat("PRIVATE_CLIPBOARD_", 600)
			// The callback/render path itself must not clear a composition. Invoke the
			// exact production handler without a command (which has its own native
			// commitCompose semantics, characterized separately below).
			if !h.w.manifestPasteHandler()(incoming) {
				t.Fatal("risky input was not retained")
			}
			h.tt.Frame()
			if h.w.editorRetainedPaste != incoming || h.w.editorText() != before || h.w.draftIdentity() != id {
				t.Fatal("retention/render changed committed composition source")
			}
			h.w.queueSchemaJump(h.w.schemaEpoch, h.w.schemaLocationChoice)
			h.tt.Frame()
			if !strings.Contains(h.w.schemaStatus, "Finish text composition") {
				t.Fatal("retention/render cleared native composition")
			}
			labels := []string{"Manifest editor", "Manifest retained paste", "Discard retained editor paste", "Check draft", "Close detail"}
			if create {
				labels = append(labels, "Preview create")
			} else {
				labels = append(labels, "Preview edit", "Preview delete")
			}
			for _, label := range labels {
				r, ok := h.tt.Find(label)
				if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > 1088 || r.Y+r.H > 700 {
					t.Errorf("control %s outside minimum window: %+v", label, r)
				}
			}
			saveNativeScreenshot(t, h.tt)
			h.tt.Compose("", 0)
			h.w.discardManifestPaste(h.w.editorPasteEpoch)
			h.tt.Frame()

			// Pinned MyGo commits preedit before dispatching either a key or menu
			// command. Compare against an empty native paste, not a fabricated promise
			// that OnPaste can intercept an earlier composition commit or undo it.
			reference := diagnosticHarness(t)
			reference.w.creating = create
			for _, r := range []*relationshipHarness{h, reference} {
				r.click("Manifest editor")
				r.tt.Key(ui.Cmd, ui.KeyHome)
				r.tt.Compose("未提交", 3)
			}
			h.tt.SetClipboard(incoming)
			reference.tt.SetClipboard("")
			h.tt.Command("paste")
			reference.tt.Command("paste")
			if h.w.editorRetainedPaste != incoming || h.w.editorText() != reference.w.editorText() || h.w.editorText() != "未提交"+before {
				t.Fatal("guard changed native preedit commit semantics or inserted clipboard content")
			}
			for _, r := range []*relationshipHarness{h, reference} {
				r.tt.Compose("追加", 2)
				r.tt.Type("提交")
			}
			committed := h.w.editorText()
			if committed != reference.w.editorText() || h.w.editorRetainedPaste != incoming {
				t.Fatal("pending retention blocked ordinary composition/typing")
			}
			for _, r := range []*relationshipHarness{h, reference} {
				r.tt.Key(ui.Cmd, ui.KeyZ)
			}
			if h.w.editorText() != reference.w.editorText() {
				t.Fatal("retention changed native composition Undo")
			}
			for _, r := range []*relationshipHarness{h, reference} {
				r.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			}
			if h.w.editorText() != committed || h.w.editorText() != reference.w.editorText() {
				t.Fatal("retention changed native composition Redo")
			}
			h.click("Discard retained editor paste")
		})
	}
}

func TestNativeManifestClipboardStaleCallbacksAndTeardown(t *testing.T) {
	for _, name := range []string{"replace", "restore", "close-detail", "disconnect", "expiry", "close"} {
		t.Run(name, func(t *testing.T) {
			h := schemaHarness(t, "https://example.invalid")
			handler := h.w.manifestPasteHandler()
			incoming := strings.Repeat("x", 8193)
			if !handler(incoming) || h.w.editorRetainedPaste != incoming {
				t.Fatal("paste was not retained")
			}
			epoch := h.w.editorPasteEpoch
			switch name {
			case "replace":
				h.w.setEditor(schemaDraft)
			case "restore":
				h.w.editor.Replace(0, h.w.editor.Len(), strings.Repeat("x", manifest.MaxBytes+1))
				h.tt.Frame()
				h.click("Restore prior draft")
			case "close-detail":
				h.w.clearDetail()
			case "disconnect":
				h.w.disconnect()
			case "expiry":
				h.w.expireConnection(h.w.contextEpoch)
			case "close":
				h.w.Close()
			}
			editor, currentEpoch := h.w.editor, h.w.editorPasteEpoch
			snapshot := ui.TextSnapshot{}
			if editor != nil {
				snapshot = editor.Snapshot()
			}
			if h.w.editorRetainedPaste != "" || !handler(incoming) || h.w.editorRetainedPaste != "" || (h.w.editor != editor || editor != nil && editor.Snapshot() != snapshot) || h.w.editorPasteEpoch != currentEpoch {
				t.Fatal("stale paste callback crossed teardown")
			}
			if name == "replace" || name == "restore" {
				if !h.w.manifestPasteHandler()(incoming) {
					t.Fatal("replacement handler did not retain")
				}
				h.w.discardManifestPaste(epoch)
				if h.w.editorRetainedPaste != incoming {
					t.Fatal("stale discard removed another document's retained input")
				}
			}
		})
	}
	t.Run("same-document-unrendered-budget", func(t *testing.T) {
		h := schemaHarness(t, "https://example.invalid")
		handler := h.w.manifestPasteHandler()
		h.w.editor.Replace(0, 0, "# fresh\n")
		if handler("# ordinary\n") {
			t.Fatal("valid same-document edit was mistaken for stale identity")
		}
		h.w.editor.Replace(0, h.w.editor.Len(), strings.Repeat("a", 8190))
		before := h.w.editor.Snapshot()
		if !handler("xyz") || h.w.editorRetainedPaste != "xyz" || h.w.editor.Snapshot() != before {
			t.Fatal("clipboard guard used a stale snapshot")
		}
	})
	t.Run("hidden-view-and-other-workspace", func(t *testing.T) {
		h := schemaHarness(t, "https://example.invalid")
		other := schemaHarness(t, "https://example.invalid")
		handler := h.w.manifestPasteHandler()
		h.w.detailMode = "YAML"
		if !handler(strings.Repeat("x", 8193)) || h.w.editorRetainedPaste != "" {
			t.Fatal("hidden editor accepted stale input")
		}
		h.w.detailMode = "Edit"
		handler(strings.Repeat("x", 8193))
		other.w.manifestPasteHandler()(strings.Repeat("y", 8193))
		h.w.clearDetail()
		if other.w.editorRetainedPaste != strings.Repeat("y", 8193) || other.w.editorText() != schemaDraft {
			t.Fatal("teardown crossed workspace boundary")
		}
	})
}

func TestNativeManifestClipboardRevokesUnsentPreview(t *testing.T) {
	var requests, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
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
	h.tt.Frame()
	h.w.prepare("edit")
	var completed func()
	select {
	case completed = <-h.updates:
	case <-h.ctx.Done():
		t.Fatal("missing queued preview")
	}
	// A queued successful result must not publish after a blocked paste, even
	// after that retained paste is discarded and no draft revision has changed.
	handler := h.w.manifestPasteHandler()
	handler(strings.Repeat("x", 8193))
	h.w.discardManifestPaste(h.w.editorPasteEpoch)
	completed()
	if h.w.plan != nil || h.w.preparing {
		t.Fatal("queued preview escaped clipboard invalidation")
	}
	h.w.prepare("edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan == nil {
		t.Fatal(h.w.detailMessage)
	}
	oldPlan, binding := h.w.plan, h.w.planBinding
	h.w.detailMode = "Edit"
	h.tt.Frame()
	before := h.w.editor.Snapshot()
	h.click("Manifest editor")
	h.tt.SetClipboard(strings.Repeat("x", 8193))
	h.tt.Key(ui.Cmd, ui.KeyV)
	count := requests.Load()
	for _, kind := range []string{"edit", "create", "apply", "delete", "scale", "restart"} {
		h.w.prepare(kind)
	}
	// Guard the execution method too, not only the disabled button and normal
	// revocation path. No stale in-memory plan may reach the operation service.
	h.w.plan, h.w.planBinding, h.w.confirmation = oldPlan, binding, oldPlan.Target().Name
	h.w.execute()
	if h.w.plan != nil || h.w.preparing || h.w.pendingWrites != 0 || writes.Load() != 0 || requests.Load() != count || h.w.editor.Snapshot() != before {
		t.Fatal("unresolved paste reached mutation or rewrote original draft")
	}
	h.click("Discard retained editor paste")
	if h.w.plan != nil || h.w.confirmation != "" {
		t.Fatal("discard resurrected prior confirmation")
	}
	h.w.prepare("edit")
	h.pump(func() bool { return !h.w.preparing })
	if h.w.plan == nil || h.w.plan == oldPlan || writes.Load() != 0 {
		t.Fatal("explicit fresh dry-run did not resume safely")
	}
	// Expired credentials are checked even before the next owner frame.
	h.w.detailMode = "Edit"
	ctx, cancel := context.WithCancel(context.Background())
	h.w.connectionCtx = ctx
	stale := h.w.manifestPasteHandler()
	cancel()
	if !stale(strings.Repeat("x", 8193)) || h.w.editorRetainedPaste != "" {
		t.Fatal("expired callback retained private clipboard text")
	}
}
