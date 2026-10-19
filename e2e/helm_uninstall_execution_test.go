//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

const helmExecutionSession = "real-uninstall-execution"

type helmExecutionFixture struct {
	testcluster.HelmMetadataFixture
	storage *corev1.Secret
	targets []*corev1.ConfigMap
	canary  *corev1.ConfigMap
}

// NewHelmMetadata reaches testcluster.Config before creating anything. The
// product executor is called only inside this explicit disposable-kind fixture.
// Synthetic release storage separates Aster's restricted behavior from Helm's
// wider CLI behavior, which remains covered by the original pinned-Helm suite.
func newHelmExecutionFixture(t *testing.T, ctx context.Context, count int) *helmExecutionFixture {
	t.Helper()
	f := &helmExecutionFixture{HelmMetadataFixture: testcluster.NewHelmMetadata(t, 1)}
	var manifest strings.Builder
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("execution-target-%d", i+1)
		fmt.Fprintf(&manifest, "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\ndata:\n  private: PRIVATE_STORED\n", name)
		cm, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"},
				Annotations: map[string]string{"meta.helm.sh/release-name": "example", "meta.helm.sh/release-namespace": f.Namespace}},
			Data: map[string]string{"private": "PRIVATE_LIVE"},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatal("fixture target", err)
		}
		f.targets = append(f.targets, cm)
	}
	payload, err := json.Marshal(map[string]any{
		"name": "example", "namespace": f.Namespace, "version": 1,
		"info":     map[string]any{"status": "deployed", "description": "fixture installation"},
		"chart":    map[string]any{"metadata": map[string]any{"name": "example-chart", "version": "1.2.3", "apiVersion": "v2"}},
		"manifest": manifest.String(), "config": map[string]any{"private": "PRIVATE_VALUE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := f.Secrets[0].DeepCopy()
	s.Data = map[string][]byte{"release": helmfixture.Stored(payload)}
	s.Annotations = map[string]string{"aster.test/retained": "unrelated-metadata"}
	clear(payload)
	f.storage, err = f.Client.CoreV1().Secrets(f.Namespace).Update(ctx, s, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal("fixture release", err)
	}
	f.canary, err = f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "execution-unrelated-canary"}, Data: map[string]string{"guard": "unchanged"},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal("fixture canary", err)
	}
	return f
}

type helmExecutionCall struct {
	kind, name, uid, rv string
	status              int
	failure             helmExecutionFailure
}

// Record only the bounded identity/status projection needed to compare real
// Kubernetes rejection shapes. Never retain the server's message or raw body.
type helmExecutionFailure struct {
	apiVersion, kind, status string
	code                     int32
	reason                   metav1.StatusReason
	hasDetails               bool
	name, resource, group    string
	uid                      string
	causes                   int
	retryAfterSeconds        int32
	emptyMetadata            bool
}

func observeHelmExecutionFailure(response *http.Response) (helmExecutionFailure, error) {
	var out helmExecutionFailure
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	// Preserve the original bytes and the rest of the original stream for the
	// product. This observer never manufactures or repairs a response envelope.
	original := response.Body
	response.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(body), original), original}
	if err != nil || len(body) > 4096 {
		return out, errors.New("fixture persistent failure envelope unavailable")
	}
	var status metav1.Status
	if json.Unmarshal(body, &status) != nil {
		return out, errors.New("fixture persistent failure envelope is not JSON")
	}
	out.apiVersion, out.kind, out.status = status.APIVersion, status.Kind, status.Status
	out.code, out.reason = status.Code, status.Reason
	out.emptyMetadata = reflect.DeepEqual(status.ListMeta, metav1.ListMeta{})
	if status.Details != nil {
		out.hasDetails = true
		out.name, out.resource, out.group = status.Details.Name, status.Details.Kind, status.Details.Group
		out.uid, out.causes, out.retryAfterSeconds = string(status.Details.UID), len(status.Details.Causes), status.Details.RetryAfterSeconds
	}
	return out, nil
}

// All responses originate at the real API server. Faults either change the
// disposable fixture just before a conditional write, or lose a SUCCESSFUL
// committed response. No fake successful Kubernetes response is manufactured.
type helmExecutionTransport struct {
	base   http.RoundTripper
	mu     sync.Mutex
	calls  []helmExecutionCall
	before func(context.Context, helmExecutionCall) error
	lose   string
	lost   bool
	fault  error
}

func (a *helmExecutionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPatch && req.Method != http.MethodDelete {
		return a.base.RoundTrip(req)
	}
	dry := req.URL.Query().Get("dryRun") != ""
	call := helmExecutionCall{name: req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]}
	if req.Method == http.MethodDelete {
		// Inspect only the bounded, nonsensitive DeleteOptions and forward their
		// original bytes unchanged. A body-only dry-run cannot count as a write.
		if req.Body == nil {
			return nil, errors.New("fixture DeleteOptions missing")
		}
		body, err := io.ReadAll(io.LimitReader(req.Body, (64<<10)+1))
		_ = req.Body.Close()
		if err != nil || len(body) > 64<<10 {
			return nil, errors.New("fixture DeleteOptions unavailable")
		}
		var options metav1.DeleteOptions
		if json.Unmarshal(body, &options) != nil {
			return nil, errors.New("fixture DeleteOptions invalid")
		}
		dry = dry || len(options.DryRun) > 0
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(body))
		if options.Preconditions != nil && options.Preconditions.UID != nil && options.Preconditions.ResourceVersion != nil {
			call.uid, call.rv = string(*options.Preconditions.UID), *options.Preconditions.ResourceVersion
		}
		if !dry && (call.uid == "" || call.rv == "" || options.PropagationPolicy == nil || *options.PropagationPolicy != metav1.DeletePropagationOrphan) {
			return nil, errors.New("fixture observed unconditional or non-orphan deletion")
		}
		call.kind = "delete"
	}
	if dry {
		return a.base.RoundTrip(req)
	}
	a.mu.Lock()
	if req.Method == http.MethodPatch {
		call.kind = "begin"
		for _, previous := range a.calls {
			if previous.kind == "begin" {
				call.kind = "finish"
				break
			}
		}
	}
	index := len(a.calls)
	a.calls = append(a.calls, call)
	a.mu.Unlock()
	if a.before != nil {
		if err := a.before(req.Context(), call); err != nil {
			a.mu.Lock()
			a.fault = err
			a.mu.Unlock()
			return nil, errors.New("fixture mutation failed")
		}
	}
	response, err := a.base.RoundTrip(req)
	var failure helmExecutionFailure
	var observationErr error
	if err == nil && response != nil && response.StatusCode >= 400 && response.StatusCode < 500 {
		failure, observationErr = observeHelmExecutionFailure(response)
	}
	a.mu.Lock()
	if response != nil {
		a.calls[index].status = response.StatusCode
		a.calls[index].failure = failure
	}
	if observationErr != nil {
		a.fault = observationErr
	}
	inject := err == nil && response != nil && response.StatusCode >= 200 && response.StatusCode < 300 && call.kind == a.lose && !a.lost
	if inject {
		a.lost = true
	}
	a.mu.Unlock()
	if inject {
		_ = response.Body.Close()
		const body = `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"ServiceUnavailable","code":503,"message":"fixture lost committed response"}`
		response.StatusCode, response.Status = http.StatusServiceUnavailable, "503 Service Unavailable"
		response.Header = http.Header{"Content-Type": []string{"application/json"}, "Retry-After": []string{"0"}}
		response.Body = io.NopCloser(strings.NewReader(body))
		response.ContentLength = int64(len(body))
	}
	return response, err
}

func (a *helmExecutionTransport) observed(t *testing.T) []helmExecutionCall {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fault != nil {
		t.Fatal("fault setup did not complete", a.fault)
	}
	return append([]helmExecutionCall(nil), a.calls...)
}

func helmExecutionBackend(t *testing.T, cfg *rest.Config, audit *helmExecutionTransport) *kube.Backend {
	t.Helper()
	copy := rest.CopyConfig(cfg)
	copy.Wrap(func(base http.RoundTripper) http.RoundTripper { audit.base = base; return audit })
	b, err := kube.New(copy)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func prepareHelmExecution(t *testing.T, ctx context.Context, f *helmExecutionFixture, b *kube.Backend) (*kube.HelmUninstallExecutionConsent, helmrelease.Revision) {
	t.Helper()
	snapshot, err := b.HelmReleaseMetadata(ctx, f.Namespace)
	if err != nil || len(snapshot.Revisions) != 1 {
		t.Fatal("fixture metadata", err)
	}
	row := snapshot.Revisions[0]
	p, err := b.PrepareHelmUninstall(ctx, f.Namespace, row, helmExecutionSession, true)
	if err != nil {
		t.Fatal("preparation", err)
	}
	dryConsent, err := b.AcknowledgeHelmUninstallDryRun(p, helmExecutionSession, f.Namespace, row, true)
	if err != nil {
		t.Fatal("dry-run acknowledgement", err)
	}
	dry, err := b.PreflightHelmUninstall(ctx, dryConsent, helmExecutionSession, f.Namespace, row)
	if err != nil {
		t.Fatal("actual server preflight", err)
	}
	if consent, err := b.AcknowledgeHelmUninstallExecution(dry, helmExecutionSession, f.Namespace, row, false); err == nil || consent != nil {
		t.Fatal("preflight was treated as execution acknowledgement")
	}
	consent, err := b.AcknowledgeHelmUninstallExecution(dry, helmExecutionSession, f.Namespace, row, true)
	if err != nil {
		t.Fatal("execution acknowledgement", err)
	}
	t.Cleanup(consent.Discard)
	return consent, row
}

func (f *helmExecutionFixture) retained(t *testing.T, ctx context.Context, status string) *corev1.Secret {
	t.Helper()
	got, err := f.Client.CoreV1().Secrets(f.Namespace).Get(ctx, f.storage.Name, metav1.GetOptions{})
	if err != nil || got.UID != f.storage.UID || got.Labels["status"] != status ||
		!reflect.DeepEqual(got.Annotations, f.storage.Annotations) || len(got.Data) != 1 ||
		!reflect.DeepEqual(statusRetainedPayload(t, f.storage), statusRetainedPayload(t, got)) {
		t.Fatal("retained storage identity, state or unrelated payload changed", err)
	}
	wire, row := statusWire(t, got)
	defer clear(wire)
	if _, err := helmrelease.InspectSecret(ctx, wire, f.Namespace, row); err != nil {
		t.Fatal("retained label and release payload disagree", err)
	}
	canary, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, f.canary.Name, metav1.GetOptions{})
	if err != nil || !reflect.DeepEqual(f.canary, canary) {
		t.Fatal("unreviewed canary changed", err)
	}
	return got
}

func (f *helmExecutionFixture) unchanged(t *testing.T, ctx context.Context, index int) {
	t.Helper()
	want := f.targets[index]
	got, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, want.Name, metav1.GetOptions{})
	if err != nil || !reflect.DeepEqual(want, got) {
		t.Fatal("unsubmitted or rejected target changed", index, err)
	}
}

func (f *helmExecutionFixture) absent(t *testing.T, ctx context.Context, index int) {
	t.Helper()
	if err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		_, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, f.targets[index].Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}); err != nil {
		t.Fatal("independent server did not confirm target absence", index, err)
	}
}

func assertHelmExecutionNoReplay(t *testing.T, ctx context.Context, f *helmExecutionFixture, b *kube.Backend, c *kube.HelmUninstallExecutionConsent, row helmrelease.Revision, audit *helmExecutionTransport) {
	t.Helper()
	writes := audit.observed(t)
	if _, err := b.ExecuteHelmUninstall(ctx, c, helmExecutionSession, f.Namespace, row); !errors.Is(err, kube.ErrHelmExecutionConsent) {
		t.Fatal("consumed execution was reusable", err)
	}
	if !reflect.DeepEqual(writes, audit.observed(t)) {
		t.Fatal("consumed execution sent another persistent request")
	}
}

func TestRealHelmUninstallExecutionRetainsHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	f := newHelmExecutionFixture(t, ctx, 2)
	audit := &helmExecutionTransport{}
	b := helmExecutionBackend(t, f.Config, audit)
	c, row := prepareHelmExecution(t, ctx, f, b)
	if len(audit.observed(t)) != 0 {
		t.Fatal("preparation or acknowledgement persisted a request")
	}
	f.retained(t, ctx, "deployed")
	for i := range f.targets {
		f.unchanged(t, ctx, i)
	}
	result, err := b.ExecuteHelmUninstall(ctx, c, helmExecutionSession, f.Namespace, row)
	if err != nil || result == nil || result.Outcome() != kube.HelmExecutionCompleted {
		t.Fatal("restricted server execution did not complete", err)
	}
	calls := audit.observed(t)
	if len(calls) != 4 || calls[0].kind != "begin" || calls[3].kind != "finish" {
		t.Fatal("expected one begin, ordered individual deletes, and one finish", calls)
	}
	for i, target := range f.targets {
		call := calls[i+1]
		if call.kind != "delete" || call.name != target.Name || call.uid != string(target.UID) || call.rv != target.ResourceVersion {
			t.Fatal("delete lost reviewed order or exact UID/RV")
		}
		f.absent(t, ctx, i)
	}
	final := f.retained(t, ctx, "uninstalled")
	if final.ResourceVersion == f.storage.ResourceVersion || result.Storage().UID != string(final.UID) || result.Storage().ResourceVersion != final.ResourceVersion {
		t.Fatal("execution result did not bind final retained storage")
	}
	effects := result.Effects()
	if len(effects) != len(calls) {
		t.Fatal("execution report omitted an ordered effect")
	}
	for i, effect := range effects {
		if effect.Kind != calls[i].kind || effect.Name != calls[i].name || effect.Namespace != f.Namespace || effect.UID == "" {
			t.Fatal("effect identity differs from actual request order")
		}
	}
	assertHelmExecutionNoReplay(t, ctx, f, b, c, row, audit)
}

func TestRealHelmUninstallExecutionLastMomentConditions(t *testing.T) {
	for _, mode := range []string{"storage-rv", "target-rv", "target-uid"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			f := newHelmExecutionFixture(t, ctx, 2)
			originalUID, originalRV := string(f.targets[0].UID), f.targets[0].ResourceVersion
			injected := false
			audit := &helmExecutionTransport{}
			audit.before = func(ctx context.Context, call helmExecutionCall) error {
				wanted := call.kind == "begin" && mode == "storage-rv" || call.kind == "delete" && mode != "storage-rv"
				if !wanted || injected {
					return nil
				}
				injected = true
				if mode == "storage-rv" {
					current, err := f.Client.CoreV1().Secrets(f.Namespace).Get(ctx, f.storage.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}
					current.Annotations["aster.test/concurrent"] = "retained"
					f.storage, err = f.Client.CoreV1().Secrets(f.Namespace).Update(ctx, current, metav1.UpdateOptions{})
					return err
				}
				configs := f.Client.CoreV1().ConfigMaps(f.Namespace)
				current := f.targets[0].DeepCopy()
				if mode == "target-rv" {
					current.Annotations["aster.test/concurrent"] = "retained"
					updated, err := configs.Update(ctx, current, metav1.UpdateOptions{})
					if err == nil {
						f.targets[0] = updated
					}
					return err
				}
				uid := current.UID
				if err := configs.Delete(ctx, current.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil {
					return err
				}
				if err := wait.PollUntilContextTimeout(ctx, 25*time.Millisecond, 3*time.Second, true, func(ctx context.Context) (bool, error) {
					_, err := configs.Get(ctx, current.Name, metav1.GetOptions{})
					if apierrors.IsNotFound(err) {
						return true, nil
					}
					return false, err
				}); err != nil {
					return err
				}
				current.ObjectMeta = metav1.ObjectMeta{Name: current.Name, Labels: current.Labels, Annotations: current.Annotations}
				replacement, err := configs.Create(ctx, current, metav1.CreateOptions{})
				if err == nil && replacement.UID == uid {
					return errors.New("fixture replacement reused UID")
				}
				if err == nil {
					f.targets[0] = replacement
				}
				return err
			}
			b := helmExecutionBackend(t, f.Config, audit)
			c, row := prepareHelmExecution(t, ctx, f, b)
			result, err := b.ExecuteHelmUninstall(ctx, c, helmExecutionSession, f.Namespace, row)
			calls := audit.observed(t)
			want, status, count := kube.HelmExecutionPartial, "uninstalling", 2
			if mode == "storage-rv" {
				// Kubernetes' JSON Patch test failure is a generic 422 with
				// empty resource/name details. It is not an exact rejection
				// envelope: keep Unknown despite this independent admin read.
				want, status, count = kube.HelmExecutionUnknown, "deployed", 1
			}
			if !injected || len(calls) != count {
				t.Fatal("fixture did not exercise exactly the last GET-to-write condition", err)
			}
			last := calls[len(calls)-1]
			if last.status != http.StatusConflict && last.status != http.StatusUnprocessableEntity {
				t.Fatal("server did not reject the exact stale condition", last.status)
			}
			if mode != "storage-rv" {
				// Pinned Kubernetes BeforeDelete uses the object's canonical
				// Kind in StatusDetails.Kind for its early UID/RV conflict,
				// unlike the plural resource used by later storage rejection.
				exactConflict := helmExecutionFailure{apiVersion: "v1", kind: "Status", status: metav1.StatusFailure,
					code: 409, reason: metav1.StatusReasonConflict, hasDetails: true,
					name: last.name, resource: "ConfigMap", emptyMetadata: true}
				if last.status != http.StatusConflict || last.failure != exactConflict {
					t.Fatal("server did not return the exact canonical ConfigMap conflict", last.status, last.failure)
				}
				if last.uid != originalUID || last.rv != originalRV {
					t.Fatal("delete replaced reviewed preconditions after the last read")
				}
			}
			if err == nil || result == nil || result.Outcome() != want {
				t.Fatal("exact last GET-to-write rejection was misclassified", err)
			}
			if mode != "storage-rv" {
				expectedEffects := []struct {
					kind    string
					outcome kube.HelmUninstallExecutionOutcome
				}{
					{"begin", kube.HelmExecutionCompleted},
					{"delete", kube.HelmExecutionRejected},
					{"delete", kube.HelmExecutionNotStarted},
					{"finish", kube.HelmExecutionNotStarted},
				}
				effects := result.Effects()
				if len(effects) != len(expectedEffects) {
					t.Fatal("conditional rejection lost an ordered effect")
				}
				for i, expected := range expectedEffects {
					if effects[i].Kind != expected.kind || effects[i].Outcome != expected.outcome {
						t.Fatal("conditional rejection misclassified an individual effect", i)
					}
				}
				if effects[1].UID != originalUID || effects[1].ResourceVersion != originalRV {
					t.Fatal("rejected effect lost the original reviewed UID/RV")
				}
			}
			f.retained(t, ctx, status)
			for i := range f.targets {
				f.unchanged(t, ctx, i)
			}
			assertHelmExecutionNoReplay(t, ctx, f, b, c, row, audit)
		})
	}
}

func (f *helmExecutionFixture) permission(ctx context.Context, user, resource, verb string, allowed bool) error {
	return wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		sar, err := f.Client.AuthorizationV1().SubjectAccessReviews().Create(ctx, &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{User: user, Groups: []string{"system:authenticated"},
				ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: f.Namespace, Group: "", Version: "v1", Resource: resource, Verb: verb}},
		}, metav1.CreateOptions{})
		if err != nil {
			return false, err
		}
		return sar.Status.Allowed == allowed, nil
	})
}

func TestRealHelmUninstallExecutionRBACAndPartialEffects(t *testing.T) {
	for _, mode := range []string{"deny-begin", "deny-second-delete", "deny-finish"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			f := newHelmExecutionFixture(t, ctx, 3)
			user := "aster-execution-" + f.Namespace
			role, err := f.Client.RbacV1().Roles(f.Namespace).Create(ctx, &rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{Name: "execution"}, Rules: []rbacv1.PolicyRule{
					{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get", "list", "patch"}},
					{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get", "delete"}},
				},
			}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.Client.RbacV1().RoleBindings(f.Namespace).Create(ctx, &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: role.Name}, Subjects: []rbacv1.Subject{{Kind: "User", APIGroup: rbacv1.GroupName, Name: user}},
				RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name},
			}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for resource, verb := range map[string]string{"secrets": "patch", "configmaps": "delete"} {
				if err := f.permission(ctx, user, resource, verb, true); err != nil {
					t.Fatal("fixture grant was not effective", err)
				}
			}
			injected := false
			audit := &helmExecutionTransport{}
			audit.before = func(ctx context.Context, call helmExecutionCall) error {
				wanted := mode == "deny-begin" && call.kind == "begin" || mode == "deny-finish" && call.kind == "finish" ||
					mode == "deny-second-delete" && call.kind == "delete" && call.name == f.targets[1].Name
				if !wanted || injected {
					return nil
				}
				injected = true
				current, err := f.Client.RbacV1().Roles(f.Namespace).Get(ctx, role.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}
				resource, verb, index := "secrets", "patch", 0
				if mode == "deny-second-delete" {
					resource, verb, index = "configmaps", "delete", 1
				}
				current.Rules[index].Verbs = []string{"get", "list"}
				if _, err := f.Client.RbacV1().Roles(f.Namespace).Update(ctx, current, metav1.UpdateOptions{}); err != nil {
					return err
				}
				return f.permission(ctx, user, resource, verb, false)
			}
			cfg := rest.CopyConfig(f.Config)
			cfg.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: []string{"system:authenticated"}}
			b := helmExecutionBackend(t, cfg, audit)
			c, row := prepareHelmExecution(t, ctx, f, b)
			result, err := b.ExecuteHelmUninstall(ctx, c, helmExecutionSession, f.Namespace, row)
			calls := audit.observed(t)
			want, status, count, deleted := kube.HelmExecutionPartial, "uninstalling", 3, 1
			if mode == "deny-begin" {
				want, status, count, deleted = kube.HelmExecutionRejected, "deployed", 1, 0
			} else if mode == "deny-finish" {
				count, deleted = 5, 3
			}
			if !injected || err == nil || result == nil || result.Outcome() != want || len(calls) != count {
				t.Fatal("real permission loss did not stop ordered effects", err)
			}
			if calls[len(calls)-1].status != http.StatusForbidden {
				t.Fatal("server did not independently deny the persistent request")
			}
			f.retained(t, ctx, status)
			for i := range f.targets {
				if i < deleted {
					f.absent(t, ctx, i)
				} else {
					f.unchanged(t, ctx, i)
				}
			}
			assertHelmExecutionNoReplay(t, ctx, f, b, c, row, audit)
		})
	}
}

func TestRealHelmUninstallExecutionCommittedResponseLoss(t *testing.T) {
	for _, stage := range []string{"begin", "delete", "finish"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			f := newHelmExecutionFixture(t, ctx, 2)
			audit := &helmExecutionTransport{lose: stage}
			b := helmExecutionBackend(t, f.Config, audit)
			c, row := prepareHelmExecution(t, ctx, f, b)
			result, err := b.ExecuteHelmUninstall(ctx, c, helmExecutionSession, f.Namespace, row)
			calls := audit.observed(t)
			if !audit.lost || err == nil || result == nil || result.Outcome() != kube.HelmExecutionUnknown {
				t.Fatal("lost committed response was not Unknown", err)
			}
			count, deleted, status := 1, 0, "uninstalling"
			if stage == "delete" {
				count, deleted = 2, 1
			} else if stage == "finish" {
				count, deleted, status = 4, 2, "uninstalled"
			}
			if len(calls) != count {
				t.Fatal("ambiguous response caused replay or a later effect", calls)
			}
			if last := calls[len(calls)-1]; last.status < 200 || last.status >= 300 {
				t.Fatal("response fault did not follow real server acceptance")
			}
			// This unwrapped admin client observes real effects independently of
			// the deliberately lost response. Observation is not product recovery.
			f.retained(t, ctx, status)
			for i := range f.targets {
				if i < deleted {
					f.absent(t, ctx, i)
				} else {
					f.unchanged(t, ctx, i)
				}
			}
			assertHelmExecutionNoReplay(t, ctx, f, b, c, row, audit)
		})
	}
}
