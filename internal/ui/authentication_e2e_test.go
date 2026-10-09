//go:build e2e

package uiworkbench

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/credentialexec"
	"github.com/laojianzi/aster/internal/testauth"
	"github.com/laojianzi/aster/internal/testcluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestNativeExecCredentialLiveLogsExpireAgainstRealCluster(t *testing.T) {
	f := testcluster.NewHTTPPod(t)
	token := testcluster.ReadOnlyToken(t, f)
	marker := filepath.Join(t.TempDir(), "credential-started")
	h := newRelationshipHarness(t, 45*time.Second)
	h.w.path = testauth.ConfigFile(t, f.Config, f.Pod.Namespace, "credential", clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_TOKEN", Value: token}, clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_DURATION", Value: "10"}, clientcmdapi.ExecEnvVar{Name: "ASTER_FIXTURE_STARTED", Value: marker})
	h.w.currentContext = "credential-test"
	h.w.contexts = []string{"credential-test"}
	h.tt.Frame()
	h.click("Connect")
	h.pump(func() bool { return h.w.trustRequired && !h.w.connectionPending })
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("helper ran before native trust")
	}
	h.click("Trust this context and connect")
	h.pump(func() bool { return h.w.status == "Live" && containsRowUID(h.w.rows, string(f.Pod.UID)) })
	oldBackend, oldContext := h.w.backend, h.w.connectionCtx
	h.click(f.Pod.Name)
	h.pump(func() bool { return h.w.detail != nil })
	h.click("Logs")
	h.click("Follow logs")
	h.pump(func() bool { return len(h.w.logRows) > 0 })
	if h.w.logCancel == nil {
		t.Fatal("logs did not establish a cancellable stream")
	}
	h.pump(func() bool { return h.w.status == "Credentials expired" })
	if oldContext.Err() == nil || h.w.backend != nil || len(h.w.rows) > 0 || len(h.w.logRows) > 0 || h.w.logCancel != nil || h.w.plan != nil || h.w.detail != nil || h.w.ops != nil || h.w.connectionPending {
		t.Fatal("credential expiry retained private state or refreshed silently")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := oldBackend.List(ctx, schema.GroupVersionResource{Version: "v1", Resource: "pods"}, f.Pod.Namespace, metav1.ListOptions{})
	if !errors.Is(err, credentialexec.ErrClosed) && !errors.Is(err, credentialexec.ErrExpired) {
		t.Fatal("old backend survives identity expiry", err)
	}
	saveNativeScreenshot(t, h.tt)
}
