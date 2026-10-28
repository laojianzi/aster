package uiworkbench

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/helmrender"
	"github.com/laojianzi/aster/internal/kube"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

// Every candidate used by this UI suite comes from the approved distribution
// and the real isolated Helm capture. A family may share that immutable capture;
// its children still have independent connections, preparations and journals.
type nativeInstallInput struct {
	request   helmrender.InstallRequest
	candidate *helmrelease.InstallCandidate
}

// All new roots join the parallel tail after the unchanged sequential suite.
// Rendering itself stays strictly serial, including every child and cleanup.
// Pure controller roots never construct a Tester. Lock order is renderer,
// capture, then the short immutable-cache lock; never the reverse.
var nativeInstallRendererMu sync.Mutex
var nativeInstallCaptureMu sync.Mutex

func nativeInstallSerialRenderer(t *testing.T) {
	t.Helper()
	nativeInstallRendererMu.Lock()
	// Register before any fixture or context so LIFO cleanup joins all workers
	// and closes every fixture before another root may render.
	t.Cleanup(nativeInstallRendererMu.Unlock)
}

// A real controller capture is asynchronous. Keep the gate until its result
// callback is available/settled (CaptureInstall has returned and released its
// own process admission). If an assertion or context timeout exits early, join
// the owning worker before releasing the gate, including t.Fatal's Goexit path.
func nativeInstallCaptureStep(h *relationshipHarness, action func()) {
	h.t.Helper()
	nativeInstallCaptureMu.Lock()
	settled := false
	defer func() {
		if !settled {
			h.w.Close()
		}
		nativeInstallCaptureMu.Unlock()
	}()
	action()
	settled = true
}

// The expensive approved archive verification/extraction is not what these UI
// lifecycle families exercise. Reuse only a still-valid result of that real
// capture for this one fixed chart/values profile. Never change its timestamps
// or retain a Backend, session, preparation, dry-run, grant or journal here.
// Source paths may already have been cleaned by the originating test: later
// stages are expressly required to use the frozen bytes. Tests that capture
// again always construct their own still-present private input files.
var nativeInstallCaptures = struct {
	sync.Mutex
	byProfile map[struct {
		namespace string
		count     int
	}]nativeInstallInput
}{byProfile: make(map[struct {
	namespace string
	count     int
}]nativeInstallInput)}

func nativeInstallRememberCapture(in nativeInstallInput) {
	if in.candidate == nil || !in.candidate.Valid(time.Now()) {
		return
	}
	key := struct {
		namespace string
		count     int
	}{in.request.Namespace, len(in.candidate.Targets())}
	nativeInstallCaptures.Lock()
	nativeInstallCaptures.byProfile[key] = in
	nativeInstallCaptures.Unlock()
}

func nativeInstallFiles(t *testing.T, namespace string, count int, copyDistribution bool) nativeInstallInput {
	t.Helper()
	archive := os.Getenv("ASTER_HELM_ARCHIVE")
	if archive == "" {
		t.Skip("ASTER_HELM_ARCHIVE is required for the approved real Helm capture")
	}
	dir := t.TempDir()
	if copyDistribution {
		raw, err := os.ReadFile(archive)
		nativeHelmJournalMust(t, err)
		archive = filepath.Join(dir, filepath.Base(archive))
		nativeHelmJournalMust(t, os.WriteFile(archive, raw, 0600))
	}
	var manifest strings.Builder
	for i := 0; i < count; i++ {
		fmt.Fprintf(&manifest, "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: config-%02d\n  namespace: {{ .Release.Namespace }}\ndata:\n  message: {{ .Values.message | quote }}\n", i)
	}
	var archiveBytes bytes.Buffer
	z := gzip.NewWriter(&archiveBytes)
	tarWriter := tar.NewWriter(z)
	for _, entry := range []struct{ name, body string }{
		{"fixture/Chart.yaml", "apiVersion: v2\nname: fixture\nversion: 0.1.0\n"},
		{"fixture/values.yaml", "message: chart-default\n"},
		{"fixture/templates/configmaps.yaml", manifest.String()},
	} {
		nativeHelmJournalMust(t, tarWriter.WriteHeader(&tar.Header{Name: entry.name, Mode: 0600, Size: int64(len(entry.body))}))
		_, err := io.WriteString(tarWriter, entry.body)
		nativeHelmJournalMust(t, err)
	}
	nativeHelmJournalMust(t, tarWriter.Close())
	nativeHelmJournalMust(t, z.Close())
	chart, values := filepath.Join(dir, "chart.tgz"), filepath.Join(dir, "values.yaml")
	nativeHelmJournalMust(t, os.WriteFile(chart, archiveBytes.Bytes(), 0600))
	nativeHelmJournalMust(t, os.WriteFile(values, []byte("message: PRIVATE_NATIVE_INSTALL_FROZEN\n"), 0600))
	return nativeInstallInput{request: helmrender.InstallRequest{HelmArchive: archive, ChartArchive: chart,
		ValuesFile: values, Release: "example", Namespace: namespace, Trusted: true,
		CapabilityAssumption: helmrelease.InstallCapabilityAssumption}}
}

func nativeInstallCachedCapture(namespace string, count int) (nativeInstallInput, bool) {
	key := struct {
		namespace string
		count     int
	}{namespace, count}
	nativeInstallCaptures.Lock()
	in, found := nativeInstallCaptures.byProfile[key]
	nativeInstallCaptures.Unlock()
	now := time.Now()
	if found && in.candidate.Valid(now) && in.candidate.ExpiresAt().Sub(now) >= 20*time.Second {
		return in, true
	}
	return nativeInstallInput{}, false
}

func nativeInstallCapture(t *testing.T, namespace string, count int) nativeInstallInput {
	t.Helper()
	if in, valid := nativeInstallCachedCapture(namespace, count); valid {
		return in
	}
	nativeInstallCaptureMu.Lock()
	defer nativeInstallCaptureMu.Unlock()
	// Another real capture may have completed while this root waited. Check
	// its original lifetime again; a concurrent miss must not duplicate work.
	if in, valid := nativeInstallCachedCapture(namespace, count); valid {
		return in
	}
	in := nativeInstallFiles(t, namespace, count, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var err error
	in.candidate, err = helmrender.CaptureInstall(ctx, in.request)
	nativeHelmJournalMust(t, err)
	if !in.candidate.Valid(time.Now()) || len(in.candidate.Targets()) != count {
		t.Fatal("approved real capture did not retain the complete chart inventory")
	}
	nativeInstallRememberCapture(in)
	return in
}

type nativeInstallCall struct {
	method, path, accept string
	dry                  bool
}

// The fixture implements the ordinary Kubernetes wire contract. Successful
// writes are applied to its independent object store before a response is lost.
// It neither fabricates candidates/grants nor reads the runner's private state.
type nativeInstallFixture struct {
	mu                  sync.Mutex
	server              *httptest.Server
	path                string
	objects, dryBodies  map[string][]byte
	calls               []nativeInstallCall
	writes, dryPosts    int
	bad                 []string
	mode                string
	faultAt             int
	entered, cancelled  chan struct{}
	enterOnce, doneOnce sync.Once
}

func newNativeInstallFixture(t *testing.T) *nativeInstallFixture {
	t.Helper()
	f := &nativeInstallFixture{path: filepath.Join(t.TempDir(), "journal"), objects: map[string][]byte{},
		dryBodies: map[string][]byte{}, entered: make(chan struct{}), cancelled: make(chan struct{}), faultAt: 1}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() {
		f.server.Close()
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.bad) != 0 {
			t.Errorf("unsafe native install request(s): %v", f.bad)
		}
	})
	return f
}

func (f *nativeInstallFixture) backend(t *testing.T, capacityLimited bool) *kube.Backend {
	t.Helper()
	cfg := &rest.Config{Host: f.server.URL, BearerToken: "native-install-current"}
	if capacityLimited {
		// The preparation and dry-run fit the initial burst. Even the best-case
		// 32-target execution cannot fit its unchanged 10-second rate budget.
		cfg.QPS, cfg.Burst = 1, 200
	} else {
		cfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()
	}
	b, err := kube.New(cfg)
	nativeHelmJournalMust(t, err)
	return b
}

func newNativeInstallHarness(t *testing.T, f *nativeInstallFixture, native bool) *relationshipHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	h := &relationshipHarness{w: New(), ctx: ctx, updates: make(chan func(), 128), t: t}
	h.w.attach(ctx, func(fn func()) {
		select {
		case h.updates <- fn:
		case <-ctx.Done():
		}
	})
	t.Cleanup(func() { cancel(); h.w.Close() })
	h.w.backend, h.w.namespace, h.w.sessionID = f.backend(t, false), "team", "native-install-session"
	h.w.activeContext, h.w.currentContext, h.w.activeNamespace = "test-context", "test-context", "team"
	h.w.helmJournalPath = f.path
	if native {
		h.tt = ui.NewTester(h.w.View, 1100, 700)
	}
	return h
}

// Controller-only lifecycle cases need no repeated layout or PNG work. Native
// interaction cases still render every input/action and the completed callback.
func nativeInstallDrain(h *relationshipHarness, done func() bool) {
	h.t.Helper()
	for !done() {
		select {
		case fn := <-h.updates:
			fn()
		case <-h.ctx.Done():
			h.t.Fatalf("native installation worker did not settle: %s", h.w.helmInstall.status)
		}
	}
	if h.tt != nil {
		h.tt.Frame()
	}
}

func nativeInstallNextCallback(h *relationshipHarness) func() {
	h.t.Helper()
	select {
	case fn := <-h.updates:
		return fn
	case <-h.ctx.Done():
		h.t.Fatal("native install callback did not arrive")
		return nil
	}
}

func nativeInstallUseCapture(h *relationshipHarness, in nativeInstallInput) {
	h.t.Helper()
	h.w.openHelmInstall()
	s := &h.w.helmInstall
	s.archive, s.chart, s.values, s.release = in.request.HelmArchive, in.request.ChartArchive, in.request.ValuesFile, in.request.Release
	s.trusted, s.capabilityAck = true, true
	s.candidate, s.capturedRequest = in.candidate, in.request
	if !s.inputsMatch() || !s.candidate.Valid(time.Now()) {
		h.t.Fatal("family fixture lost its actual immutable capture or exact selected inputs")
	}
	if h.tt != nil {
		h.tt.Frame()
	}
}

func nativeInstallPrepare(h *relationshipHarness) {
	h.t.Helper()
	h.w.helmInstall.prepareAck = true
	h.w.prepareHelmInstall()
	nativeInstallDrain(h, func() bool { return h.w.helmInstall.busy == "" })
	if h.w.helmInstall.preparation == nil {
		h.t.Fatal("native installation preparation failed", h.w.helmInstall.status)
	}
}

func nativeInstallPreflight(h *relationshipHarness) {
	h.t.Helper()
	nativeInstallPrepare(h)
	h.w.helmInstall.dryRunAck = true
	h.w.preflightHelmInstall()
	nativeInstallDrain(h, func() bool { return h.w.helmInstall.busy == "" })
	s := &h.w.helmInstall
	if s.dryRun == nil || !s.dryRun.Matches(s.backend, s.candidate, s.session, time.Now()) {
		h.t.Fatal("native installation dry-run failed or lost its live origin", s.status)
	}
}

func nativeInstallExecute(h *relationshipHarness) *kube.HelmInstallExecution {
	h.t.Helper()
	h.w.helmInstall.executeAck, h.w.helmInstall.confirmation = true, h.w.helmInstall.candidate.Release()
	h.w.executeHelmInstall()
	nativeInstallDrain(h, func() bool { return h.w.pendingWrites == 0 && h.w.helmInstall.busy == "" })
	return h.w.helmInstall.execution
}

func nativeInstallReach(h *relationshipHarness, label string) {
	h.t.Helper()
	h.tt.Frame()
	viewport, ok := h.tt.Find("Helm install inventory")
	if !ok {
		h.t.Fatal("missing native install scroll viewport")
	}
	// Start at the top only when the requested control is not already visible.
	visible := func() bool {
		b, found := h.tt.Find(label)
		return found && b.W > 0 && b.H > 0 && b.X >= viewport.X && b.X+b.W <= viewport.X+viewport.W &&
			b.Y > viewport.Y && b.Y+b.H < viewport.Y+viewport.H
	}
	if visible() {
		return
	}
	h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, -100000)
	h.tt.Frame()
	for i := 0; i < 80; i++ {
		if visible() {
			return
		}
		h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, viewport.H/2)
		h.tt.Frame()
	}
	b, found := h.tt.Find(label)
	h.t.Fatalf("unreachable install control %q: found=%t bounds=%+v viewport=%+v", label, found, b, viewport)
}

func nativeInstallClick(h *relationshipHarness, label string) {
	h.t.Helper()
	nativeInstallReach(h, label)
	h.click(label)
}

func nativeInstallEdit(h *relationshipHarness, label, value string) {
	h.t.Helper()
	nativeInstallClick(h, label)
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type(value)
	h.tt.Frame()
}

func nativeInstallOpenAndFill(h *relationshipHarness, in nativeInstallInput) {
	h.t.Helper()
	h.click("Helm releases")
	h.click("Install Helm release")
	if !h.w.helmInstall.open || h.w.helmInstall.candidate != nil || h.w.helmInstall.prepareAck || h.w.helmInstall.dryRunAck || h.w.helmInstall.executeAck {
		h.t.Fatal("opening native install acquired authority")
	}
	for _, field := range []struct{ label, value string }{
		{"Helm install distribution archive", in.request.HelmArchive},
		{"Helm install chart archive", in.request.ChartArchive},
		{"Helm install values file", in.request.ValuesFile},
		{"Helm install release name", in.request.Release},
	} {
		nativeInstallEdit(h, field.label, field.value)
	}
}

func (f *nativeInstallFixture) counts() (calls, dry, writes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls), f.dryPosts, f.writes
}

func (f *nativeInstallFixture) setFault(mode string, at int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode, f.faultAt = mode, at
}

func (f *nativeInstallFixture) object(key string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return bytes.Clone(f.objects[key])
}

func (f *nativeInstallFixture) binding(t *testing.T) string {
	t.Helper()
	ca := sha256.Sum256(nil)
	raw, err := json.Marshal(struct {
		Purpose, Endpoint, ServerName, CA string
		Insecure                          bool
		NextProtos                        []string
		Impersonate                       rest.ImpersonationConfig
	}{"aster/helm-uninstall/connection/v1", f.server.URL, "", hex.EncodeToString(ca[:]), false, nil, rest.ImpersonationConfig{}})
	nativeHelmJournalMust(t, err)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func nativeInstallMetadata(raw []byte) metav1.PartialObjectMetadata {
	var m metav1.PartialObjectMetadata
	_ = json.Unmarshal(raw, &m)
	m.APIVersion, m.Kind = "meta.k8s.io/v1", "PartialObjectMetadata"
	return m
}

func (f *nativeInstallFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, nativeInstallCall{r.Method, r.URL.Path, r.Header.Get("Accept"), r.URL.Query().Get("dryRun") == "All"})
	mode, faultAt := f.mode, f.faultAt
	if r.Header.Get("Authorization") != "Bearer native-install-current" {
		f.bad = append(f.bad, "wrong credential")
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	const prefix = "/api/v1/namespaces/team/"
	if r.Method == http.MethodGet {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1":
			_ = json.NewEncoder(w).Encode(metav1.APIResourceList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "APIResourceList"}, GroupVersion: "v1", APIResources: []metav1.APIResource{
				{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: metav1.Verbs{"get", "create"}},
				{Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "create", "patch"}},
				{Name: "namespaces", Kind: "Namespace", Verbs: metav1.Verbs{"get"}},
			}})
		case "/api/v1/namespaces/team":
			_ = json.NewEncoder(w).Encode(metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadata"}, ObjectMeta: metav1.ObjectMeta{Name: "team", UID: "namespace-uid", ResourceVersion: "100"}})
		case prefix + "secrets":
			list := metav1.PartialObjectMetadataList{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadataList"}, ListMeta: metav1.ListMeta{ResourceVersion: "list-rv"}, Items: []metav1.PartialObjectMetadata{}}
			keys := make([]string, 0, len(f.objects))
			for key := range f.objects {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if strings.HasPrefix(key, "secrets/") {
					m := nativeInstallMetadata(f.objects[key])
					if m.Labels["owner"] == "helm" {
						list.Items = append(list.Items, m)
					}
				}
			}
			_ = json.NewEncoder(w).Encode(list)
		default:
			key := strings.TrimPrefix(r.URL.Path, prefix)
			parts := strings.Split(key, "/")
			if len(parts) != 2 || parts[0] != "configmaps" && parts[0] != "secrets" {
				f.bad = append(f.bad, "unexpected read "+r.URL.Path)
				w.WriteHeader(500)
				return
			}
			raw := f.objects[key]
			if len(raw) == 0 {
				w.WriteHeader(404)
				_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Code: 404, Reason: metav1.StatusReasonNotFound, Details: &metav1.StatusDetails{Name: parts[1], Kind: parts[0]}})
			} else if strings.Contains(r.Header.Get("Accept"), "PartialObjectMetadata") {
				_ = json.NewEncoder(w).Encode(nativeInstallMetadata(raw))
			} else {
				_, _ = w.Write(raw)
			}
		}
		return
	}
	f.mu.Lock()
	bad := r.Method != http.MethodPost && r.Method != http.MethodPatch
	query := r.URL.Query()
	dry := query.Get("dryRun") == "All"
	queryKeys := 2
	if dry {
		queryKeys++
	}
	if query.Get("fieldValidation") != "Strict" || query.Get("fieldManager") != helmrelease.InstallFieldManager || len(query) != queryKeys || r.Header.Get("Accept-Encoding") != "identity" {
		bad = true
	}
	resource := strings.TrimPrefix(r.URL.Path, prefix)
	if r.Method == http.MethodPost {
		bad = bad || resource != "secrets" && resource != "configmaps" || r.Header.Get("Content-Type") != "application/json"
	} else {
		bad = bad || dry || resource != "secrets/sh.helm.release.v1.example.v1" || r.Header.Get("Content-Type") != "application/json-patch+json"
	}
	if bad {
		f.bad = append(f.bad, "unexpected mutation "+r.Method+" "+r.URL.RequestURI())
	}
	f.mu.Unlock()
	if bad {
		w.WriteHeader(405)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, helmrelease.MaxSecretBytes+1))
	if err != nil || len(raw) > helmrelease.MaxSecretBytes {
		w.WriteHeader(400)
		return
	}
	var obj map[string]any
	if r.Method == http.MethodPatch {
		patch, patchErr := jsonpatch.DecodePatch(raw)
		if patchErr != nil {
			w.WriteHeader(422)
			return
		}
		changed, patchErr := patch.Apply(f.object(resource))
		if patchErr != nil {
			w.WriteHeader(422)
			return
		}
		err = json.Unmarshal(changed, &obj)
	} else {
		err = json.Unmarshal(raw, &obj)
	}
	if err != nil {
		w.WriteHeader(400)
		return
	}
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		w.WriteHeader(400)
		return
	}
	name, _ := meta["name"].(string)
	key := resource
	if r.Method == http.MethodPost {
		key += "/" + name
	}
	if dry {
		f.mu.Lock()
		f.dryPosts++
		f.dryBodies[key] = bytes.Clone(raw)
		f.mu.Unlock()
		meta["uid"], meta["creationTimestamp"] = "dry-only-uid", time.Now().UTC().Format(time.RFC3339Nano)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(obj)
		return
	}
	f.mu.Lock()
	f.writes++
	n := f.writes
	if r.Method == http.MethodPost && !bytes.Equal(raw, f.dryBodies[key]) {
		f.bad = append(f.bad, "persistent body differs from the independently authorized frozen dry-run body")
	}
	f.mu.Unlock()
	if mode == "reject" && n == faultAt {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Code: 403, Reason: metav1.StatusReasonForbidden, Details: &metav1.StatusDetails{Name: name, Kind: strings.Split(resource, "/")[0]}})
		return
	}
	if r.Method == http.MethodPost {
		meta["uid"], meta["creationTimestamp"] = fmt.Sprintf("persistent-%d", n), time.Now().UTC().Format(time.RFC3339Nano)
	}
	meta["resourceVersion"] = fmt.Sprintf("rv-%d", n)
	stored, err := json.Marshal(obj)
	if err != nil {
		w.WriteHeader(500)
		return
	}
	f.mu.Lock()
	f.objects[key] = stored
	f.mu.Unlock()
	if n == faultAt {
		switch mode {
		case "loss":
			conn, _, hijackErr := w.(http.Hijacker).Hijack()
			if hijackErr == nil {
				_ = conn.Close()
			}
			return
		case "block":
			f.enterOnce.Do(func() { close(f.entered) })
			<-r.Context().Done()
			f.doneOnce.Do(func() { close(f.cancelled) })
			return
		}
	}
	status := http.StatusCreated
	if r.Method == http.MethodPatch {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(stored)
}

func nativeInstallDiskSnapshot(t *testing.T, path string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(path, func(file string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(path, file)
		if err != nil {
			return err
		}
		out[rel], err = os.ReadFile(file)
		return err
	})
	if !os.IsNotExist(err) {
		nativeHelmJournalMust(t, err)
	}
	return out
}

func nativeInstallAssertDiskUnchanged(t *testing.T, path string, before map[string][]byte) {
	t.Helper()
	if !reflect.DeepEqual(before, nativeInstallDiskSnapshot(t, path)) {
		t.Fatal("read-only native history or rejected admission changed durable WAL/guard bytes")
	}
}

func nativeInstallLoad(t *testing.T, path, id string) helmjournal.Snapshot {
	t.Helper()
	s, err := helmjournal.OpenExisting(path)
	nativeHelmJournalMust(t, err)
	defer s.Close()
	snap, err := s.Load(id)
	nativeHelmJournalMust(t, err)
	return snap
}
