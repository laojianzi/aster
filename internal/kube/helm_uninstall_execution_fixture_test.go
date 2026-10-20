package kube

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/helmrelease"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

// This server first runs the real existing preparation and server dry-run. Its
// persistent phase independently applies JSON Patch tests and mutations, records
// actual request preconditions, and retains the resulting Secret and resources.
// The execution result never supplies the fixture's expected outcome.
type helmExecutionRequest struct {
	kind, name string
	body       []byte
}

type helmExecutionFixture struct {
	mu                     sync.Mutex
	active                 atomic.Bool
	dry                    *dryRunFixture
	b                      *Backend
	journal                *HelmUninstallJournal
	journalDir             string
	p                      *HelmUninstallPreparation
	preflight              *HelmUninstallDryRun
	dryConsent             *HelmUninstallDryRunConsent
	wire, original         []byte
	names                  []string
	objects                map[string]metav1.PartialObjectMetadata
	deleted                map[string]bool
	absenceReads           map[string]int
	requests               []helmExecutionRequest
	events                 []string
	bad                    []string
	mode                   string
	faultAt                int
	patches, deletes       int
	dryPatches, dryDeletes int
	onWrite                func(string)
	onRead                 func(string)
	onReject               func(*metav1.Status)
}

func newHelmExecutionFixture(t *testing.T, two bool) *helmExecutionFixture {
	t.Helper()
	names := []string{"sample"}
	manifest := liveManifest
	if two {
		names = append(names, "second")
		manifest += "---\n" + strings.Replace(liveManifest, "name: sample", "name: second", 1)
	}
	manifestJSON, _ := json.Marshal(manifest)
	// Lexemes and whitespace in unrelated sensitive fields must survive both
	// storage transitions; an encoding/json round trip would lose this spelling.
	payload := []byte(fmt.Sprintf(`{ "name":"example", "namespace":"team", "version":2,
"info": { "status":"deployed" },
"chart":{"metadata":{"name":"example-chart","version":"1.2.3"},"values":{"password":"PRIVATE_DEFAULT"}},
"manifest":%s,"config":{"password":"PRIVATE_TOKEN","large":9007199254740993,"decimal":1.2300e+05,"text":"\u4e2d"} }`, manifestJSON))
	secret := helmfixture.Secret("team", "example", "deployed", 2)
	secret.Data["release"] = helmfixture.Stored(payload)
	wire, err := json.Marshal(secret)
	if err != nil {
		t.Fatal(err)
	}
	f := &helmExecutionFixture{dry: &dryRunFixture{wire: wire}, wire: bytes.Clone(wire), original: bytes.Clone(wire), names: names,
		objects: map[string]metav1.PartialObjectMetadata{}, deleted: map[string]bool{}, absenceReads: map[string]int{}}
	for _, name := range names {
		m := liveObject()
		if name == "second" {
			m.Name, m.UID, m.ResourceVersion = name, "second-uid", "rv-b"
		}
		f.objects[name] = m
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !f.active.Load() {
			f.dry.serve(w, r)
			return
		}
		f.serve(w, r)
	}))
	t.Cleanup(srv.Close)
	f.b, err = New(&rest.Config{Host: srv.URL + "/prefix", BearerToken: "current", RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()})
	if err != nil {
		t.Fatal(err)
	}
	f.dry.b = f.b
	f.journalDir = filepath.Join(t.TempDir(), "journal")
	f.journal, err = OpenHelmUninstallJournal(f.journalDir)
	if err != nil {
		t.Fatal("private journal", err)
	}
	t.Cleanup(func() {
		if err := f.journal.Close(); err != nil {
			t.Error("close journal", err)
		}
	})
	f.p, err = f.b.PrepareHelmUninstall(context.Background(), "team", selectedHelmRevision(), "session", true)
	if err != nil {
		t.Fatal("preparation", err)
	}
	f.dry.p = f.p
	f.dryConsent = f.dry.consent(t)
	f.preflight, err = f.b.PreflightHelmUninstall(context.Background(), f.dryConsent, "session", "team", selectedHelmRevision())
	if err != nil || f.preflight == nil || f.dry.bad {
		t.Fatal("real preflight", err)
	}
	f.active.Store(true)
	return f
}

func (f *helmExecutionFixture) consent(t *testing.T) *HelmUninstallExecutionConsent {
	t.Helper()
	c, err := f.b.AcknowledgeHelmUninstallExecution(f.preflight, "session", "team", selectedHelmRevision(), true, f.journal)
	if err != nil || c == nil {
		t.Fatal("execution acknowledgement", err)
	}
	return c
}

func (f *helmExecutionFixture) inspect(t *testing.T) ([]helmExecutionRequest, []string, corev1.Secret) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bad) != 0 {
		t.Fatal("request invariant", f.bad)
	}
	var secret corev1.Secret
	if err := json.Unmarshal(f.wire, &secret); err != nil {
		t.Fatal(err)
	}
	requests := make([]helmExecutionRequest, len(f.requests))
	for i, r := range f.requests {
		requests[i] = helmExecutionRequest{r.kind, r.name, bytes.Clone(r.body)}
	}
	return requests, append([]string(nil), f.events...), secret
}

func (f *helmExecutionFixture) fault(kind string) string {
	if f.faultAt != 0 && f.faultAt != len(f.requests) {
		return ""
	}
	return strings.TrimPrefix(f.mode, kind+"-")
}

func (f *helmExecutionFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/prefix")
	if r.Header.Get("Authorization") != "Bearer current" || !strings.HasPrefix(r.URL.Path, "/prefix/") {
		f.bad = append(f.bad, "changed credential or prefix")
	}
	event := r.Method + " " + path
	if r.URL.Query().Encode() == "dryRun=All" {
		event = "DRYRUN " + event
	}
	f.events = append(f.events, event)
	if r.Method == http.MethodGet {
		if f.onRead != nil {
			f.onRead(path)
		}
		f.read(w, r, path)
		return
	}
	if r.URL.RawQuery != "" && r.URL.Query().Encode() != "dryRun=All" || r.Header.Get("Accept-Encoding") != "identity" {
		f.bad = append(f.bad, "mutation query or content encoding")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, helmrelease.MaxSecretBytes+1))
	if err != nil || len(body) == 0 || len(body) > helmrelease.MaxSecretBytes {
		f.bad = append(f.bad, "unbounded or unreadable mutation body")
		w.WriteHeader(500)
		return
	}
	defer clear(body)
	name := path[strings.LastIndex(path, "/")+1:]
	if r.URL.Query().Encode() == "dryRun=All" {
		f.serveDryRun(w, r, path, name, body)
		return
	}
	kind := ""
	if r.Method == http.MethodPatch && path == "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v2" {
		kind = "begin"
		if f.patches > 0 {
			kind = "finish"
		}
		f.patches++
		if r.Header.Get("Content-Type") != "application/json-patch+json" || r.Header.Get("Accept") != "application/json" {
			f.bad = append(f.bad, "storage negotiation")
		}
	} else if r.Method == http.MethodDelete && path == "/api/v1/namespaces/team/configmaps/"+name && f.objects[name].Name == name {
		kind = "delete"
		f.deletes++
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != liveMetadataAccept {
			f.bad = append(f.bad, "DELETE negotiation")
		}
		var options metav1.DeleteOptions
		m := f.objects[name]
		if json.Unmarshal(body, &options) != nil || options.APIVersion != "v1" || options.Kind != "DeleteOptions" || options.Preconditions == nil ||
			options.Preconditions.UID == nil || options.Preconditions.ResourceVersion == nil || *options.Preconditions.UID != m.UID || *options.Preconditions.ResourceVersion != m.ResourceVersion ||
			options.PropagationPolicy == nil || *options.PropagationPolicy != metav1.DeletePropagationOrphan || len(options.DryRun) != 0 ||
			options.GracePeriodSeconds != nil || options.OrphanDependents != nil || options.IgnoreStoreReadErrorWithClusterBreakingPotential != nil {
			f.bad = append(f.bad, "DELETE widened or changed original identity")
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(body, &fields)
		if len(fields) != 4 {
			f.bad = append(f.bad, "unexpected DELETE field")
		}
	} else {
		f.bad = append(f.bad, "unexpected mutation "+r.Method+" "+path)
		w.WriteHeader(500)
		return
	}
	f.requests = append(f.requests, helmExecutionRequest{kind, name, bytes.Clone(body)})
	fault := f.fault(kind)
	if fault == "anonymous422" {
		w.WriteHeader(422)
		_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: metav1.StatusFailure,
			Reason: metav1.StatusReasonInvalid, Code: 422, Message: "PRIVATE_JSON_PATCH_TEST", Details: &metav1.StatusDetails{}})
		return
	}
	if strings.HasPrefix(fault, "reject-") {
		code, _ := strconv.Atoi(strings.TrimPrefix(fault, "reject-"))
		reason := map[int]metav1.StatusReason{400: metav1.StatusReasonBadRequest, 401: metav1.StatusReasonUnauthorized, 403: metav1.StatusReasonForbidden,
			404: metav1.StatusReasonNotFound, 409: metav1.StatusReasonConflict, 422: metav1.StatusReasonInvalid}[code]
		resource, uid := "secrets", selectedHelmRevision().UID
		if kind == "delete" {
			resource, uid = "configmaps", string(f.objects[name].UID)
		}
		w.WriteHeader(code)
		status := metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: metav1.StatusFailure,
			Reason: reason, Code: int32(code), Message: "PRIVATE_ADMISSION", Details: &metav1.StatusDetails{Name: name, Kind: resource, UID: typesUID(uid)}}
		if f.onReject != nil {
			f.onReject(&status)
		}
		_ = json.NewEncoder(w).Encode(status)
		return
	}
	if strings.HasPrefix(fault, "bare-") {
		code, _ := strconv.Atoi(strings.TrimPrefix(fault, "bare-"))
		w.WriteHeader(code)
		_, _ = w.Write([]byte("PRIVATE_PROXY_BODY"))
		return
	}
	if fault == "redirect" {
		w.Header().Set("Location", "/prefix/unexpected")
		w.WriteHeader(307)
		return
	}
	var out []byte
	if kind == "delete" {
		f.deleted[name] = true
		object := f.objects[name]
		m := object.DeepCopy()
		now, zero := metav1.Now(), int64(0)
		m.DeletionTimestamp, m.DeletionGracePeriodSeconds = &now, &zero
		m.Finalizers = []string{metav1.FinalizerOrphanDependents}
		m.ResourceVersion += "-deleting"
		out, _ = json.Marshal(m)
		if strings.HasPrefix(fault, "status") {
			s := metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: metav1.StatusSuccess,
				Details: &metav1.StatusDetails{Name: name, Kind: "configmaps", UID: m.UID}}
			switch fault {
			case "status-code":
				s.Code = 200
			case "status-no-uid":
				s.Details.UID = ""
			case "status-wrong-name":
				s.Details.Name = "other"
			case "status-message":
				s.Message = "PRIVATE_UNEXPECTED"
			case "status-failure":
				s.Status = metav1.StatusFailure
			case "status-cause":
				s.Details.Causes = []metav1.StatusCause{{Message: "PRIVATE_CAUSE"}}
			}
			out, _ = json.Marshal(s)
		}
	} else {
		var before corev1.Secret
		_ = json.Unmarshal(f.wire, &before)
		if reason := checkExecutionPatch(body, before, kind); reason != "" {
			f.bad = append(f.bad, reason)
		}
		patch, e := jsonpatch.DecodePatch(body)
		if e == nil {
			out, e = patch.Apply(f.wire)
		}
		if e != nil {
			f.bad = append(f.bad, "conditional JSON Patch failed")
			w.WriteHeader(409)
			return
		}
		var after corev1.Secret
		if json.Unmarshal(out, &after) != nil {
			f.bad = append(f.bad, "patch did not produce Secret")
		}
		if reason := checkExecutionPreservation(before, after, kind); reason != "" {
			f.bad = append(f.bad, reason)
		}
		after.ResourceVersion = strconv.Itoa(7 + f.patches)
		out, _ = json.Marshal(after)
		clear(f.wire)
		f.wire = bytes.Clone(out)
	}
	if f.onWrite != nil {
		f.onWrite(kind)
	}
	if fault == "drop" {
		conn, _, e := w.(http.Hijacker).Hijack()
		if e == nil {
			_ = conn.Close()
		}
		clear(out)
		return
	}
	if fault == "cancel" {
		clear(out)
		return
	}
	switch fault {
	case "invalid":
		out = []byte("PRIVATE_INVALID_JSON")
	case "trailing":
		out = append(out, []byte(" {}")...)
	case "duplicate":
		out = append([]byte(`{"kind":"Secret",`), out[1:]...)
	case "media":
		w.Header().Set("Content-Type", "text/plain")
	case "warning":
		w.Header().Set("Warning", "299 fixture PRIVATE_WARNING")
	case "encoding":
		w.Header().Set("Content-Encoding", "gzip")
	case "large-header":
		w.Header().Set("Content-Length", strconv.Itoa(helmrelease.MaxSecretBytes+maxLiveObjectBytes+1))
		clear(out)
		return
	case "large-stream":
		out = bytes.Repeat([]byte("x"), helmrelease.MaxSecretBytes+maxLiveObjectBytes+1)
	case "202":
		w.WriteHeader(202)
	}
	if strings.HasPrefix(fault, "change-") {
		var object map[string]any
		_ = json.Unmarshal(out, &object)
		meta := object["metadata"].(map[string]any)
		switch strings.TrimPrefix(fault, "change-") {
		case "uid":
			meta["uid"] = "replacement"
		case "name":
			meta["name"] = "other"
		case "ns":
			meta["namespace"] = "other"
		case "empty-rv":
			meta["resourceVersion"] = ""
		case "same-rv":
			if kind == "delete" {
				meta["resourceVersion"] = f.objects[name].ResourceVersion
			} else {
				meta["resourceVersion"] = strconv.Itoa(6 + f.patches)
			}
		case "unknown":
			object["PRIVATE_UNKNOWN"] = "PRIVATE_VALUE"
		case "payload":
			object["data"].(map[string]any)["release"] = "UFJJVkFURQ=="
		case "status":
			meta["labels"].(map[string]any)["status"] = "failed"
		case "full-kind":
			object["kind"], object["apiVersion"] = "ConfigMap", "v1"
		case "finalizer":
			meta["finalizers"] = []string{"private/hold"}
		case "not-terminating":
			delete(meta, "deletionTimestamp")
		case "marker":
			delete(meta["labels"].(map[string]any), "app.kubernetes.io/managed-by")
		}
		out, _ = json.Marshal(object)
	}
	_, _ = w.Write(out)
	clear(out)
}

func (f *helmExecutionFixture) serveDryRun(w http.ResponseWriter, r *http.Request, path, name string, body []byte) {
	if r.Method == http.MethodPatch && path == "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v2" {
		f.dryPatches++
		patch, err := jsonpatch.DecodePatch(body)
		var out []byte
		if err == nil {
			out, err = patch.Apply(f.wire)
		}
		if err != nil {
			f.bad = append(f.bad, "new dry-run used stale storage identity")
			w.WriteHeader(409)
			return
		}
		var s corev1.Secret
		_ = json.Unmarshal(out, &s)
		s.ResourceVersion = "hypothetical-dryrun-rv"
		_ = json.NewEncoder(w).Encode(s)
		clear(out)
		return
	}
	object, ok := f.objects[name]
	if r.Method != http.MethodDelete || !ok || path != "/api/v1/namespaces/team/configmaps/"+name {
		f.bad = append(f.bad, "unexpected dry-run request")
		w.WriteHeader(500)
		return
	}
	f.dryDeletes++
	var options metav1.DeleteOptions
	if json.Unmarshal(body, &options) != nil || !reflect.DeepEqual(options.DryRun, []string{metav1.DryRunAll}) ||
		options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != object.UID ||
		options.Preconditions.ResourceVersion == nil || *options.Preconditions.ResourceVersion != object.ResourceVersion ||
		options.PropagationPolicy == nil || *options.PropagationPolicy != metav1.DeletePropagationOrphan {
		f.bad = append(f.bad, "fresh dry-run widened target")
	}
	m := object.DeepCopy()
	now, zero := metav1.Now(), int64(0)
	m.DeletionTimestamp, m.DeletionGracePeriodSeconds = &now, &zero
	m.Finalizers = []string{metav1.FinalizerOrphanDependents}
	m.ResourceVersion = "hypothetical-dryrun-rv"
	_ = json.NewEncoder(w).Encode(m)
}

func (f *helmExecutionFixture) read(w http.ResponseWriter, r *http.Request, path string) {
	switch path {
	case "/api/v1/namespaces/team/secrets":
		if r.Header.Get("Accept") != helmrelease.Accept || r.URL.Query().Get("labelSelector") != "owner=helm" {
			f.bad = append(f.bad, "unbounded storage list")
		}
		var secret corev1.Secret
		_ = json.Unmarshal(f.wire, &secret)
		row := metav1.PartialObjectMetadata{ObjectMeta: *secret.ObjectMeta.DeepCopy()}
		rows := []metav1.PartialObjectMetadata{row}
		if f.mode == "pre-source-uid" {
			rows[0].UID = "replacement"
		}
		if f.mode == "pre-source-rv" || f.mode == "source-after-begin" && f.patches > 0 || f.mode == "source-after-delete" && f.deletes > 0 {
			rows[0].ResourceVersion = "changed"
		}
		if f.mode == "pre-extra-revision" || f.mode == "revision-after-begin" && f.patches > 0 || f.mode == "revision-after-delete" && f.deletes > 0 {
			next := row.DeepCopy()
			next.Name, next.UID = "sh.helm.release.v1.example.v3", "uid3"
			next.Labels["version"], next.Labels["status"] = "3", "pending-upgrade"
			rows = append(rows, *next)
		}
		_, _ = w.Write(releasePage("", "collection", rows...))
	case "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v2":
		if r.Header.Get("Accept") != "application/json" || r.URL.RawQuery != "" {
			f.bad = append(f.bad, "storage GET fallback")
		}
		if f.mode == "pre-storage-denied" {
			w.WriteHeader(403)
			_, _ = w.Write([]byte("PRIVATE_SECRET_ERROR"))
			return
		}
		if f.mode == "payload-after-begin" && f.patches > 0 {
			var s corev1.Secret
			_ = json.Unmarshal(f.wire, &s)
			s.Data["private-extra"] = []byte("PRIVATE_CHANGED")
			_ = json.NewEncoder(w).Encode(s)
			return
		}
		_, _ = w.Write(f.wire)
	case "/api/v1":
		_ = json.NewEncoder(w).Encode(liveDiscovery())
	default:
		name := path[strings.LastIndex(path, "/")+1:]
		m, ok := f.objects[name]
		if !ok || path != "/api/v1/namespaces/team/configmaps/"+name || r.Header.Get("Accept") != liveMetadataAccept || r.URL.RawQuery != "" {
			f.bad = append(f.bad, "unexpected read or full-object fallback "+path)
			w.WriteHeader(500)
			return
		}
		m = *m.DeepCopy()
		if !f.deleted[name] {
			switch f.mode {
			case "pre-target-uid":
				m.UID = "replacement"
			case "pre-target-rv":
				m.ResourceVersion = "changed"
			case "pre-target-keep":
				m.Annotations["helm.sh/resource-policy"] = "keep"
			case "pre-target-hook":
				m.Annotations["helm.sh/hook"] = "test"
			case "pre-target-finalizer":
				m.Finalizers = []string{"private/hold"}
			case "pre-target-owner":
				m.OwnerReferences = []metav1.OwnerReference{{UID: "owner"}}
			case "pre-target-marker":
				delete(m.Annotations, "meta.helm.sh/release-name")
			case "second-replaced":
				if f.deletes > 0 && name == "second" {
					m.UID = "replacement"
				}
			}
			_ = json.NewEncoder(w).Encode(m)
			return
		}
		f.absenceReads[name]++
		if f.mode == "delete-pending" || f.mode == "delete-pending-then-absent" && f.absenceReads[name] == 1 {
			now, zero := metav1.Now(), int64(0)
			m.DeletionTimestamp, m.DeletionGracePeriodSeconds = &now, &zero
			m.Finalizers = []string{metav1.FinalizerOrphanDependents}
			m.ResourceVersion += "-deleting"
			_ = json.NewEncoder(w).Encode(m)
			return
		}
		if f.mode == "delete-replacement" {
			m.UID, m.ResourceVersion = "replacement", "new-rv"
			_ = json.NewEncoder(w).Encode(m)
			return
		}
		if f.mode == "delete-absence-denied" {
			w.WriteHeader(403)
			_, _ = w.Write([]byte("PRIVATE_FORBIDDEN"))
			return
		}
		if f.mode == "delete-absence-budget" {
			w.Header().Set("Content-Length", strconv.Itoa(maxLiveObjectBytes+1))
			return
		}
		if f.mode == "delete-absence-warning" {
			w.Header().Set("Warning", "299 fixture PRIVATE_WARNING")
		}
		s := metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: metav1.StatusFailure,
			Reason: metav1.StatusReasonNotFound, Code: 404, Details: &metav1.StatusDetails{Name: name, Kind: "configmaps"}}
		if f.mode == "delete-wrong404" {
			s.Details.Name = "other"
		}
		if f.mode == "delete-cause404" {
			s.Details.Causes = []metav1.StatusCause{{Message: "PRIVATE_CAUSE"}}
		}
		if f.mode == "delete-uid404" {
			s.Details.UID = "replacement"
		}
		out, _ := json.Marshal(s)
		if f.mode == "delete-proxy404" {
			out = []byte("PRIVATE_PROXY_404")
		}
		if f.mode == "delete-null404" {
			out = []byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404,"details":null}`)
		}
		if f.mode == "delete-duplicate404" {
			out = append([]byte(`{"code":404,`), out[1:]...)
		}
		if f.mode == "delete-trailing404" {
			out = append(out, []byte(" {}")...)
		}
		w.WriteHeader(404)
		_, _ = w.Write(out)
		if f.mode != "delete-proxy404" {
			f.events = append(f.events, "absent "+name)
		}
	}
}

func checkExecutionPatch(body []byte, before corev1.Secret, kind string) string {
	var ops []struct{ Op, Path, Value string }
	if json.Unmarshal(body, &ops) != nil || len(ops) != 8 {
		return "wrong JSON Patch shape"
	}
	status := "deployed"
	if kind == "finish" {
		status = "uninstalling"
	}
	values := []string{before.Name, before.Namespace, string(before.UID), before.ResourceVersion, status}
	paths := []string{"/metadata/name", "/metadata/namespace", "/metadata/uid", "/metadata/resourceVersion", "/metadata/labels/status"}
	for i := range values {
		if ops[i].Op != "test" || ops[i].Path != paths[i] || ops[i].Value != values[i] {
			return "missing exact storage precondition"
		}
	}
	to := "uninstalling"
	if kind == "finish" {
		to = "uninstalled"
	}
	if ops[5].Op != "replace" || ops[5].Path != "/metadata/labels/status" || ops[5].Value != to || ops[6].Op != "add" || ops[6].Path != "/metadata/labels/modifiedAt" ||
		ops[7].Op != "replace" || ops[7].Path != "/data/release" {
		return "widened storage patch"
	}
	if _, err := strconv.ParseInt(ops[6].Value, 10, 64); err != nil {
		return "invalid storage timestamp"
	}
	return ""
}

func executionPayload(s corev1.Secret) ([]byte, error) {
	compressed, err := base64.StdEncoding.DecodeString(string(s.Data["release"]))
	if err != nil {
		return nil, err
	}
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, helmrelease.MaxReleaseBytes+1))
}

func checkExecutionPreservation(before, after corev1.Secret, kind string) string {
	a, err := executionPayload(before)
	if err != nil {
		return "original fixture payload"
	}
	b, err := executionPayload(after)
	if err != nil {
		return "updated payload invalid"
	}
	defer clear(a)
	defer clear(b)
	var left, right map[string]json.RawMessage
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil || len(left) != len(right) {
		return "payload field loss"
	}
	for key, raw := range left {
		if key != "info" && !bytes.Equal(raw, right[key]) {
			return "unrelated payload bytes changed: " + key
		}
	}
	var li, ri map[string]json.RawMessage
	_ = json.Unmarshal(left["info"], &li)
	_ = json.Unmarshal(right["info"], &ri)
	if kind == "finish" && !bytes.Equal(li["deleted"], ri["deleted"]) {
		return "finish reset original deletion time"
	}
	for key, raw := range li {
		if key != "status" && key != "description" && key != "deleted" && !bytes.Equal(raw, ri[key]) {
			return "unrelated info changed"
		}
	}
	before.ResourceVersion, after.ResourceVersion = "", ""
	before.Labels["status"], before.Labels["modifiedAt"] = after.Labels["status"], after.Labels["modifiedAt"]
	before.Data["release"] = after.Data["release"]
	if !reflect.DeepEqual(before, after) {
		return "unrelated storage changed"
	}
	return ""
}

func typesUID(s string) types.UID { return types.UID(s) }
