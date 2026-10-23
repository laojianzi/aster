package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

func installHTTPCandidate(t *testing.T, now time.Time) *helmrelease.InstallCandidate {
	t.Helper()
	v := map[string]any{"name": "example", "namespace": "team", "version": 1, "apply_method": "ssa",
		"info":     map[string]any{"status": "pending-install", "description": "Dry run complete", "first_deployed": now.Add(-time.Second).UTC().Format(time.RFC3339Nano), "last_deployed": now.Add(-time.Second).UTC().Format(time.RFC3339Nano)},
		"chart":    map[string]any{"metadata": map[string]any{"apiVersion": "v2", "name": "sample", "version": "0.1.0"}},
		"manifest": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\n  namespace: team\ndata:\n  message: PRIVATE_HTTP_VALUE\n"}
	raw, _ := json.Marshal(v)
	c, err := helmrelease.DecodeInstallCapture(context.Background(), raw, "team", "example", now)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func installHTTPDiscovery() metav1.APIResourceList {
	return metav1.APIResourceList{TypeMeta: metav1.TypeMeta{Kind: "APIResourceList", APIVersion: "v1"}, GroupVersion: "v1", APIResources: []metav1.APIResource{
		{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: []string{"get"}},
		{Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: []string{"get", "list"}},
		{Name: "namespaces", Kind: "Namespace", Namespaced: false, Verbs: []string{"get"}},
	}}
}
func TestHelmInstallPreparationHTTPReadOnly(t *testing.T) {
	for _, mode := range []string{"success", "pagination", "unacknowledged", "invalid-session", "cancelled", "expired", "nil-context",
		"missing-namespace", "namespace-replaced", "namespace-rv-changed", "namespace-deleting", "namespace-wrong-name",
		"history-conflict", "history-conflict-after", "history-denied", "history-partial", "history-changed", "unlabeled-secret",
		"reserved-secret-after", "present-target", "target-denied", "redirect", "throttled", "proxy404", "wrong-404-name",
		"wrong-404-kind", "wrong-404-code", "wrong-discovery", "ambiguous-discovery", "late-cancel"} {
		t.Run(mode, func(t *testing.T) {
			var requests, lists, nss, secrets atomic.Int32
			var bad atomic.Bool
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer current" {
					bad.Store(true)
				}
				p := strings.TrimPrefix(r.URL.Path, "/prefix")
				switch p {
				case "/api/v1":
					d := installHTTPDiscovery()
					if mode == "wrong-discovery" {
						d.APIResources[0].Namespaced = false
					}
					if mode == "ambiguous-discovery" {
						d.APIResources = append(d.APIResources, d.APIResources[0])
					}
					_ = json.NewEncoder(w).Encode(d)
				case "/api/v1/namespaces/team":
					n := nss.Add(1)
					if r.Header.Get("Accept") != liveMetadataAccept {
						bad.Store(true)
					}
					if mode == "missing-namespace" {
						w.WriteHeader(404)
						return
					}
					m := metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{Kind: "PartialObjectMetadata", APIVersion: "meta.k8s.io/v1"},
						ObjectMeta: metav1.ObjectMeta{Name: "team", UID: "namespace-uid", ResourceVersion: "namespace-rv"}}
					if mode == "namespace-replaced" && n == 2 {
						m.UID = "replacement"
					}
					if mode == "namespace-rv-changed" && n == 2 {
						m.ResourceVersion = "changed"
					}
					if mode == "namespace-deleting" {
						v := metav1.Now()
						m.DeletionTimestamp = &v
					}
					if mode == "namespace-wrong-name" {
						m.Name = "other"
					}
					if mode == "late-cancel" && n == 2 {
						cancel()
					}
					_ = json.NewEncoder(w).Encode(m)
				case "/api/v1/namespaces/team/secrets":
					n := lists.Add(1)
					if r.Header.Get("Accept") != helmrelease.Accept || r.URL.Query().Get("labelSelector") != "owner=helm" {
						bad.Store(true)
					}
					if mode == "history-denied" {
						w.WriteHeader(403)
						return
					}
					if mode == "history-partial" {
						_, _ = w.Write([]byte(`{"kind":"PartialObjectMetadataList"}`))
						return
					}
					rows := []metav1.PartialObjectMetadata{}
					if mode == "history-conflict" || mode == "history-conflict-after" && n == 2 {
						rows = append(rows, metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.example.v4", Namespace: "team", UID: "old", ResourceVersion: "old-rv",
							Labels: map[string]string{"owner": "helm", "name": "example", "version": "4", "status": "uninstalled"}}})
					}
					if mode == "history-changed" && n == 2 {
						rows = append(rows, metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.other.v1", Namespace: "team", UID: "other", ResourceVersion: "rv",
							Labels: map[string]string{"owner": "helm", "name": "other", "version": "1", "status": "deployed"}}})
					}
					token := ""
					if mode == "pagination" && n%2 == 1 {
						token = "next"
					} else if mode == "pagination" && r.URL.Query().Get("continue") != "next" {
						bad.Store(true)
					}
					_, _ = w.Write(releasePage(token, "collection", rows...))
				case "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v1", "/api/v1/namespaces/team/configmaps/example":
					resource, name := "configmaps", "example"
					isSecret := strings.Contains(p, "/secrets/")
					n := int32(0)
					if isSecret {
						resource, name = "secrets", "sh.helm.release.v1.example.v1"
						n = secrets.Add(1)
					}
					if r.Header.Get("Accept") != liveMetadataAccept {
						bad.Store(true)
					}
					if mode == "unlabeled-secret" && isSecret || mode == "reserved-secret-after" && isSecret && n == 2 || mode == "present-target" && !isSecret {
						m := metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{Kind: "PartialObjectMetadata", APIVersion: "meta.k8s.io/v1"},
							ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team", UID: "existing", ResourceVersion: "rv"}}
						_ = json.NewEncoder(w).Encode(m)
						return
					}
					if !isSecret {
						switch mode {
						case "target-denied":
							w.WriteHeader(403)
							return
						case "redirect":
							w.Header().Set("Location", "/private")
							w.WriteHeader(302)
							return
						case "throttled":
							w.Header().Set("Retry-After", "1")
							w.WriteHeader(429)
							return
						case "proxy404":
							w.WriteHeader(404)
							_, _ = w.Write([]byte("PRIVATE_PROXY"))
							return
						}
					}
					s := metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: "Failure", Code: 404, Reason: metav1.StatusReasonNotFound,
						Details: &metav1.StatusDetails{Name: name, Kind: resource}}
					if !isSecret {
						switch mode {
						case "wrong-404-name":
							s.Details.Name = "other"
						case "wrong-404-kind":
							s.Details.Kind = "Secret"
						case "wrong-404-code":
							s.Code = 500
						}
					}
					w.WriteHeader(404)
					_ = json.NewEncoder(w).Encode(s)
				default:
					bad.Store(true)
					w.WriteHeader(500)
				}
			}))
			defer srv.Close()
			cfg := &rest.Config{Host: srv.URL + "/prefix", BearerToken: "current", RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()}
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			if mode == "expired" {
				now = now.Add(-3 * time.Minute)
			}
			c := installHTTPCandidate(t, now)
			session := "current-session"
			if mode == "invalid-session" {
				session = ""
			}
			if mode == "cancelled" {
				cancel()
			}
			if mode == "nil-context" {
				ctx = nil
			}
			p, err := b.PrepareHelmInstall(ctx, c, session, mode != "unacknowledged")
			good := mode == "success" || mode == "pagination"
			if good {
				if err != nil || p == nil || !p.Matches(b, c, session, time.Now()) {
					t.Fatal("preparation rejected", err)
				}
				other, _ := New(cfg)
				if p.Matches(other, c, session, time.Now()) || p.Matches(b, c, "other", time.Now()) ||
					p.Matches(b, installHTTPCandidate(t, time.Now()), session, time.Now()) || p.Matches(b, c, session, p.ExpiresAt()) {
					t.Fatal("binding bypass")
				}
				uid, rv := p.NamespaceIdentity()
				if uid != "namespace-uid" || rv != "namespace-rv" {
					t.Fatal("wrong namespace identity")
				}
				if strings.Contains(fmt.Sprintf("%#v", p), "PRIVATE") {
					t.Fatal("private binding leaked")
				}
				want := int32(8)
				if mode == "pagination" {
					want += 2
				}
				if requests.Load() != want {
					t.Fatalf("unexpected GET count %d", requests.Load())
				}
			} else if err == nil || p != nil {
				t.Fatal("unsafe preparation accepted")
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("private diagnostics")
			}
			if bad.Load() {
				t.Fatal("write, wrong identity/header, unexpected route or followed redirect")
			}
			if mode == "unacknowledged" || mode == "invalid-session" || mode == "cancelled" || mode == "expired" || mode == "nil-context" {
				if requests.Load() != 0 {
					t.Fatal("transport used before admission")
				}
			}
		})
	}
}
