//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// Writes below are explicit disposable-fixture setup/teardown (including the
// competing actor). The Backend under examination is independently GET-guarded.
type installReadGuard struct {
	next   http.RoundTripper
	bad    *atomic.Bool
	onRead func(*http.Request)
}

func (g installReadGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		g.bad.Store(true)
		return nil, fmt.Errorf("non-read install preparation request")
	}
	if g.onRead != nil {
		g.onRead(r)
	}
	return g.next.RoundTrip(r)
}

func TestRealHelmInstallPreparationReadOnly(t *testing.T) {
	for _, mode := range []string{"absent", "unacknowledged", "cancelled", "denied", "existing-target",
		"unlabeled-reserved-secret", "old-release", "competing-release"} {
		t.Run(mode, func(t *testing.T) {
			f := testcluster.NewHelmMetadata(t, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cms, secrets := f.Client.CoreV1().ConfigMaps(f.Namespace), f.Client.CoreV1().Secrets(f.Namespace)
			cm, err := cms.Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "install-canary"},
				Data:       map[string]string{"keep": "PRIVATE_CANARY"},
			}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			s, err := secrets.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "install-secret-canary"},
				Data:       map[string][]byte{"keep": []byte("PRIVATE_SECRET")},
			}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var occupied *corev1.ConfigMap
			var reserved *corev1.Secret
			if mode == "existing-target" {
				occupied, err = cms.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "install-target"}, Data: map[string]string{"keep": "DO_NOT_ADOPT"}}, metav1.CreateOptions{})
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "unlabeled-reserved-secret" {
				reserved, err = secrets.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.install-example.v1"}, Data: map[string][]byte{"keep": []byte("UNLABELED_COLLISION")}}, metav1.CreateOptions{})
				if err != nil {
					t.Fatal(err)
				}
			}
			makeHistory := func() (*corev1.Secret, error) {
				return secrets.Create(ctx, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.install-example.v2", Labels: map[string]string{"owner": "helm", "name": "install-example", "version": "2", "status": "uninstalled"}},
					Type:       "helm.sh/release.v1", Data: map[string][]byte{"release": []byte("HISTORY_PAYLOAD_MUST_NOT_BE_READ")},
				}, metav1.CreateOptions{})
			}
			var old *corev1.Secret
			if mode == "old-release" {
				old, err = makeHistory()
				if err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now()
			stamp := now.Add(-time.Second).UTC().Format(time.RFC3339Nano)
			payload, err := json.Marshal(map[string]any{
				"name": "install-example", "namespace": f.Namespace, "version": 1, "apply_method": "ssa",
				"info":     map[string]any{"status": "pending-install", "description": "Dry run complete", "first_deployed": stamp, "last_deployed": stamp},
				"chart":    map[string]any{"metadata": map[string]any{"name": "install-chart", "version": "0.1.0", "apiVersion": "v2"}},
				"manifest": fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: install-target\n  namespace: %s\ndata:\n  keep: NEVER_INSTALLED\n", f.Namespace),
			})
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := helmrelease.DecodeInstallCapture(ctx, payload, f.Namespace, "install-example", now)
			clear(payload)
			if err != nil {
				t.Fatal("capture fixture", err)
			}
			var bad atomic.Bool
			var raced atomic.Bool
			var raceErr error
			cfg := rest.CopyConfig(f.Config)
			if mode == "denied" {
				cfg.Impersonate = rest.ImpersonationConfig{UserName: "aster-install-no-reads"}
			}
			cfg.Wrap(func(next http.RoundTripper) http.RoundTripper {
				return installReadGuard{next: next, bad: &bad, onRead: func(r *http.Request) {
					// An independently authorized actor creates history after the
					// first complete history read, before the target absence read.
					if mode == "competing-release" && strings.HasSuffix(r.URL.Path, "/configmaps/install-target") && raced.CompareAndSwap(false, true) {
						old, raceErr = makeHistory()
					}
				}}
			})
			b, err := kube.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			opctx := ctx
			if mode == "cancelled" {
				var stop context.CancelFunc
				opctx, stop = context.WithCancel(ctx)
				stop()
			}
			p, err := b.PrepareHelmInstall(opctx, candidate, "real-install-session", mode != "unacknowledged")
			if mode == "absent" {
				if err != nil || p == nil || !p.Matches(b, candidate, "real-install-session", time.Now()) {
					t.Fatal("valid read-only preparation", err)
				}
			} else if err == nil || p != nil {
				t.Fatal("unsafe preparation accepted")
			}
			if mode == "unacknowledged" && !errors.Is(err, helmrelease.ErrInstallConsent) {
				t.Fatal("consent", err)
			}
			if mode == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancel", err)
			}
			if mode == "competing-release" && (!raced.Load() || raceErr != nil) {
				t.Fatal("race fixture", raceErr)
			}
			if bad.Load() {
				t.Fatal("preparation attempted mutation")
			}
			// Full objects, not just names, remain identical (UID/RV/data/metadata).
			checkCM := func(want *corev1.ConfigMap) {
				t.Helper()
				if want == nil {
					return
				}
				got, e := cms.Get(ctx, want.Name, metav1.GetOptions{})
				if e != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("ConfigMap changed", e)
				}
			}
			checkSecret := func(want *corev1.Secret) {
				t.Helper()
				if want == nil {
					return
				}
				got, e := secrets.Get(ctx, want.Name, metav1.GetOptions{})
				if e != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("Secret changed", e)
				}
			}
			checkCM(cm)
			checkCM(occupied)
			checkSecret(s)
			checkSecret(reserved)
			checkSecret(old)
			// Observe exact installer identities independently. Namespace-wide
			// counts incorrectly include controller-created kube-root-ca.crt;
			// those objects are neither installed nor owned by this test.
			// The guard above still rejects EVERY non-GET Backend request.
			expectAbsent := func(err error, resource, name string) {
				t.Helper()
				var status *apierrors.StatusError
				if !errors.As(err, &status) || !apierrors.IsNotFound(err) || status.ErrStatus.Code != 404 ||
					status.ErrStatus.Reason != metav1.StatusReasonNotFound || status.ErrStatus.Details == nil ||
					status.ErrStatus.Details.Group != "" || status.ErrStatus.Details.Kind != resource ||
					status.ErrStatus.Details.Name != name {
					t.Fatalf("exact %s/%s absence not established", resource, name)
				}
			}
			if occupied == nil {
				_, err := cms.Get(ctx, "install-target", metav1.GetOptions{})
				expectAbsent(err, "configmaps", "install-target")
			}
			if reserved == nil {
				_, err := secrets.Get(ctx, candidate.StorageName(), metav1.GetOptions{})
				expectAbsent(err, "secrets", candidate.StorageName())
			}
		})
	}
}
