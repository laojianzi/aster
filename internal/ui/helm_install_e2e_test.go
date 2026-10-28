//go:build e2e

package uiworkbench

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

type nativeInstallRealRequest struct {
	method, path, accept, query, encoding string
	dry, hasBody, canReplay               bool
	status                                int
}

// All replies come from the real opted-in API server. The single optional
// fault discards an actual successful target-create reply after consuming it;
// it never supplies synthetic success, mutates the server, or stores bodies.
type nativeInstallRealAudit struct {
	mu                    sync.Mutex
	namespace             string
	loseTarget, lost, bad bool
	requests              []nativeInstallRealRequest
}

type nativeInstallRealTransport struct {
	base  http.RoundTripper
	audit *nativeInstallRealAudit
}

func (a *nativeInstallRealAudit) wrap(base http.RoundTripper) http.RoundTripper {
	return nativeInstallRealTransport{base: base, audit: a}
}

func (r nativeInstallRealTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	a := r.audit
	prefix := "/api/v1/namespaces/" + a.namespace
	query := req.URL.Query()
	call := nativeInstallRealRequest{method: req.Method, path: req.URL.Path,
		accept: req.Header.Get("Accept"), query: req.URL.RawQuery, encoding: req.Header.Get("Accept-Encoding"),
		dry: query.Get("dryRun") != "", hasBody: req.Body != nil && req.Body != http.NoBody, canReplay: req.GetBody != nil}
	valid := req.Method == http.MethodGet
	if req.Method == http.MethodPost || req.Method == http.MethodPatch {
		valid = req.Method == http.MethodPost && (call.path == prefix+"/secrets" || call.path == prefix+"/configmaps") ||
			req.Method == http.MethodPatch && call.path == prefix+"/secrets/sh.helm.release.v1.example.v1" && !call.dry
		valid = valid && query.Get("fieldValidation") == "Strict" && query.Get("fieldManager") == helmrelease.InstallFieldManager &&
			(!call.dry || query.Get("dryRun") == "All") && query.Get("force") == "" && req.GetBody == nil &&
			req.Header.Get("Accept-Encoding") == "identity"
	}
	a.mu.Lock()
	index := len(a.requests)
	a.requests = append(a.requests, call)
	a.bad = a.bad || !valid
	a.mu.Unlock()
	if !valid {
		return nil, errors.New("unexpected real native install request")
	}
	response, err := r.base.RoundTrip(req)
	a.mu.Lock()
	if response != nil {
		a.requests[index].status = response.StatusCode
	}
	inject := a.loseTarget && !a.lost && !call.dry && call.method == http.MethodPost &&
		call.path == prefix+"/configmaps" && err == nil && response != nil && response.StatusCode == http.StatusCreated
	if inject {
		a.lost = true
	}
	a.mu.Unlock()
	if inject {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, helmrelease.MaxSecretBytes+1))
		_ = response.Body.Close()
		return nil, errors.New("PRIVATE_NATIVE_INSTALL_REAL_ACCEPTED_RESPONSE_LOST")
	}
	return response, err
}

func (a *nativeInstallRealAudit) snapshot() ([]nativeInstallRealRequest, bool, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]nativeInstallRealRequest(nil), a.requests...), a.lost, a.bad
}

func (a *nativeInstallRealAudit) writes() []nativeInstallRealRequest {
	calls, _, _ := a.snapshot()
	var out []nativeInstallRealRequest
	for _, call := range calls {
		if call.method != http.MethodGet && !call.dry {
			out = append(out, call)
		}
	}
	return out
}

func connectNativeInstallReal(t *testing.T, h *relationshipHarness, f testcluster.HelmMetadataFixture, audit *nativeInstallRealAudit) {
	t.Helper()
	_, current, err := kubeconfig.Contexts("")
	nativeHelmJournalMust(t, err)
	h.w.currentContext, h.w.contexts, h.w.namespace = current, []string{current}, f.Namespace
	h.tt.Frame()
	h.click("Connect")
	nativeInstallDrain(h, func() bool { return h.w.status == "Live" })
	// Retain the actual native connection's endpoint, TLS, credentials, request
	// limiter and session. Only its HTTP observer changes; no fake limiter or
	// fixture URL is substituted for the disposable cluster.
	cfg := rest.CopyConfig(h.w.backend.Config())
	cfg.Wrap(audit.wrap)
	backend, err := kube.New(cfg)
	nativeHelmJournalMust(t, err)
	h.w.backend = backend
	h.tt.Frame()
}

func captureNativeInstallReal(h *relationshipHarness, in nativeInstallInput, audit *nativeInstallRealAudit) {
	h.t.Helper()
	before, _, _ := audit.snapshot()
	nativeInstallOpenAndFill(h, in)
	nativeInstallClick(h, helmInstallTrustLabel)
	nativeInstallClick(h, helmInstallCapabilityLabel)
	nativeInstallClick(h, "Capture install candidate")
	nativeInstallDrain(h, func() bool { return h.w.helmInstall.busy == "" })
	s := &h.w.helmInstall
	if s.candidate == nil || !s.candidate.Valid(time.Now()) || !s.inputsMatch() ||
		s.candidate.Namespace() != in.request.Namespace || s.candidate.Release() != in.request.Release ||
		s.prepareAck || s.dryRunAck || s.executeAck {
		h.t.Fatal("real native capture failed or retained later-stage authority", s.status)
	}
	after, _, bad := audit.snapshot()
	if bad || !reflect.DeepEqual(before, after) {
		h.t.Fatal("local native capture contacted the API server")
	}
	if _, err := os.Stat(s.journalPath); !os.IsNotExist(err) {
		h.t.Fatal("local capture opened private recovery storage", err)
	}
}

func preflightNativeInstallReal(h *relationshipHarness, audit *nativeInstallRealAudit) {
	h.t.Helper()
	nativeInstallClick(h, helmInstallPrepareConsentLabel)
	nativeInstallClick(h, "Prepare Helm install")
	nativeInstallDrain(h, func() bool { return h.w.helmInstall.busy == "" })
	s := &h.w.helmInstall
	if s.preparation == nil || !s.preparation.Matches(s.backend, s.candidate, s.session, time.Now()) ||
		s.prepareAck || s.dryRunAck || s.executeAck {
		h.t.Fatal("real native preparation failed or inherited permission", s.status)
	}
	nativeInstallClick(h, helmInstallDryRunConsentLabel)
	nativeInstallClick(h, "Run install dry-run")
	nativeInstallDrain(h, func() bool { return s.busy == "" })
	if s.dryRun == nil || !s.dryRun.Matches(s.backend, s.candidate, s.session, time.Now()) ||
		s.dryRunAck || s.executeAck || s.confirmation != "" {
		h.t.Fatal("real native preflight failed or lost its origin across worker completion", s.status)
	}
	calls, _, bad := audit.snapshot()
	dry := 0
	for _, call := range calls {
		if call.dry {
			dry++
			if call.method != http.MethodPost || call.status != http.StatusCreated {
				h.t.Fatal("server did not independently accept the exact native dry-run create")
			}
		}
	}
	if bad || dry != 2 || len(audit.writes()) != 0 {
		h.t.Fatal("real native preflight omitted a body or authorized persistent effects")
	}
	if _, err := os.Stat(s.journalPath); !os.IsNotExist(err) {
		h.t.Fatal("preparation or preflight created an operation journal", err)
	}
}

func executeNativeInstallReal(h *relationshipHarness) *kube.HelmInstallExecution {
	h.t.Helper()
	nativeInstallClick(h, helmInstallExecuteConsentLabel)
	nativeInstallEdit(h, "Helm install release confirmation", "example")
	nativeInstallClick(h, "Execute Helm install")
	nativeInstallDrain(h, func() bool { return h.w.helmInstall.busy == "" && h.w.pendingWrites == 0 })
	s := &h.w.helmInstall
	if !s.attempted || s.execution == nil || s.execution.JournalID() == "" || !s.execution.JournalComplete() ||
		s.executeAck || s.confirmation != "" || s.preparation != nil || s.dryRun != nil {
		h.t.Fatal("real native execution lost its result, journal or consumed confirmation", s.status)
	}
	return s.execution
}

func TestNativeHelmInstallAgainstRealCluster(t *testing.T) {
	for _, scenario := range []string{"completed", "conflict", "response-loss"} {
		t.Run(scenario, func(t *testing.T) {
			f := testcluster.NewHelmMetadata(t, 0)
			h := newRelationshipHarness(t, 90*time.Second)
			h.w.helmJournalPath = filepath.Join(t.TempDir(), "journal")
			path := h.w.helmJournalPath
			audit := &nativeInstallRealAudit{namespace: f.Namespace, loseTarget: scenario == "response-loss"}
			connectNativeInstallReal(t, h, f, audit)
			in := nativeInstallFiles(t, f.Namespace, 1, false)
			var conflict *corev1.ConfigMap
			if scenario == "conflict" {
				var err error
				conflict, err = f.Client.CoreV1().ConfigMaps(f.Namespace).Create(h.ctx, &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "config-00", Namespace: f.Namespace},
					Data:       map[string]string{"existing": "PRIVATE_NATIVE_INSTALL_EXISTING"},
				}, metav1.CreateOptions{})
				nativeHelmJournalMust(t, err)
			}
			captureNativeInstallReal(h, in, audit)
			if scenario == "conflict" {
				nativeInstallClick(h, helmInstallPrepareConsentLabel)
				nativeInstallClick(h, "Prepare Helm install")
				nativeInstallDrain(h, func() bool { return h.w.helmInstall.busy == "" })
				s := &h.w.helmInstall
				if s.preparation != nil || s.dryRun != nil || s.execution != nil || s.attempted ||
					s.status != helmrelease.ErrInstallConflict.Error() || len(audit.writes()) != 0 {
					t.Fatal("real same-name object was adopted or qualified for native installation", s.status)
				}
				actual, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(h.ctx, conflict.Name, metav1.GetOptions{})
				if err != nil || !reflect.DeepEqual(actual, conflict) {
					t.Fatal("conflict review changed the independent existing object", err)
				}
				_, err = f.Client.CoreV1().Secrets(f.Namespace).Get(h.ctx, "sh.helm.release.v1.example.v1", metav1.GetOptions{})
				if !apierrors.IsNotFound(err) {
					t.Fatal("conflict preparation created release storage", err)
				}
				if _, err = os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("conflict preparation created an execution journal", err)
				}
				calls, lost, bad := audit.snapshot()
				if lost || bad || len(calls) == 0 {
					t.Fatal("conflict evidence did not come from ordinary real-server reads")
				}
				for _, call := range calls {
					if call.method != http.MethodGet || call.dry {
						t.Fatal("conflict preparation advanced beyond its acknowledged GET scope")
					}
				}
				saveNativeScreenshot(t, h.tt)
				return
			}
			preflightNativeInstallReal(h, audit)
			result := executeNativeInstallReal(h)
			wantOutcome, wantWrites, wantStatus := helmjournal.Completed, 3, "deployed"
			if scenario == "response-loss" {
				wantOutcome, wantWrites, wantStatus = helmjournal.Unknown, 2, "pending-install"
			}
			calls, lost, bad := audit.snapshot()
			writes := audit.writes()
			if bad || lost != (scenario == "response-loss") || len(writes) != wantWrites || result.Outcome() != wantOutcome {
				t.Fatal("real native install lost its exact dispatch or historical outcome", h.w.helmInstall.status)
			}
			prefix := "/api/v1/namespaces/" + f.Namespace
			for i, write := range writes {
				wantMethod, wantPath, wantCode := http.MethodPost, prefix+"/secrets", http.StatusCreated
				if i == 1 {
					wantPath = prefix + "/configmaps"
				} else if i == 2 {
					wantMethod, wantPath, wantCode = http.MethodPatch, prefix+"/secrets/sh.helm.release.v1.example.v1", http.StatusOK
				}
				if write.method != wantMethod || write.path != wantPath || write.status != wantCode {
					t.Fatal("native persistent stages were reordered, replayed or not accepted by the real API server")
				}
			}
			secret, err := f.Client.CoreV1().Secrets(f.Namespace).Get(h.ctx, "sh.helm.release.v1.example.v1", metav1.GetOptions{})
			nativeHelmJournalMust(t, err)
			target, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(h.ctx, "config-00", metav1.GetOptions{})
			nativeHelmJournalMust(t, err)
			if secret.UID == "" || target.UID == "" || secret.Type != "helm.sh/release.v1" ||
				secret.Labels["owner"] != "helm" || secret.Labels["name"] != "example" || secret.Labels["version"] != "1" ||
				secret.Labels["status"] != wantStatus || len(secret.Data) != 1 || len(target.Data) != 1 ||
				target.Data["message"] != "PRIVATE_NATIVE_INSTALL_FROZEN" {
				t.Fatal("independent server objects do not match the frozen first-revision installation")
			}
			_ = nativeHelmRetainedPayload(t, secret, wantStatus)
			effects := result.Effects()
			if len(effects) != 3 || effects[0].UID != string(secret.UID) || effects[0].Outcome != helmjournal.Completed {
				t.Fatal("native result lost accepted storage identity")
			}
			snap := nativeInstallLoad(t, path, result.JournalID())
			if snap.Version != 2 || snap.Action != "install" || snap.Install == nil || !snap.Closed || snap.Corrupt ||
				!reflect.DeepEqual(snap.Effects, effects) {
				t.Fatal("real native result was not retained as checked immutable v2 history")
			}
			if scenario == "completed" {
				if effects[1].UID != string(target.UID) || effects[1].ObservedResourceVersion != target.ResourceVersion ||
					effects[1].Outcome != helmjournal.Completed || effects[2].UID != string(secret.UID) ||
					effects[2].ObservedResourceVersion != secret.ResourceVersion || effects[2].Outcome != helmjournal.Completed {
					t.Fatal("completed native result did not positively observe the real accepted objects")
				}
				nativeInstallReach(h, "Helm install outcome")
				saveNativeScreenshot(t, h.tt)
				before := len(calls)
				h.w.executeHelmInstall()
				after, _, _ := audit.snapshot()
				if len(after) != before || h.w.pendingWrites != 0 {
					t.Fatal("completed native UI replayed a consumed execution")
				}
				return
			}
			if effects[1].Outcome != helmjournal.Unknown || effects[1].UID != "" || effects[2].Outcome != helmjournal.NotStarted ||
				strings.Contains(h.w.helmInstall.status, "PRIVATE_NATIVE_INSTALL_REAL") {
				t.Fatal("lost actual create response was fabricated into an accepted identity or exposed transport details")
			}
			beforeDisk := nativeInstallDiskSnapshot(t, path)
			before := len(calls)
			nativeInstallClick(h, "Review install recovery")
			nativeInstallDrain(h, func() bool { return h.w.helmUninstall.busy == "" })
			u := &h.w.helmUninstall
			if u.installRecovery == nil || u.recovery != nil || u.selectedID != result.JournalID() || len(u.history) != 1 ||
				u.installRecovery.Outcome() != helmjournal.Unknown || !reflect.DeepEqual(u.installRecovery.Effects(), effects) ||
				u.installObservation != nil || u.reconcileAck || u.row.StorageName != "" {
				t.Fatal("mixed history dispatch changed the real v2 record or recovered read/write authority", u.status)
			}
			nativeHelmClick(h, "Observe current metadata")
			after, _, _ := audit.snapshot()
			if len(after) != before || u.installObservation != nil {
				t.Fatal("local history or an unconfirmed observation inherited execution consent")
			}
			nativeHelmClick(h, helmRecoveryConsentLabel)
			nativeHelmClick(h, "Observe current metadata")
			nativeInstallDrain(h, func() bool { return u.busy == "" })
			observation := u.installObservation
			if observation == nil || observation.HistoryOutcome() != helmjournal.Unknown || u.installRecovery.Outcome() != helmjournal.Unknown ||
				observation.Storage().State != kube.HelmInstallRecoveryOriginal || observation.Storage().UID != string(secret.UID) ||
				len(observation.Items()) != 1 || observation.Items()[0].State != kube.HelmInstallRecoveryUnattributed ||
				observation.Items()[0].HistoricalUID != "" || observation.Items()[0].UID != string(target.UID) ||
				u.reconcileAck || u.installReconcileConsent != nil {
				t.Fatal("fresh metadata reads cleared Unknown or attributed a create whose acknowledgement was lost", u.status)
			}
			after, _, bad = audit.snapshot()
			expected := []string{prefix, prefix + "/secrets", prefix + "/secrets/" + secret.Name,
				prefix + "/configmaps/" + target.Name, prefix + "/secrets/" + secret.Name, prefix + "/secrets", prefix}
			if bad || len(after)-before != len(expected) || len(audit.writes()) != wantWrites {
				t.Fatal("fresh recovery did not stay inside one bounded metadata-only GET sequence")
			}
			for i, call := range after[before:] {
				accept, query := "application/json;as=PartialObjectMetadata;g=meta.k8s.io;v=v1", ""
				if call.path == prefix+"/secrets" {
					accept, query = helmrelease.Accept, "labelSelector=owner%3Dhelm&limit=64"
				}
				if call.method != http.MethodGet || call.path != expected[i] || call.accept != accept || call.query != query ||
					call.encoding != "identity" || call.hasBody || call.canReplay || call.status != http.StatusOK {
					t.Fatal("recovery retried, read full object data or escaped its exact recorded scope")
				}
			}
			nativeInstallAssertDiskUnchanged(t, path, beforeDisk)
			store, err := helmjournal.OpenExisting(path)
			nativeHelmJournalMust(t, err)
			admission, admissionErr := store.ReviewAdmission(context.Background(), helmjournal.AdmissionScope{
				Binding: snap.Install.Binding, Namespace: f.Namespace, Release: "example"})
			nativeHelmJournalMust(t, store.Close())
			if !errors.Is(admissionErr, helmjournal.ErrUnresolved) || admission.Reason != helmjournal.AdmissionUnresolved || admission.RecordID != result.JournalID() {
				t.Fatal("metadata-only recovery cleared the retained unresolved-operation guard", admissionErr)
			}
			nativeInstallAssertDiskUnchanged(t, path, beforeDisk)
			before = len(after)
			h.w.observeHelmRecovery()
			after, _, _ = audit.snapshot()
			if len(after) != before || u.installObservation != observation {
				t.Fatal("fresh observation confirmation was silently reused")
			}
			nativeHelmReach(h, "Helm install recovery current target 1")
			if strings.Contains(strings.Join(h.tt.Texts(), "\n"), "PRIVATE_NATIVE_INSTALL") {
				t.Fatal("native recovery exposed private captured content or transport details")
			}
			saveNativeScreenshot(t, h.tt)
		})
	}
}
