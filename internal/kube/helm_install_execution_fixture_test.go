package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

type installExecutionFixture struct {
	mu            sync.Mutex
	objects       map[string][]byte
	dryBodies     [][]byte
	calls, writes atomic.Int32
	bad           atomic.Bool
	mode          string
	faultAt       int
	onWrite       func(int)
	journal       *HelmUninstallJournal
	skipLiveWAL   bool
}

func (f *installExecutionFixture) handler(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	if r.Header.Get("Authorization") != "Bearer current" {
		f.bad.Store(true)
	}
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/prefix")
	prefix := "/api/v1/namespaces/team/"
	if r.Method == http.MethodGet {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch path {
		case "/api/v1":
			d := installHTTPDiscovery()
			d.APIResources[0].Verbs = append(d.APIResources[0].Verbs, "create")
			d.APIResources[1].Verbs = append(d.APIResources[1].Verbs, "create", "patch")
			if f.mode == "no-patch" {
				d.APIResources[1].Verbs = []string{"get", "list", "create"}
			}
			_ = json.NewEncoder(w).Encode(d)
		case "/api/v1/namespaces/team":
			m := metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadata"}, ObjectMeta: metav1.ObjectMeta{Name: "team", UID: "namespace-uid", ResourceVersion: "namespace-rv"}}
			if f.mode == "namespace-changed" {
				m.UID = "new-namespace"
			}
			_ = json.NewEncoder(w).Encode(m)
		case prefix + "secrets":
			list := metav1.PartialObjectMetadataList{TypeMeta: metav1.TypeMeta{Kind: "PartialObjectMetadataList", APIVersion: "meta.k8s.io/v1"}, ListMeta: metav1.ListMeta{ResourceVersion: "list-rv"}}
			for key, raw := range f.objects {
				if strings.HasPrefix(key, "secrets/") {
					var m metav1.PartialObjectMetadata
					_ = json.Unmarshal(raw, &m)
					m.Kind, m.APIVersion = "PartialObjectMetadata", "meta.k8s.io/v1"
					list.Items = append(list.Items, m)
				}
			}
			if f.mode == "history-conflict" {
				list.Items = append(list.Items, metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.example.v2", Namespace: "team", UID: "other", ResourceVersion: "old", Labels: map[string]string{"owner": "helm", "name": "example", "version": "2", "status": "failed"}}})
			}
			_ = json.NewEncoder(w).Encode(list)
		default:
			key := strings.TrimPrefix(path, prefix)
			raw := f.objects[key]
			if len(raw) == 0 {
				parts := strings.Split(key, "/")
				if len(parts) != 2 {
					f.bad.Store(true)
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(404)
				_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Code: 404, Reason: metav1.StatusReasonNotFound, Details: &metav1.StatusDetails{Name: parts[1], Kind: parts[0]}})
				return
			}
			if (f.mode == "observe-loss" || f.mode == "observe-change") && int(f.writes.Load()) == f.faultAt && r.Header.Get("Accept") == "application/json" {
				if f.mode == "observe-loss" {
					w.WriteHeader(403)
					return
				}
				var obj map[string]any
				_ = json.Unmarshal(raw, &obj)
				obj["metadata"].(map[string]any)["resourceVersion"] = "replacement-rv"
				_ = json.NewEncoder(w).Encode(obj)
				return
			}
			if r.Header.Get("Accept") == liveMetadataAccept {
				var m metav1.PartialObjectMetadata
				_ = json.Unmarshal(raw, &m)
				m.Kind = "PartialObjectMetadata"
				m.APIVersion = "meta.k8s.io/v1"
				_ = json.NewEncoder(w).Encode(m)
			} else {
				_, _ = w.Write(raw)
			}
		}
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPatch {
		f.bad.Store(true)
		w.WriteHeader(405)
		return
	}
	q := r.URL.Query()
	dry := q.Get("dryRun") == "All"
	if q.Get("fieldValidation") != "Strict" || q.Get("fieldManager") != helmrelease.InstallFieldManager || len(q) != 2+btoi(dry) {
		f.bad.Store(true)
	}
	if r.Header.Get("Accept-Encoding") != "identity" {
		f.bad.Store(true)
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, helmrelease.MaxSecretBytes+1))
	if err != nil {
		w.WriteHeader(400)
		return
	}
	defer clear(raw)
	resource := strings.TrimPrefix(path, prefix)
	status := http.StatusCreated
	var obj map[string]any
	if r.Method == http.MethodPost {
		if resource != "secrets" && resource != "configmaps" || r.Header.Get("Content-Type") != "application/json" {
			f.bad.Store(true)
		}
		if json.Unmarshal(raw, &obj) != nil {
			f.bad.Store(true)
			w.WriteHeader(400)
			return
		}
	} else {
		if dry || resource != "secrets/sh.helm.release.v1.example.v1" || r.Header.Get("Content-Type") != "application/json-patch+json" {
			f.bad.Store(true)
		}
		status = http.StatusOK
		f.mu.Lock()
		original := bytes.Clone(f.objects[resource])
		f.mu.Unlock()
		defer clear(original)
		patch, e := jsonpatch.DecodePatch(raw)
		if e != nil {
			f.bad.Store(true)
			w.WriteHeader(422)
			return
		}
		changed, e := patch.Apply(original)
		if e != nil {
			f.bad.Store(true)
			w.WriteHeader(422)
			return
		}
		defer clear(changed)
		if json.Unmarshal(changed, &obj) != nil {
			f.bad.Store(true)
			w.WriteHeader(422)
			return
		}
	}
	meta := obj["metadata"].(map[string]any)
	name := meta["name"].(string)
	if dry {
		f.mu.Lock()
		f.dryBodies = append(f.dryBodies, bytes.Clone(raw))
		f.mu.Unlock()
		meta["uid"] = "dry-only-uid"
		meta["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(obj)
		return
	}
	n := int(f.writes.Add(1))
	i := n - 1
	if f.skipLiveWAL {
		// Crash fixtures verify the durable prefix after killing the remote child.
	} else if f.journal == nil {
		f.bad.Store(true)
	} else {
		ids, e := f.journal.IDs()
		if e != nil || len(ids) != 1 {
			f.bad.Store(true)
		} else {
			s, e := f.journal.store.Load(ids[0])
			if e != nil || i >= len(s.Effects) || s.Effects[i].Outcome != helmjournal.Unknown || s.Effects[i].ContentDigest != installRequestDigest(raw) {
				f.bad.Store(true)
			}
		}
	}
	f.mu.Lock()
	if r.Method == http.MethodPost && (i >= len(f.dryBodies) || !bytes.Equal(raw, f.dryBodies[i])) {
		f.bad.Store(true)
	}
	f.mu.Unlock()
	fault := n == f.faultAt
	if fault && (f.mode == "reject" || f.mode == "conflict" || f.mode == "proxy409" || f.mode == "retry-after" || f.mode == "redirect") {
		code, reason := 403, metav1.StatusReasonForbidden
		if f.mode == "conflict" {
			code, reason = 409, metav1.StatusReasonAlreadyExists
			if r.Method == http.MethodPatch {
				reason = metav1.StatusReasonConflict
			}
		}
		if f.mode == "retry-after" {
			code, reason = 429, metav1.StatusReasonTooManyRequests
			w.Header().Set("Retry-After", "0")
		}
		if f.mode == "redirect" {
			w.Header().Set("Location", "/bad-followup")
			w.WriteHeader(307)
			return
		}
		if f.mode == "proxy409" {
			w.WriteHeader(409)
			_, _ = w.Write([]byte("PRIVATE_PROXY_ERROR"))
			return
		}
		w.WriteHeader(code)
		kind := strings.Split(resource, "/")[0]
		_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Reason: reason, Code: int32(code), Details: &metav1.StatusDetails{Name: name, Kind: kind}})
		return
	}
	if r.Method == http.MethodPost {
		meta["uid"] = "persistent-" + strconv.Itoa(n)
		meta["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	meta["resourceVersion"] = "rv-" + strconv.Itoa(n)
	stored, _ := json.Marshal(obj)
	defer clear(stored)
	key := resource
	if r.Method == http.MethodPost {
		key += "/" + name
	}
	f.mu.Lock()
	f.objects[key] = bytes.Clone(stored)
	f.mu.Unlock()
	if f.onWrite != nil {
		f.onWrite(n)
	}
	if fault {
		switch f.mode {
		case "loss":
			h, ok := w.(http.Hijacker)
			if !ok {
				f.bad.Store(true)
				return
			}
			c, _, e := h.Hijack()
			if e == nil {
				_ = c.Close()
			}
			return
		case "malformed":
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"kind":`))
			return
		case "missing-rv":
			delete(meta, "resourceVersion")
		case "wrong-content":
			obj["unexpected"] = "PRIVATE_MUTATION"
		case "managed-null", "managed-array":
			var fields any
			if f.mode == "managed-array" {
				fields = []any{}
			}
			meta["managedFields"] = []any{map[string]any{"manager": helmrelease.InstallFieldManager, "operation": "Update", "apiVersion": "v1", "fieldsType": "FieldsV1", "fieldsV1": fields}}
		case "duplicate-uid":
			meta["uid"] = "persistent-1"
		case "warning":
			w.Header().Set("Warning", "299 proxy PRIVATE_WARNING")
		case "encoding":
			w.Header().Set("Content-Encoding", "gzip")
		case "content-type":
			w.Header().Set("Content-Type", "text/plain")
		case "trailer":
			w.Header().Set("Trailer", "Warning")
		case "truncated":
			w.Header().Set("Content-Length", "999999")
		}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(obj)
	if fault && f.mode == "trailer" {
		w.Header().Set("Warning", "299 proxy PRIVATE_TRAILER")
	}
}
func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
func installExecutionSetup(t *testing.T, n int) (*Backend, *helmrelease.InstallCandidate, *HelmInstallDryRun, *HelmInstallDryRunConsent, *installExecutionFixture) {
	t.Helper()
	f := &installExecutionFixture{objects: map[string][]byte{}, faultAt: 1}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	b, err := New(&rest.Config{Host: srv.URL + "/prefix", BearerToken: "current", RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	stamp := now.Add(-time.Second).UTC().Format(time.RFC3339Nano)
	manifest := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			manifest += "---\n"
		}
		manifest += fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: target-%d\ndata:\n  private: PRIVATE_INSTALL_VALUE\n", i)
	}
	raw, _ := json.Marshal(map[string]any{"name": "example", "namespace": "team", "version": 1, "apply_method": "ssa", "info": map[string]any{"status": "pending-install", "description": "Dry run complete", "first_deployed": stamp, "last_deployed": stamp}, "chart": map[string]any{"metadata": map[string]any{"name": "sample", "version": "0.1.0", "apiVersion": "v2"}}, "manifest": manifest})
	c, err := helmrelease.DecodeInstallCapture(context.Background(), raw, "team", "example", now)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err := b.PrepareHelmInstall(context.Background(), c, "execute-session", true)
	if err != nil {
		t.Fatal(err)
	}
	g, err := b.AcknowledgeHelmInstallDryRun(context.Background(), p, c, "execute-session", true)
	if err != nil {
		t.Fatal(err)
	}
	report, err := b.PreflightHelmInstall(context.Background(), g, c, "execute-session")
	if err != nil {
		t.Fatal(err)
	}
	f.journal, err = OpenHelmUninstallJournal(filepath.Join(t.TempDir(), "journal"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.journal.Close() })
	f.calls.Store(0)
	return b, c, report, g, f
}
