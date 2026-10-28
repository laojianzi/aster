//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

type installExecutionTransport struct {
	next      http.RoundTripper
	namespace string
	writes    *atomic.Int32
	bad       *atomic.Bool
	before    func(int) error
	after     func(int, *http.Response) error
}

func (g installExecutionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet {
		return g.next.RoundTrip(r)
	}
	q := r.URL.Query()
	prefix := "/api/v1/namespaces/" + g.namespace + "/"
	dry := q.Get("dryRun") == "All"
	allowed := r.Method == http.MethodPost && (r.URL.Path == prefix+"secrets" || r.URL.Path == prefix+"configmaps")
	allowed = allowed || !dry && r.Method == http.MethodPatch && r.URL.Path == prefix+"secrets/sh.helm.release.v1.execute-install.v1"
	count := 2
	if dry {
		count = 3
	}
	if !allowed || q.Get("fieldValidation") != "Strict" || q.Get("fieldManager") != helmrelease.InstallFieldManager || len(q) != count || r.GetBody != nil {
		g.bad.Store(true)
		return nil, errors.New("unexpected install method, path or replay capability")
	}
	if dry {
		return g.next.RoundTrip(r)
	}
	n := int(g.writes.Add(1))
	if g.before != nil {
		if err := g.before(n); err != nil {
			return nil, err
		}
	}
	response, err := g.next.RoundTrip(r)
	if err == nil && g.after != nil {
		if e := g.after(n, response); e != nil {
			response.Body.Close()
			return nil, e
		}
	}
	return response, err
}

// Every persistent operation is confined to a disposable fixture namespace.
// The separately authenticated fixture client creates only deliberate race
// competitors. It does not stand in for the Backend's own successful writes.
func TestRealHelmInstallExecution(t *testing.T) {
	for _, mode := range []string{"success", "storage-race", "target-race", "committed-response-loss"} {
		t.Run(mode, func(t *testing.T) {
			f := testcluster.NewHelmMetadata(t, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			var writes atomic.Int32
			var bad atomic.Bool
			cfg := rest.CopyConfig(f.Config)
			cfg.Wrap(func(next http.RoundTripper) http.RoundTripper {
				return installExecutionTransport{next: next, namespace: f.Namespace, writes: &writes, bad: &bad,
					before: func(n int) error {
						if mode == "storage-race" && n == 1 {
							_, err := f.Client.CoreV1().Secrets(f.Namespace).Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.execute-install.v1"}, Data: map[string][]byte{"canary": []byte("DO_NOT_ADOPT")}}, metav1.CreateOptions{})
							return err
						}
						if mode == "target-race" && n == 2 {
							_, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "execute-target"}, Data: map[string]string{"canary": "DO_NOT_ADOPT"}}, metav1.CreateOptions{})
							return err
						}
						return nil
					},
					after: func(n int, response *http.Response) error {
						if mode == "committed-response-loss" && n == 1 {
							if response.StatusCode != 201 {
								bad.Store(true)
							}
							return errors.New("PRIVATE_COMMITTED_RESPONSE_LOSS")
						}
						return nil
					},
				}
			})
			b, err := kube.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			stamp := now.Add(-time.Second).UTC().Format(time.RFC3339Nano)
			raw, _ := json.Marshal(map[string]any{"name": "execute-install", "namespace": f.Namespace, "version": 1, "apply_method": "ssa", "info": map[string]any{"status": "pending-install", "description": "Dry run complete", "first_deployed": stamp, "last_deployed": stamp}, "chart": map[string]any{"metadata": map[string]any{"name": "execute-chart", "apiVersion": "v2", "version": "0.1.0"}}, "manifest": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: execute-target\ndata:\n  private: REVIEWED_INSTALL\n"})
			candidate, err := helmrelease.DecodeInstallCapture(ctx, raw, f.Namespace, "execute-install", now)
			clear(raw)
			if err != nil {
				t.Fatal(err)
			}
			prep, err := b.PrepareHelmInstall(ctx, candidate, "real-execution", true)
			if err != nil {
				t.Fatal(err)
			}
			dryConsent, err := b.AcknowledgeHelmInstallDryRun(ctx, prep, candidate, "real-execution", true)
			if err != nil {
				t.Fatal(err)
			}
			preflight, err := b.PreflightHelmInstall(ctx, dryConsent, candidate, "real-execution")
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(t.TempDir(), "journal")
			journal, err := kube.OpenHelmUninstallJournal(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			grant, err := b.AcknowledgeHelmInstallExecution(ctx, preflight, candidate, "real-execution", true, journal)
			if err != nil {
				t.Fatal(err)
			}
			result, executionErr := b.ExecuteHelmInstall(ctx, grant, candidate, "real-execution")
			if result == nil || bad.Load() {
				t.Fatal("missing result or unsafe transport", executionErr)
			}
			secret, err := f.Client.CoreV1().Secrets(f.Namespace).Get(ctx, candidate.StorageName(), metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			cm, cmErr := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, "execute-target", metav1.GetOptions{})
			switch mode {
			case "success":
				if executionErr != nil || result.Outcome() != helmjournal.Completed || !result.JournalComplete() || writes.Load() != 3 || cmErr != nil || cm.Data["private"] != "REVIEWED_INSTALL" || secret.Labels["status"] != "deployed" {
					t.Fatal("real install incomplete", executionErr, result.Outcome())
				}
				if result.Effects()[0].UID != string(secret.UID) || result.Effects()[2].ObservedResourceVersion != secret.ResourceVersion || result.Effects()[1].UID != string(cm.UID) {
					t.Fatal("not actual persistent identities")
				}
			case "storage-race":
				if !errors.Is(executionErr, kube.ErrHelmInstallExecutionRejected) || writes.Load() != 1 || result.Outcome() != helmjournal.Rejected || string(secret.Data["canary"]) != "DO_NOT_ADOPT" || !apierrors.IsNotFound(cmErr) {
					t.Fatal("adopted storage race", executionErr)
				}
			case "target-race":
				if !errors.Is(executionErr, kube.ErrHelmInstallExecutionRejected) || writes.Load() != 2 || result.Outcome() != "Partial" || cmErr != nil || cm.Data["canary"] != "DO_NOT_ADOPT" || secret.Labels["status"] != "pending-install" {
					t.Fatal("adopted target race", executionErr)
				}
			case "committed-response-loss":
				if !errors.Is(executionErr, kube.ErrHelmInstallExecutionUnknown) || writes.Load() != 1 || result.Outcome() != helmjournal.Unknown || secret.Labels["status"] != "pending-install" || !apierrors.IsNotFound(cmErr) {
					t.Fatal("lost response replayed or discarded", executionErr)
				}
			}
			if executionErr != nil && strings.Contains(executionErr.Error(), "PRIVATE_") {
				t.Fatal("transport detail leaked")
			}
			before := writes.Load()
			if _, err = b.ExecuteHelmInstall(ctx, grant, candidate, "real-execution"); !errors.Is(err, kube.ErrHelmInstallExecutionConsent) || writes.Load() != before {
				t.Fatal("write authority reused")
			}
			if err = journal.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := helmjournal.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			recovery, err := store.RecoverInstall(result.JournalID(), false)
			if err != nil {
				t.Fatal(err)
			}
			if !recovery.Snapshot().Closed || writes.Load() != before {
				t.Fatal("inert recovery changed cluster")
			}
		})
	}
}
