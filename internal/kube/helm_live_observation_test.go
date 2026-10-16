package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/helmrelease"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

func livePreview(t *testing.T, text string) *helmrelease.UninstallPreview {
	t.Helper()
	data, _ := json.Marshal(helmfixture.WithManifest("team", "example", "deployed", 2, text))
	p, e := helmrelease.PreviewUninstallSecret(context.Background(), data, "team", selectedHelmRevision(), "session", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	return p
}

const liveManifest = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: sample\n"

func liveRevisionMeta() metav1.PartialObjectMetadata {
	s := helmfixture.Secret("team", "example", "deployed", 2)
	return metav1.PartialObjectMetadata{ObjectMeta: s.ObjectMeta}
}
func liveDiscovery() metav1.APIResourceList {
	return metav1.APIResourceList{TypeMeta: metav1.TypeMeta{Kind: "APIResourceList"}, GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: metav1.Verbs{"get"}}}}
}
func liveObject() metav1.PartialObjectMetadata {
	return metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{Kind: "PartialObjectMetadata", APIVersion: "meta.k8s.io/v1"}, ObjectMeta: metav1.ObjectMeta{Name: "sample", Namespace: "team", UID: types.UID("live-uid"), ResourceVersion: "rv-a", Labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"}, Annotations: map[string]string{"meta.helm.sh/release-name": "example", "meta.helm.sh/release-namespace": "team", "private": "PRIVATE"}}}
}

func TestHelmLiveReadOnlyMetadataAndHistory(t *testing.T) {
	for _, mode := range []string{"present", "versioned-discovery", "missing-marker", "conflict-marker", "absent", "foreign", "cluster", "lifecycle", "replaced-uid", "newer-before", "newer-after", "source-replaced", "source-rv", "history-changed", "reordered", "pagination"} {
		t.Run(mode, func(t *testing.T) {
			text := liveManifest
			if mode == "foreign" {
				text += "  namespace: elsewhere\n"
			}
			p := livePreview(t, text)
			var lists, objects, disc atomic.Int32
			var bad atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer current" {
					bad.Store(true)
				}
				path := strings.TrimPrefix(r.URL.Path, "/prefix")
				switch path {
				case "/api/v1/namespaces/team/secrets":
					n := lists.Add(1)
					if r.Header.Get("Accept") != helmrelease.Accept || r.URL.Query().Get("labelSelector") != "owner=helm" {
						bad.Store(true)
					}
					row := liveRevisionMeta()
					old := row
					old.Name = "sh.helm.release.v1.example.v1"
					old.UID = "old"
					old.Labels = map[string]string{"owner": "helm", "name": "example", "version": "1", "status": "superseded"}
					rows := []metav1.PartialObjectMetadata{row, old}
					if (mode == "newer-before") || (mode == "newer-after" && n == 2) {
						next := row
						next.Name = "sh.helm.release.v1.example.v3"
						next.UID = "new"
						next.Labels = map[string]string{"owner": "helm", "name": "example", "version": "3", "status": "pending-upgrade"}
						rows = append(rows, next)
					}
					if mode == "source-replaced" && n == 2 {
						rows[0].UID = "recreated"
					}
					if mode == "source-rv" && n == 2 {
						rows[0].ResourceVersion = "changed"
					}
					if mode == "history-changed" && n == 2 {
						rows[1].ResourceVersion = "changed"
					}
					if mode == "reordered" && n == 2 {
						rows[0], rows[1] = rows[1], rows[0]
					}
					token := ""
					rv := strconv.Itoa(int(n))
					if mode == "pagination" {
						rv = "snapshot"
						if n%2 == 1 {
							token = "cursor"
							rows = rows[:1]
						} else {
							if r.URL.Query().Get("continue") != "cursor" {
								bad.Store(true)
							}
							rows = rows[1:]
						}
					}
					_, _ = w.Write(releasePage(token, rv, rows...))
				case "/api/v1":
					disc.Add(1)
					d := liveDiscovery()
					if mode == "versioned-discovery" {
						d.APIVersion = "v1"
					}
					if mode == "cluster" {
						d.APIResources[0].Namespaced = false
					}
					_ = json.NewEncoder(w).Encode(d)
				case "/api/v1/namespaces/team/configmaps/sample", "/api/v1/configmaps/sample":
					objects.Add(1)
					if r.Header.Get("Accept") != liveMetadataAccept || r.URL.RawQuery != "" {
						bad.Store(true)
					}
					if mode == "absent" {
						w.WriteHeader(404)
						_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: "Failure", Reason: metav1.StatusReasonNotFound, Code: 404, Details: &metav1.StatusDetails{Name: "sample", Kind: "configmaps"}})
						return
					}
					m := liveObject()
					if mode == "cluster" {
						m.Namespace = ""
					}
					if mode == "missing-marker" {
						delete(m.Annotations, "meta.helm.sh/release-name")
					}
					if mode == "conflict-marker" {
						m.Labels["app.kubernetes.io/managed-by"] = "PRIVATE_OTHER"
					}
					if mode == "replaced-uid" {
						m.UID = "new-live-uid"
					}
					if mode == "lifecycle" {
						now := metav1.Now()
						m.DeletionTimestamp = &now
						m.Finalizers = []string{"private-finalizer"}
						m.Annotations["helm.sh/resource-policy"] = "PRIVATE_UNKNOWN_POLICY"
						m.Annotations["helm.sh/hook"] = "PRIVATE_HOOK"
					}
					_ = json.NewEncoder(w).Encode(m)
				default:
					bad.Store(true)
					http.Error(w, "PRIVATE_PATH", 500)
				}
			}))
			defer srv.Close()
			b, e := New(&rest.Config{Host: srv.URL + "/prefix", BearerToken: "current"})
			if e != nil {
				t.Fatal(e)
			}
			o, e := b.ObserveHelmPreview(context.Background(), p, "session", false)
			if o != nil || !errors.Is(e, helmrelease.ErrLiveConsent) || lists.Load() != 0 {
				t.Fatal("consent", e)
			}
			o, e = b.ObserveHelmPreview(context.Background(), p, "other", true)
			if o != nil || !errors.Is(e, helmrelease.ErrLiveExpired) || lists.Load() != 0 {
				t.Fatal("session", e)
			}
			o, e = b.ObserveHelmPreview(context.Background(), p, "session", true)
			stale := strings.HasPrefix(mode, "newer") || strings.HasPrefix(mode, "source-") || mode == "history-changed"
			if stale {
				if o != nil || !errors.Is(e, helmrelease.ErrLiveStale) {
					t.Fatal("late stale", e)
				}
			} else {
				if e != nil || o == nil || len(o.Items()) != 1 {
					t.Fatal(e)
				}
				item := o.Items()[0]
				expected := "match"
				if mode == "missing-marker" {
					expected = "missing"
				}
				if mode == "conflict-marker" {
					expected = "mismatch"
				}
				if mode == "foreign" {
					if objects.Load() != 0 || disc.Load() != 0 || item.State != "outside-namespace" {
						t.Fatal("foreign GET")
					}
				} else if mode == "absent" {
					if item.State != "absent" || item.UID != "" {
						t.Fatal(item)
					}
				} else if item.State != "present" || item.Markers != expected {
					t.Fatal(item)
				}
				if mode == "lifecycle" && (!item.Keep || !item.Hook || !item.Finalizers || !item.Terminating) {
					t.Fatal("lifecycle lost")
				}
				if mode == "replaced-uid" && item.UID != "new-live-uid" {
					t.Fatal("not current UID")
				}
				projected, _ := json.Marshal(o.Items())
				if strings.Contains(string(projected), "PRIVATE") {
					t.Fatal("privacy")
				}
			}
			if mode == "newer-before" && objects.Load() != 0 {
				t.Fatal("live read after stale precheck")
			}
			if bad.Load() {
				t.Fatal("request boundary")
			}
		})
	}
}

func TestHelmLiveRejectsRoutesAndPrivateFailures(t *testing.T) {
	modes := []string{"denied", "unauthenticated", "unsupported", "throttled", "server", "html404", "wrong404", "full-object", "duplicate-json", "bad-uid", "bad-name", "bad-ns", "large-object", "large-discovery", "invalid-discovery", "missing-route", "duplicate-route", "no-get", "wrong-gv", "invalid-plural", "cluster-with-ns", "subresource-only", "redirect", "late-denied"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			text := liveManifest
			if mode == "cluster-with-ns" {
				text += "  namespace: team\n"
			}
			p := livePreview(t, text)
			var objects, lists, redirects atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirects.Add(1) }))
			defer target.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("write")
				}
				if strings.HasSuffix(r.URL.Path, "/secrets") {
					n := lists.Add(1)
					if mode == "late-denied" && n == 2 {
						http.Error(w, "PRIVATE", 403)
						return
					}
					_, _ = w.Write(releasePage("", "snapshot", liveRevisionMeta()))
					return
				}
				if r.URL.Path == "/api/v1" {
					d := liveDiscovery()
					switch mode {
					case "invalid-discovery":
						_, _ = w.Write([]byte("PRIVATE"))
						return
					case "large-discovery":
						_, _ = w.Write([]byte(strings.Repeat("x", maxLiveDiscoveryBytes+1)))
						return
					case "missing-route":
						d.APIResources[0].Kind = "Pod"
					case "duplicate-route":
						d.APIResources = append(d.APIResources, d.APIResources[0])
					case "no-get":
						d.APIResources[0].Verbs = metav1.Verbs{"list"}
					case "wrong-gv":
						d.GroupVersion = "other/v1"
					case "invalid-plural":
						d.APIResources[0].Name = ".."
					case "cluster-with-ns":
						d.APIResources[0].Namespaced = false
					case "subresource-only":
						d.APIResources[0].Name = "configmaps/status"
					}
					_ = json.NewEncoder(w).Encode(d)
					return
				}
				objects.Add(1)
				codes := map[string]int{"denied": 403, "unauthenticated": 401, "unsupported": 406, "throttled": 429, "server": 500, "html404": 404}
				if code := codes[mode]; code != 0 {
					w.Header().Set("Retry-After", "0")
					http.Error(w, "PRIVATE", code)
					return
				}
				switch mode {
				case "redirect":
					http.Redirect(w, r, target.URL, 302)
					return
				case "large-object":
					_, _ = w.Write([]byte(strings.Repeat("x", maxLiveObjectBytes+1)))
					return
				case "full-object":
					_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{},"data":{"token":"PRIVATE"}}`))
					return
				case "duplicate-json":
					_, _ = w.Write([]byte(`{"kind":"PartialObjectMetadata","kind":"PRIVATE"}`))
					return
				case "wrong404":
					w.WriteHeader(404)
					_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: "Failure", Reason: metav1.StatusReasonNotFound, Code: 404, Details: &metav1.StatusDetails{Name: "other", Kind: "configmaps"}})
					return
				}
				m := liveObject()
				if mode == "bad-uid" {
					m.UID = ""
				}
				if mode == "bad-name" {
					m.Name = "other"
				}
				if mode == "bad-ns" {
					m.Namespace = "other"
				}
				_ = json.NewEncoder(w).Encode(m)
			}))
			defer srv.Close()
			b, _ := New(&rest.Config{Host: srv.URL})
			o, e := b.ObserveHelmPreview(context.Background(), p, "session", true)
			if o != nil || e == nil || strings.Contains(e.Error(), "PRIVATE") || objects.Load() > 1 || redirects.Load() != 0 {
				t.Fatal("failure/partial/retry", e)
			}
		})
	}
}

func TestHelmLiveCancellationAndBudgets(t *testing.T) {
	for _, stage := range []string{"before", "discovery", "object", "after"} {
		t.Run(stage, func(t *testing.T) {
			started, stopped := make(chan struct{}), make(chan struct{})
			var lists atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				current := "object"
				if strings.HasSuffix(r.URL.Path, "/secrets") {
					if lists.Add(1) == 1 {
						current = "before"
					} else {
						current = "after"
					}
				} else if r.URL.Path == "/api/v1" {
					current = "discovery"
				}
				if stage == current {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					close(started)
					<-r.Context().Done()
					close(stopped)
					return
				}
				switch current {
				case "before", "after":
					_, _ = w.Write(releasePage("", "snapshot", liveRevisionMeta()))
				case "discovery":
					_ = json.NewEncoder(w).Encode(liveDiscovery())
				default:
					_ = json.NewEncoder(w).Encode(liveObject())
				}
			}))
			defer srv.Close()
			b, _ := New(&rest.Config{Host: srv.URL})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			p := livePreview(t, liveManifest)
			go func() {
				o, e := b.ObserveHelmPreview(ctx, p, "session", true)
				if o != nil {
					t.Error("cancel retained result")
				}
				done <- e
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("not started")
			}
			cancel()
			select {
			case e := <-done:
				if !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("not cancelled")
			}
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("request survived")
			}
		})
	}
	// Group preflight must reject before *any* HTTP traffic.
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer srv.Close()
	b, _ := New(&rest.Config{Host: srv.URL})
	text := ""
	for i := 0; i < maxLiveGroups+1; i++ {
		text += fmt.Sprintf("---\napiVersion: group%d.example/v1\nkind: Widget\nmetadata:\n  name: test\n", i)
	}
	if o, e := b.ObserveHelmPreview(context.Background(), livePreview(t, text), "session", true); o != nil || !errors.Is(e, helmrelease.ErrLiveBudget) || calls.Load() != 0 {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if o, e := b.ObserveHelmPreview(ctx, livePreview(t, liveManifest), "session", true); o != nil || !errors.Is(e, context.Canceled) || calls.Load() != 0 {
		t.Fatal(e)
	}
	// Exercise the shared bytes, not only individual response limits.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", 40))) }))
	defer srv2.Close()
	b, _ = New(&rest.Config{Host: srv2.URL, RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()})
	remaining := 64
	if _, _, e := b.helmLiveGET(context.Background(), []string{"/api/v1"}, "application/json", 64, &remaining, false); e != nil {
		t.Fatal(e)
	}
	if _, _, e := b.helmLiveGET(context.Background(), []string{"/api/v1"}, "application/json", 64, &remaining, false); !errors.Is(e, helmrelease.ErrLiveBudget) {
		t.Fatal("shared bytes", e)
	}
}

// Exercise the legacy wire envelope, not only a locally constructed versioned
// TypeMeta. Kubernetes' api/discovery/api__v1.json intentionally lacks apiVersion.
func TestHelmLiveDiscoveryEnvelope(t *testing.T) {
	const core = `{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"configmaps","singularName":"configmap","namespaced":true,"kind":"ConfigMap","verbs":["get"],"storageVersionHash":"opaque"}]}`
	cases := []struct {
		name, body, gv, kind string
		valid                bool
	}{
		{"legacy-core", core, "v1", "ConfigMap", true},
		{"explicit-v1", strings.Replace(core, "{", `{"apiVersion":"v1",`, 1), "v1", "ConfigMap", true},
		{"legacy-group", strings.ReplaceAll(strings.ReplaceAll(core, `"v1"`, `"apps/v1"`), "ConfigMap", "Deployment"), "apps/v1", "Deployment", true},
		{"empty-version", strings.Replace(core, "{", `{"apiVersion":"",`, 1), "v1", "ConfigMap", false},
		{"null-version", strings.Replace(core, "{", `{"apiVersion":null,`, 1), "v1", "ConfigMap", false},
		{"wrong-version", strings.Replace(core, "{", `{"apiVersion":"apps/v1",`, 1), "v1", "ConfigMap", false},
		{"numeric-version", strings.Replace(core, "{", `{"apiVersion":1,`, 1), "v1", "ConfigMap", false},
		{"duplicate-version", strings.Replace(core, "{", `{"apiVersion":"v1","apiVersion":"v1",`, 1), "v1", "ConfigMap", false},
		{"duplicate-gv", strings.Replace(core, "{", `{"groupVersion":"v1",`, 1), "v1", "ConfigMap", false},
		{"wrong-gv", core, "apps/v1", "ConfigMap", false},
		{"missing-gv", strings.Replace(core, `"groupVersion":"v1",`, "", 1), "v1", "ConfigMap", false},
		{"missing-kind", strings.Replace(core, `"kind":"APIResourceList",`, "", 1), "v1", "ConfigMap", false},
		{"wrong-kind", strings.Replace(core, "APIResourceList", "APIGroupDiscoveryList", 1), "v1", "ConfigMap", false},
		{"unknown-field", strings.Replace(core, "{", `{"PRIVATE":"SECRET",`, 1), "v1", "ConfigMap", false},
		{"null-resources", `{"kind":"APIResourceList","groupVersion":"v1","resources":null}`, "v1", "ConfigMap", false},
		{"trailing-json", core + "{}", "v1", "ConfigMap", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			list, err := decodeLiveDiscovery([]byte(tc.body), tc.gv)
			if !tc.valid {
				if !errors.Is(err, helmrelease.ErrLiveRouting) || list.APIResources != nil || strings.Contains(err.Error(), "SECRET") {
					t.Fatal("invalid discovery accepted or leaked", err)
				}
				return
			}
			if err != nil || list.GroupVersion != tc.gv || len(list.APIResources) != 1 {
				t.Fatal("valid legacy discovery rejected", err)
			}
			route, err := resolveLiveRoute(list, tc.kind)
			if err != nil || route.resource != "configmaps" || !route.namespaced {
				t.Fatal("exact discovered route changed", err)
			}
		})
	}
}
