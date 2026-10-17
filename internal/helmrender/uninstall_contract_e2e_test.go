//go:build e2e

package helmrender

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// The existing explicit destructive-kind opt-in is required before ANY cluster
// access. In addition, these tests require a TLS literal-loopback endpoint and
// embedded test certificates. No user kubeconfig is copied to the Helm process.
func contractCluster(t *testing.T) (*rest.Config, kubernetes.Interface, *contractTool) {
	t.Helper()
	cfg := testcluster.Config(t)
	endpoint, err := url.Parse(cfg.Host)
	if err != nil || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") || net.ParseIP(endpoint.Hostname()) == nil || !net.ParseIP(endpoint.Hostname()).IsLoopback() || cfg.Insecure || cfg.ExecProvider != nil || cfg.AuthProvider != nil || cfg.Proxy != nil || cfg.BearerToken != "" || cfg.BearerTokenFile != "" || cfg.Username != "" || cfg.Password != "" || cfg.Impersonate.UserName != "" {
		t.Fatal("uninstall characterization requires the dedicated local certificate-authenticated kind cluster")
	}
	if err := rest.LoadTLSFiles(cfg); err != nil || len(cfg.CAData) == 0 || len(cfg.CertData) == 0 || len(cfg.KeyData) == 0 {
		t.Fatal("isolated kind TLS identity is required")
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw := clientcmdapi.Config{Clusters: map[string]*clientcmdapi.Cluster{"contract": {Server: cfg.Host, CertificateAuthorityData: cfg.CAData, TLSServerName: cfg.ServerName}}, AuthInfos: map[string]*clientcmdapi.AuthInfo{"contract": {ClientCertificateData: cfg.CertData, ClientKeyData: cfg.KeyData}}, Contexts: map[string]*clientcmdapi.Context{"contract": {Cluster: "contract", AuthInfo: "contract"}}, CurrentContext: "contract"}
	data, err := clientcmd.Write(raw)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, client, newContractTool(t, data, approvedContractExecutable(t))
}
func contractChart(t *testing.T, root string, keep, hook bool) string {
	t.Helper()
	chart := filepath.Join(root, "contract-chart")
	if err := os.MkdirAll(filepath.Join(chart, "templates"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"Chart.yaml": "apiVersion: v2\nname: contract\nversion: 0.1.0\n", "templates/target.yaml": contractManifest("target", false)}
	if keep {
		files["templates/kept.yaml"] = contractManifest("kept", true)
	}
	if hook {
		files["templates/delete-hook.yaml"] = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: delete-hook\n  annotations:\n    helm.sh/hook: pre-delete\ndata:\n  fixture: harmless\n"
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(chart, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return chart
}
func TestRealHelmUninstallContractResourcesAndHistory(t *testing.T) {
	cfg, client, tool := contractCluster(t)
	for _, mode := range []string{"keep-history", "purge-history", "missing-marker", "mismatch-marker", "recreated", "hooks", "no-hooks"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			ns, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "aster-helm-contract-"}}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := client.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
					t.Error("fixture namespace cleanup", err)
				}
			})
			// Local static chart only: no repositories, dependencies, Jobs,
			// external images, custom resource definitions or cluster objects.
			chart := contractChart(t, t.TempDir(), mode == "keep-history", mode == "hooks" || mode == "no-hooks")
			if _, err := tool.run(t, "install", contractRelease, chart, "--namespace", ns.Name, "--kube-context", "contract", "--wait=hookOnly", "--timeout=5s", "--disable-openapi-validation"); err != nil {
				t.Fatal("real Helm install failed", err)
			}
			cm, err := client.CoreV1().ConfigMaps(ns.Name).Get(ctx, "target", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			originalUID := cm.UID
			if mode == "missing-marker" || mode == "mismatch-marker" {
				if mode == "missing-marker" {
					delete(cm.Labels, "app.kubernetes.io/managed-by")
				} else {
					cm.Annotations["meta.helm.sh/release-name"] = "different"
				}
				cm, err = client.CoreV1().ConfigMaps(ns.Name).Update(ctx, cm, metav1.UpdateOptions{})
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "recreated" {
				if err := client.CoreV1().ConfigMaps(ns.Name).Delete(ctx, cm.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &originalUID}}); err != nil {
					t.Fatal(err)
				}
				cm.UID = ""
				cm.ResourceVersion = ""
				cm.CreationTimestamp = metav1.Time{}
				cm.ManagedFields = nil
				cm, err = client.CoreV1().ConfigMaps(ns.Name).Create(ctx, cm, metav1.CreateOptions{})
				if err != nil || cm.UID == originalUID {
					t.Fatal("recreated fixture must have a new UID", err)
				}
			}
			// Exercise the already delivered Aster readers against a REAL Helm
			// installation, rather than only Helm-shaped synthetic Secrets.
			backend, err := kube.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := backend.HelmReleaseMetadata(ctx, ns.Name)
			if err != nil || len(snapshot.Revisions) != 1 {
				t.Fatal("actual release metadata", err)
			}
			row := snapshot.Revisions[0]
			preview, err := backend.PreviewHelmUninstall(ctx, ns.Name, row, "contract-session", true)
			if err != nil {
				t.Fatal("actual release preview", err)
			}
			observation, err := backend.ObserveHelmPreview(ctx, preview, "contract-session", true)
			if err != nil {
				t.Fatal("actual release live metadata", err)
			}
			wantMarker := "match"
			if mode == "missing-marker" {
				wantMarker = "missing"
			}
			if mode == "mismatch-marker" {
				wantMarker = "mismatch"
			}
			found := false
			for _, item := range observation.Items() {
				if item.Stored.Name == "target" {
					found = true
					if item.UID != string(cm.UID) || item.Markers != wantMarker {
						t.Fatal("actual target observation differs")
					}
				}
			}
			if !found {
				t.Fatal("actual installed target missing from preview")
			}
			before, err := client.CoreV1().Secrets(ns.Name).Get(ctx, row.StorageName, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tool.uninstall(t, ns.Name, "--dry-run", "--keep-history"); err != nil {
				t.Fatal("actual dry-run", err)
			}
			after, err := client.CoreV1().Secrets(ns.Name).Get(ctx, row.StorageName, metav1.GetOptions{})
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("dry-run changed actual release", err)
			}
			live, err := client.CoreV1().ConfigMaps(ns.Name).Get(ctx, "target", metav1.GetOptions{})
			if err != nil || !reflect.DeepEqual(cm, live) {
				t.Fatal("observation/dry-run changed target", err)
			}
			if mode == "hooks" || mode == "no-hooks" {
				if _, err := client.CoreV1().ConfigMaps(ns.Name).Get(ctx, "delete-hook", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
					t.Fatal("dry-run must not execute delete hook", err)
				}
			}
			flags := []string{"--keep-history"}
			if mode == "purge-history" {
				flags = nil
			}
			if mode != "hooks" {
				flags = append(flags, "--no-hooks")
			}
			if _, err := tool.uninstall(t, ns.Name, flags...); err != nil {
				t.Fatal("actual uninstall", err)
			}
			if mode == "missing-marker" || mode == "mismatch-marker" {
				live, err = client.CoreV1().ConfigMaps(ns.Name).Get(ctx, "target", metav1.GetOptions{})
				if err != nil || !reflect.DeepEqual(cm, live) {
					t.Fatal("mismatched object must remain unchanged despite CLI success", err)
				}
			} else {
				// --wait=hookOnly permits an asynchronous DELETE to remain visible
				// briefly. Poll READS only for exact absence, with a separate tight
				// deadline; a persistent object, replacement UID or denied GET fails.
				err = observeHelmUninstallTargetAbsent(ctx, 3*time.Second, "target", cm.UID, func(ctx context.Context) (types.UID, error) {
					observed, err := client.CoreV1().ConfigMaps(ns.Name).Get(ctx, "target", metav1.GetOptions{})
					if err != nil {
						return "", err
					}
					if observed == nil || observed.Name != "target" || observed.Namespace != ns.Name {
						return "", fmt.Errorf("ConfigMap response identity does not match the requested target")
					}
					return observed.UID, nil
				})
				if err != nil {
					t.Fatal("real target disappearance not established", err)
				}
			}
			history, err := client.CoreV1().Secrets(ns.Name).List(ctx, metav1.ListOptions{LabelSelector: "owner=helm,name=contract"})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "purge-history" {
				if len(history.Items) != 0 {
					t.Fatal("history not purged")
				}
			} else if len(history.Items) != 1 || history.Items[0].Labels["status"] != "uninstalled" {
				t.Fatal("actual retained history status")
			}
			if mode == "keep-history" {
				kept, err := client.CoreV1().ConfigMaps(ns.Name).Get(ctx, "kept", metav1.GetOptions{})
				if err != nil {
					t.Fatal("kept resource removed", err)
				}
				if _, err := tool.uninstall(t, ns.Name, "--keep-history", "--no-hooks"); err == nil {
					t.Fatal("repeated retained uninstall unexpectedly succeeded")
				}
				if _, err := tool.uninstall(t, ns.Name, "--no-hooks"); err != nil {
					t.Fatal("explicit fixture history purge", err)
				}
				again, err := client.CoreV1().ConfigMaps(ns.Name).Get(ctx, "kept", metav1.GetOptions{})
				if err != nil || !reflect.DeepEqual(kept, again) {
					t.Fatal("history purge deleted kept object", err)
				}
				remaining, err := client.CoreV1().Secrets(ns.Name).List(ctx, metav1.ListOptions{LabelSelector: "owner=helm,name=contract"})
				if err != nil || len(remaining.Items) != 0 {
					t.Fatal("explicit purge retained history", err)
				}
			}
			if mode == "hooks" || mode == "no-hooks" {
				_, err := client.CoreV1().ConfigMaps(ns.Name).Get(ctx, "delete-hook", metav1.GetOptions{})
				if mode == "hooks" && err != nil {
					t.Fatal("actual pre-delete hook did not run", err)
				}
				if mode == "no-hooks" && !apierrors.IsNotFound(err) {
					t.Fatal("no-hooks did not suppress hook", err)
				}
			}
		})
	}
}
