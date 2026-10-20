//go:build e2e

package uiworkbench

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

type nativeHelmRequest struct {
	method, path, kind, uid, rv string
	status                      int
	dry                         bool
}

// This observer records bounded request identities, never Secret bodies,
// credentials or remote error messages. Every response comes from the real API
// server. The one optional fault replaces only an already successful DELETE
// response, after the independent server has accepted the actual request.
type nativeHelmAudit struct {
	mu         sync.Mutex
	requests   []nativeHelmRequest
	loseDelete bool
	lost       bool
}

type nativeHelmTransport struct {
	base  http.RoundTripper
	audit *nativeHelmAudit
}

func (a *nativeHelmAudit) wrap(base http.RoundTripper) http.RoundTripper {
	return nativeHelmTransport{base: base, audit: a}
}

func (r nativeHelmTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	call := nativeHelmRequest{method: req.Method, path: req.URL.Path, dry: req.URL.Query().Get("dryRun") != ""}
	if req.Method == http.MethodDelete {
		// DELETE dryRun may be in DeleteOptions rather than the URL. Preserve
		// the exact original bytes while checking the independent wire facts.
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
		call.dry = call.dry || len(options.DryRun) > 0
		call.kind = "delete"
		if options.Preconditions != nil && options.Preconditions.UID != nil && options.Preconditions.ResourceVersion != nil {
			call.uid, call.rv = string(*options.Preconditions.UID), *options.Preconditions.ResourceVersion
		}
		if call.uid == "" || call.rv == "" || options.PropagationPolicy == nil || *options.PropagationPolicy != metav1.DeletePropagationOrphan {
			return nil, errors.New("fixture observed unconditional or non-orphan deletion")
		}
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	a := r.audit
	a.mu.Lock()
	if req.Method == http.MethodPatch && !call.dry {
		call.kind = "begin"
		for _, previous := range a.requests {
			if previous.kind == "begin" && !previous.dry {
				call.kind = "finish"
				break
			}
		}
	}
	index := len(a.requests)
	a.requests = append(a.requests, call)
	a.mu.Unlock()
	response, err := r.base.RoundTrip(req)
	a.mu.Lock()
	if response != nil {
		a.requests[index].status = response.StatusCode
	}
	inject := a.loseDelete && !a.lost && !call.dry && call.kind == "delete" && err == nil &&
		response != nil && response.StatusCode >= 200 && response.StatusCode < 300
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

func (a *nativeHelmAudit) snapshot() ([]nativeHelmRequest, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]nativeHelmRequest(nil), a.requests...), a.lost
}

func (a *nativeHelmAudit) writes() []nativeHelmRequest {
	requests, _ := a.snapshot()
	var writes []nativeHelmRequest
	for _, request := range requests {
		if request.method != http.MethodGet && request.method != http.MethodHead && !request.dry {
			writes = append(writes, request)
		}
	}
	return writes
}

func connectNativeHelmFixture(t *testing.T, h *relationshipHarness, f testcluster.HelmNativeUninstallFixture, audit *nativeHelmAudit) {
	t.Helper()
	_, current, err := kubeconfig.Contexts("")
	if err != nil {
		t.Fatal("disposable native context", err)
	}
	h.w.currentContext, h.w.contexts, h.w.namespace = current, []string{current}, f.Namespace
	h.tt.Frame()
	h.click("Connect")
	h.pump(func() bool { return h.w.status == "Live" })
	// Establish the actual native connection/session before adding the test's
	// request observer. The copied configuration retains its endpoint, TLS and
	// session-bound credentials; no fixture URL or successful reply is invented.
	cfg := rest.CopyConfig(h.w.backend.Config())
	cfg.Wrap(audit.wrap)
	backend, err := kube.New(cfg)
	if err != nil {
		t.Fatal("native real-server observer", err)
	}
	h.w.backend = backend
	h.tt.Frame()
}

func selectNativeHelmFixture(h *relationshipHarness, f testcluster.HelmNativeUninstallFixture) {
	h.t.Helper()
	h.click("Helm releases")
	h.click("Refresh Helm metadata")
	h.pump(func() bool { return !h.w.helmReleases.busy })
	h.click("Select " + f.Storage.Name)
}

// Decode fixture-owned bytes independently of the product's status codec. Only
// the three documented lifecycle members may differ; all other raw JSON values
// remain byte-for-byte equal, including unrelated members inside info.
func nativeHelmRetainedPayload(t *testing.T, secret *corev1.Secret, status string) map[string]json.RawMessage {
	t.Helper()
	compressed, err := base64.StdEncoding.DecodeString(string(secret.Data["release"]))
	if err != nil {
		t.Fatal("fixture release encoding", err)
	}
	defer clear(compressed)
	z, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal("fixture release compression", err)
	}
	defer z.Close()
	payload, err := io.ReadAll(io.LimitReader(z, helmrelease.MaxReleaseBytes+1))
	if err != nil || len(payload) > helmrelease.MaxReleaseBytes {
		t.Fatal("fixture release decoded budget", err)
	}
	defer clear(payload)
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		t.Fatal("fixture release JSON")
	}
	var info map[string]json.RawMessage
	if json.Unmarshal(fields["info"], &info) != nil {
		t.Fatal("fixture release info")
	}
	var actual string
	if json.Unmarshal(info["status"], &actual) != nil || actual != status {
		t.Fatal("actual retained label/payload state mismatch")
	}
	for _, key := range []string{"status", "description", "deleted"} {
		delete(info, key)
	}
	fields["info"], err = json.Marshal(info)
	if err != nil {
		t.Fatal("fixture retained info", err)
	}
	return fields
}

func assertNativeHelmRetained(t *testing.T, ctx context.Context, f testcluster.HelmNativeUninstallFixture, status string) *corev1.Secret {
	t.Helper()
	storage, err := f.Client.CoreV1().Secrets(f.Namespace).Get(ctx, f.Storage.Name, metav1.GetOptions{})
	if err != nil || storage.UID != f.Storage.UID || storage.Type != f.Storage.Type || storage.Labels["status"] != status ||
		!reflect.DeepEqual(storage.Annotations, f.Storage.Annotations) || len(storage.Data) != 1 ||
		!reflect.DeepEqual(nativeHelmRetainedPayload(t, f.Storage, "deployed"), nativeHelmRetainedPayload(t, storage, status)) {
		t.Fatal("native execution changed retained identity or unrelated payload", err)
	}
	canary, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, f.Canary.Name, metav1.GetOptions{})
	if err != nil || !reflect.DeepEqual(canary, f.Canary) {
		t.Fatal("native execution changed an unreviewed canary", err)
	}
	return storage
}

func assertNativeHelmTargets(t *testing.T, ctx context.Context, f testcluster.HelmNativeUninstallFixture, deleted int) {
	t.Helper()
	for index, expected := range f.Targets {
		if index < deleted {
			err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
				_, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, expected.Name, metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					return true, nil
				}
				return false, err
			})
			if err != nil {
				t.Fatal("independent server did not confirm reviewed target absence", index, err)
			}
			continue
		}
		actual, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, expected.Name, metav1.GetOptions{})
		if err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatal("unsubmitted reviewed target changed", index, err)
		}
	}
}

// Reach controls through the actual native scroll viewport. Finding a widget
// outside the visible clip is not sufficient evidence that a user can click it.
func nativeHelmE2EReach(h *relationshipHarness, label string) {
	h.t.Helper()
	h.tt.Frame()
	viewport, ok := h.tt.Find("Helm uninstall inventory")
	if !ok || viewport.H < 96 {
		h.t.Fatal("native uninstall viewport missing or collapsed")
	}
	h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, -10000)
	h.tt.Frame()
	for range 80 {
		box, found := h.tt.Find(label)
		if found && box.H >= 18 && box.W >= 20 && box.Y >= viewport.Y && box.Y+box.H <= viewport.Y+viewport.H {
			return
		}
		h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 50)
		h.tt.Frame()
	}
	saveNativeScreenshot(h.t, h.tt)
	h.t.Fatal("native uninstall control could not be reached", label)
}

func nativeHelmE2EClick(h *relationshipHarness, label string) {
	h.t.Helper()
	nativeHelmE2EReach(h, label)
	h.click(label)
}

func runNativeHelmUninstall(t *testing.T, h *relationshipHarness, f testcluster.HelmNativeUninstallFixture, audit *nativeHelmAudit) *kube.HelmUninstallExecution {
	t.Helper()
	selectNativeHelmFixture(h, f)
	h.click("Review restricted uninstall")
	if h.w.helmUninstall.preparation != nil || h.w.helmUninstall.dryRun != nil || h.w.helmUninstall.execution != nil ||
		h.w.helmUninstall.prepareAck || h.w.helmUninstall.dryRunAck || h.w.helmUninstall.executeAck || len(audit.writes()) != 0 {
		t.Fatal("opening native review inherited authorization or started an operation")
	}
	before, _ := audit.snapshot()
	nativeHelmE2EClick(h, "Prepare restricted uninstall")
	after, _ := audit.snapshot()
	if !reflect.DeepEqual(before, after) || h.w.helmUninstall.busy != "" {
		t.Fatal("native preparation read without separate content confirmation")
	}
	nativeHelmE2EClick(h, helmPrepareConsentLabel)
	if !h.w.helmUninstall.prepareAck {
		t.Fatal("native preparation confirmation did not commit")
	}
	nativeHelmE2EClick(h, "Prepare restricted uninstall")
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	p := h.w.helmUninstall.preparation
	if p == nil || !p.Matches(h.w.backend, h.w.sessionID, f.Namespace, h.w.helmReleases.selected, time.Now()) ||
		h.w.helmUninstall.prepareAck || h.w.helmUninstall.dryRunAck || h.w.helmUninstall.executeAck {
		t.Fatal("native preparation failed or leaked confirmation", h.w.helmUninstall.status)
	}
	targets := p.Targets()
	if len(targets) != len(f.Targets) {
		t.Fatal("native plan omitted reviewed targets")
	}
	for index, target := range targets {
		want := f.Targets[index]
		if target.Namespace != f.Namespace || target.Name != want.Name || target.UID != string(want.UID) || target.ResourceVersion != want.ResourceVersion {
			t.Fatal("native plan did not bind the actual current reviewed identities")
		}
	}
	storage := assertNativeHelmRetained(t, h.ctx, f, "deployed")
	assertNativeHelmTargets(t, h.ctx, f, 0)
	if !reflect.DeepEqual(storage, f.Storage) || len(audit.writes()) != 0 {
		t.Fatal("native preparation persisted an effect")
	}
	before, _ = audit.snapshot()
	nativeHelmE2EClick(h, "Run uninstall dry-run")
	after, _ = audit.snapshot()
	if !reflect.DeepEqual(before, after) || h.w.helmUninstall.dryRun != nil {
		t.Fatal("native dry-run inherited preparation consent")
	}
	nativeHelmE2EClick(h, helmDryRunConsentLabel)
	if !h.w.helmUninstall.dryRunAck {
		t.Fatal("native server dry-run confirmation did not commit")
	}
	nativeHelmE2EClick(h, "Run uninstall dry-run")
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	dry := h.w.helmUninstall.dryRun
	if dry == nil || !dry.Matches(h.w.backend, h.w.sessionID, f.Namespace, h.w.helmReleases.selected, time.Now()) ||
		h.w.helmUninstall.dryRunAck || h.w.helmUninstall.executeAck || h.w.helmUninstall.confirmation != "" {
		t.Fatal("native server dry-run failed or inherited execution confirmation", h.w.helmUninstall.status)
	}
	storage = assertNativeHelmRetained(t, h.ctx, f, "deployed")
	assertNativeHelmTargets(t, h.ctx, f, 0)
	if !reflect.DeepEqual(storage, f.Storage) || len(audit.writes()) != 0 {
		t.Fatal("native dry-run persisted an effect")
	}
	requests, _ := audit.snapshot()
	dryRuns := 0
	for _, request := range requests {
		if request.dry {
			dryRuns++
			if request.status < 200 || request.status >= 300 || request.method != http.MethodPatch && request.method != http.MethodDelete {
				t.Fatal("native server dry-run was not independently admitted")
			}
		}
	}
	if dryRuns != 1+len(f.Targets) {
		t.Fatal("native server dry-run omitted an exact storage or target request", dryRuns)
	}
	nativeHelmE2EClick(h, "Execute restricted uninstall")
	if len(audit.writes()) != 0 || h.w.helmUninstall.attempted || h.w.pendingWrites != 0 {
		t.Fatal("native execution did not require its own confirmation")
	}
	nativeHelmE2EClick(h, helmExecuteConsentLabel)
	if !h.w.helmUninstall.executeAck {
		t.Fatal("native execution acknowledgement did not commit")
	}
	nativeHelmE2EClick(h, "Helm uninstall release confirmation")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("incorrect-example")
	nativeHelmE2EClick(h, "Execute restricted uninstall")
	if len(audit.writes()) != 0 || h.w.helmUninstall.attempted || h.w.pendingWrites != 0 {
		t.Fatal("incorrect typed release confirmation persisted an effect")
	}
	nativeHelmE2EClick(h, "Helm uninstall release confirmation")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("example")
	nativeHelmE2EClick(h, "Execute restricted uninstall")
	h.pump(func() bool { return h.w.helmUninstall.busy == "" && h.w.pendingWrites == 0 })
	result := h.w.helmUninstall.execution
	if result == nil || result.JournalID() == "" || !result.JournalComplete() || !h.w.helmUninstall.attempted ||
		h.w.helmUninstall.confirmation != "" || h.w.helmUninstall.executeAck || h.w.helmUninstall.dryRun != nil || h.w.helmUninstall.preparation != nil {
		t.Fatal("native execution did not settle its report, journal or one-shot state", h.w.helmUninstall.status)
	}
	return result
}

func selectNativeHelmHistory(h *relationshipHarness, id string) *kube.HelmUninstallRecovery {
	h.t.Helper()
	nativeHelmE2EClick(h, "Reload local history")
	h.pump(func() bool { return h.w.helmUninstall.busy == "" })
	if len(h.w.helmUninstall.history) != 1 || h.w.helmUninstall.history[0].id != id {
		h.t.Fatal("native local history did not reopen the exact single record", h.w.helmUninstall.status)
	}
	nativeHelmE2EClick(h, "Select Helm recovery "+id)
	r := h.w.helmUninstall.recovery
	if r == nil || r.ID() != id || !r.Complete() || r.Corrupt() || h.w.helmUninstall.recoveryErr != nil ||
		h.w.helmUninstall.reconcileAck || h.w.helmUninstall.observation != nil {
		h.t.Fatal("native recovery did not present immutable checked history", h.w.helmUninstall.status)
	}
	return r
}

func TestNativeHelmUninstallAgainstRealCluster(t *testing.T) {
	f := testcluster.NewHelmNativeUninstall(t)
	h := newRelationshipHarness(t, 90*time.Second)
	h.w.helmJournalPath = filepath.Join(t.TempDir(), "journal")
	audit := &nativeHelmAudit{}
	connectNativeHelmFixture(t, h, f, audit)
	result := runNativeHelmUninstall(t, h, f, audit)
	if result.Outcome() != kube.HelmExecutionCompleted {
		t.Fatal("native confirmed execution did not complete", h.w.helmUninstall.status)
	}
	writes := audit.writes()
	if len(writes) != 4 || writes[0].kind != "begin" || writes[3].kind != "finish" {
		t.Fatal("native UI did not submit one exact ordered retained-history batch")
	}
	for i, request := range writes {
		if request.status < 200 || request.status >= 300 {
			t.Fatal("native persistent request was not admitted by the real API server", i)
		}
	}
	for index, target := range f.Targets {
		request := writes[index+1]
		if request.kind != "delete" || request.path != "/api/v1/namespaces/"+f.Namespace+"/configmaps/"+target.Name ||
			request.uid != string(target.UID) || request.rv != target.ResourceVersion {
			t.Fatal("native conditional deletion lost the reviewed order or identity")
		}
	}
	assertNativeHelmTargets(t, h.ctx, f, 2)
	final := assertNativeHelmRetained(t, h.ctx, f, "uninstalled")
	if final.ResourceVersion == f.Storage.ResourceVersion || result.Storage().UID != string(final.UID) || result.Storage().ResourceVersion != final.ResourceVersion {
		t.Fatal("native completion report does not identify the actual retained revision")
	}
	nativeHelmE2EReach(h, "Helm uninstall outcome")
	saveNativeScreenshot(t, h.tt)
	h.tt.Frame()
	h.tt.Frame()
	if !reflect.DeepEqual(writes, audit.writes()) {
		t.Fatal("native completed attempt replayed without a new user action")
	}
	h.click("Close uninstall review")
	h.click("Refresh Helm metadata")
	h.pump(func() bool { return !h.w.helmReleases.busy })
	h.click("Select " + f.Storage.Name)
	h.click("Review recovery history")
	beforeHistory, _ := audit.snapshot()
	recovered := selectNativeHelmHistory(h, result.JournalID())
	afterHistory, _ := audit.snapshot()
	if !reflect.DeepEqual(beforeHistory, afterHistory) || recovered.Outcome() != kube.HelmExecutionCompleted ||
		!reflect.DeepEqual(recovered.Effects(), result.Effects()) || !reflect.DeepEqual(recovered.Targets(), result.Targets()) {
		t.Fatal("native completed history changed or silently fetched cluster data")
	}
	h.click("Close uninstall review")
	h.click("Disconnect")
	if h.w.helmUninstall.open || h.w.backend != nil || !reflect.DeepEqual(writes, audit.writes()) {
		t.Fatal("native teardown retained capability or submitted another effect")
	}
}

func TestNativeHelmRecoveryAgainstRealCluster(t *testing.T) {
	f := testcluster.NewHelmNativeUninstall(t)
	path := filepath.Join(t.TempDir(), "journal")
	first := newRelationshipHarness(t, 90*time.Second)
	first.w.helmJournalPath = path
	originalAudit := &nativeHelmAudit{loseDelete: true}
	connectNativeHelmFixture(t, first, f, originalAudit)
	originalBackend, originalSession := first.w.backend, first.w.sessionID
	result := runNativeHelmUninstall(t, first, f, originalAudit)
	_, lost := originalAudit.snapshot()
	writes := originalAudit.writes()
	if result.Outcome() != kube.HelmExecutionUnknown || !lost || len(writes) != 2 || writes[0].kind != "begin" || writes[1].kind != "delete" ||
		writes[1].status < 200 || writes[1].status >= 300 || writes[1].uid != string(f.Targets[0].UID) {
		t.Fatal("native Unknown did not follow the first real committed DELETE response loss", first.w.helmUninstall.status)
	}
	assertNativeHelmTargets(t, first.ctx, f, 1)
	assertNativeHelmRetained(t, first.ctx, f, "uninstalling")
	oldTargets, oldEffects := result.Targets(), result.Effects()
	first.click("Close uninstall review")
	first.click("Disconnect")
	first.w.Close()

	// A new window reads the same on-disk directory while still disconnected.
	// No Backend or previous session is copied from the original Workbench.
	fresh := newRelationshipHarness(t, 90*time.Second)
	fresh.w.helmJournalPath = path
	fresh.tt.Frame()
	fresh.click("Helm recovery")
	offline := selectNativeHelmHistory(fresh, result.JournalID())
	if fresh.w.backend != nil || fresh.w.sessionID != "" || offline.Outcome() != kube.HelmExecutionUnknown ||
		!reflect.DeepEqual(offline.Targets(), oldTargets) || !reflect.DeepEqual(offline.Effects(), oldEffects) ||
		!reflect.DeepEqual(writes, originalAudit.writes()) {
		t.Fatal("offline native recovery copied authority, changed history or replayed a request")
	}
	fresh.click("Close uninstall review")
	reads := &nativeHelmAudit{}
	connectNativeHelmFixture(t, fresh, f, reads)
	if fresh.w.backend == originalBackend || fresh.w.sessionID == originalSession || fresh.w.sessionID == "" {
		t.Fatal("recovery reused the old Backend or session")
	}
	selectNativeHelmFixture(fresh, f)
	current := fresh.w.helmReleases.selected
	if current.Status != "uninstalling" || current.UID != string(f.Storage.UID) || current.ResourceVersion == f.Storage.ResourceVersion {
		t.Fatal("fresh native selection did not observe the current retained revision")
	}
	fresh.click("Review recovery history")
	beforeHistory, _ := reads.snapshot()
	recovered := selectNativeHelmHistory(fresh, result.JournalID())
	beforeConsent, _ := reads.snapshot()
	if !reflect.DeepEqual(beforeHistory, beforeConsent) || recovered.Outcome() != kube.HelmExecutionUnknown {
		t.Fatal("native history selection silently observed the cluster")
	}
	nativeHelmE2EClick(fresh, "Observe current metadata")
	afterDisabled, _ := reads.snapshot()
	if !reflect.DeepEqual(beforeConsent, afterDisabled) || fresh.w.helmUninstall.observation != nil {
		t.Fatal("native recovery observed without fresh GET confirmation")
	}
	nativeHelmE2EClick(fresh, helmRecoveryConsentLabel)
	afterAcknowledgement, _ := reads.snapshot()
	if !fresh.w.helmUninstall.reconcileAck || !reflect.DeepEqual(beforeConsent, afterAcknowledgement) {
		t.Fatal("native GET acknowledgement was missing or sent a request itself")
	}
	nativeHelmE2EClick(fresh, "Observe current metadata")
	fresh.pump(func() bool { return fresh.w.helmUninstall.busy == "" })
	observation := fresh.w.helmUninstall.observation
	if observation == nil || observation.ID() != recovered.ID() || observation.Revision() != current ||
		observation.HistoryOutcome() != kube.HelmExecutionUnknown || fresh.w.helmUninstall.reconcileAck {
		t.Fatal("fresh native GET result lost its independent identity or history", fresh.w.helmUninstall.status)
	}
	items := observation.Items()
	if len(items) != 2 || items[0].State != kube.HelmRecoveryTargetAbsent || items[1].State != kube.HelmRecoveryTargetOriginal ||
		items[0].Target != oldTargets[0] || items[1].Target != oldTargets[1] || items[1].UID != string(f.Targets[1].UID) || items[1].ResourceVersion != f.Targets[1].ResourceVersion {
		t.Fatal("native current facts did not distinguish the deleted and unsubmitted original targets")
	}
	afterObservation, _ := reads.snapshot()
	for _, request := range afterObservation {
		if request.method != http.MethodGet {
			t.Fatal("native recovery sent a mutation or dry-run request")
		}
	}
	if recovered.Outcome() != kube.HelmExecutionUnknown || !reflect.DeepEqual(recovered.Targets(), oldTargets) ||
		!reflect.DeepEqual(recovered.Effects(), oldEffects) || !reflect.DeepEqual(writes, originalAudit.writes()) {
		t.Fatal("native GET observation rewrote history or restarted old execution")
	}
	nativeHelmE2EClick(fresh, "Observe current metadata")
	afterRepeatedClick, _ := reads.snapshot()
	if !reflect.DeepEqual(afterObservation, afterRepeatedClick) {
		t.Fatal("native consumed GET consent was replayed")
	}
	nativeHelmE2EReach(fresh, "Helm recovery current target 2")
	saveNativeScreenshot(t, fresh.tt)
	again := selectNativeHelmHistory(fresh, result.JournalID())
	if again.Outcome() != kube.HelmExecutionUnknown || !reflect.DeepEqual(again.Effects(), oldEffects) || !reflect.DeepEqual(again.Targets(), oldTargets) {
		t.Fatal("reopening history after observation lost uncertainty")
	}
	assertNativeHelmTargets(t, fresh.ctx, f, 1)
	assertNativeHelmRetained(t, fresh.ctx, f, "uninstalling")
	if len(reads.writes()) != 0 || !reflect.DeepEqual(writes, originalAudit.writes()) {
		t.Fatal("native recovery modified the actual retained server state")
	}
	fresh.click("Close uninstall review")
	fresh.click("Disconnect")
}
