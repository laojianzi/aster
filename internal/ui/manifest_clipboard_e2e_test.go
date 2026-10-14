//go:build e2e

package uiworkbench

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNativeManifestClipboardAgainstRealCluster(t *testing.T) {
	f := testcluster.NewManifestDelivery(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	live, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "clipboard-original", Namespace: f.Namespace}, Data: map[string]string{"value": "unchanged"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, current, err := kubeconfig.Contexts("")
	if err != nil {
		t.Fatal(err)
	}
	h := newRelationshipHarness(t, 90*time.Second)
	h.w.currentContext, h.w.contexts, h.w.namespace = current, []string{current}, f.Namespace
	h.tt.Frame()
	h.click("Connect")
	h.pump(func() bool { return h.w.status == "Live" })
	h.click("ConfigMaps")
	h.pump(func() bool { return containsRowUID(h.w.rows, string(live.UID)) })
	assertUnchanged := func() {
		t.Helper()
		after, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, live.Name, metav1.GetOptions{})
		if err != nil || after.UID != live.UID || after.ResourceVersion != live.ResourceVersion || !reflect.DeepEqual(after.Data, live.Data) || !reflect.DeepEqual(after.BinaryData, live.BinaryData) || !reflect.DeepEqual(after.Labels, live.Labels) || !reflect.DeepEqual(after.Annotations, live.Annotations) {
			t.Fatal("clipboard workflow changed the live object or could not verify its identity")
		}
		if h.w.pendingWrites != 0 || len(h.w.history) != 0 {
			t.Fatal("clipboard/dry-run flow submitted a resource write")
		}
	}
	incoming := strings.Repeat("PRIVATE_CLIPBOARD_", 600)
	t.Run("edit", func(t *testing.T) {
		h.click(live.Name)
		h.pump(func() bool { return h.w.detail != nil })
		h.click("Edit")
		buffer, original := h.w.editor, h.w.editor.Snapshot()
		h.click("Manifest editor")
		h.tt.Key(ui.Cmd, ui.KeyEnd)
		h.tt.SetClipboard("# native clipboard 中文 🚢\r\n")
		h.tt.Key(ui.Cmd, ui.KeyV)
		edited := h.w.editorText()
		snapshot, id := buffer.Snapshot(), h.w.draftIdentity()
		if edited != original.String()+"# native clipboard 中文 🚢\n" {
			t.Fatal("ordinary resource paste did not follow native normalization")
		}
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.SetClipboard(incoming)
		h.tt.Command("paste")
		if h.w.editorRetainedPaste != incoming || buffer.Snapshot() != snapshot || h.w.draftIdentity() != id || h.w.editor != buffer {
			t.Fatal("real resource intercepted paste changed the editor")
		}
		h.w.prepare("edit")
		if h.w.plan != nil || h.w.preparing {
			t.Fatal("retained resource paste reached preview")
		}
		assertUnchanged()
		saveNativeScreenshot(t, h.tt)
		h.click("Discard retained editor paste")
		h.click("Manifest editor")
		h.tt.Key(ui.Cmd, ui.KeyZ)
		if h.w.editorText() != original.String() {
			t.Fatal("resource retention lost native undo")
		}
		h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
		if h.w.editorText() != edited || h.w.editor != buffer || snapshot.String() != edited {
			t.Fatal("resource retention lost redo or snapshot")
		}
		h.click("Preview edit")
		h.pump(func() bool { return !h.w.preparing })
		if h.w.plan == nil || h.w.plan.Kind() != "edit" || h.w.plan.Target().UID != live.UID {
			t.Fatal("fresh reviewed edit dry-run did not resume")
		}
		assertUnchanged()
	})
	if t.Failed() {
		return
	}
	t.Run("create", func(t *testing.T) {
		h.click("Close detail")
		h.click("New resource")
		if !h.w.creating {
			t.Fatal("native create entry did not open")
		}
		const name = "clipboard-dry-run-only"
		text := fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\n  namespace: %s\ndata:\n  value: local-only\n", name, f.Namespace)
		h.click("Manifest editor")
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.SetClipboard(text)
		h.tt.Key(ui.Cmd, ui.KeyV)
		buffer, snapshot := h.w.editor, h.w.editor.Snapshot()
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.SetClipboard(incoming)
		h.tt.Key(ui.Cmd, ui.KeyV)
		h.w.prepare("create")
		if h.w.editorRetainedPaste != incoming || h.w.editor != buffer || buffer.Snapshot() != snapshot || h.w.plan != nil || h.w.preparing {
			t.Fatal("retained create paste changed source or reached preview")
		}
		if _, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatal("new draft absence was not proven")
		}
		assertUnchanged()
		saveNativeScreenshot(t, h.tt)
		h.click("Discard retained editor paste")
		h.click("Preview create")
		h.pump(func() bool { return !h.w.preparing })
		if h.w.plan == nil || h.w.plan.Kind() != "create" || h.w.plan.Target().Name != name || h.w.plan.Target().UID != "" {
			t.Fatal("fresh create-only dry-run did not resume")
		}
		if _, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatal("create dry-run persisted or absence was not proven")
		}
		assertUnchanged()
	})
	if t.Failed() {
		return
	}
	h.click("Edit")
	h.click("Manifest editor")
	h.tt.SetClipboard(incoming)
	h.tt.Command("paste")
	if h.w.editorRetainedPaste != incoming {
		t.Fatal("retention did not resume after review")
	}
	h.click("Disconnect")
	if h.w.editorRetainedPaste != "" || h.w.backend != nil || h.w.plan != nil {
		t.Fatal("disconnect retained clipboard or permission state")
	}
}
