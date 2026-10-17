package kube

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/helmrelease"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

func preparationWire(t *testing.T, unsafe bool) []byte {
	t.Helper()
	d := map[string]any{"name": "example", "namespace": "team", "version": 2, "info": map[string]any{"status": "deployed"},
		"chart":    map[string]any{"metadata": map[string]any{"name": "example-chart", "version": "1.2.3"}, "values": map[string]any{"password": "PRIVATE"}},
		"manifest": liveManifest, "config": map[string]any{"password": "PRIVATE"}}
	if unsafe {
		d["hooks"] = []any{map[string]any{"name": "PRIVATE_HOOK"}}
	}
	payload, _ := json.Marshal(d)
	s := helmfixture.Secret("team", "example", "deployed", 2)
	s.Data["release"] = helmfixture.Stored(payload)
	b, e := json.Marshal(s)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestHelmUninstallPreparationHTTPReadOnly(t *testing.T) {
	for _, mode := range []string{"success", "pagination", "unacknowledged", "invalid-session", "canceled", "nondeployed", "older-before", "newer-after", "rv-after", "uid-after", "unsupported-content", "secret-denied", "missing-object", "proxy404", "denied-object", "full-object", "keep", "hook", "finalizer", "owner", "terminating", "missing-marker", "wrong-marker", "wrong-route", "cluster-route", "wrong-identity", "late-cancel"} {
		t.Run(mode, func(t *testing.T) {
			var lists, content, objects, discovery atomic.Int32
			var bad atomic.Bool
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer current" {
					bad.Store(true)
				}
				p := strings.TrimPrefix(r.URL.Path, "/prefix")
				switch p {
				case "/api/v1/namespaces/team/secrets":
					n := lists.Add(1)
					row := liveRevisionMeta()
					rows := []metav1.PartialObjectMetadata{row}
					if r.Header.Get("Accept") != helmrelease.Accept || r.URL.Query().Get("labelSelector") != "owner=helm" {
						bad.Store(true)
					}
					if mode == "older-before" {
						old := row
						old.UID = "old"
						old.Name = "sh.helm.release.v1.example.v1"
						old.Labels = map[string]string{"owner": "helm", "name": "example", "version": "1", "status": "superseded"}
						rows = append(rows, old)
					}
					if mode == "newer-after" && n == 2 {
						next := row
						next.UID = "new"
						next.Name = "sh.helm.release.v1.example.v3"
						next.Labels = map[string]string{"owner": "helm", "name": "example", "version": "3", "status": "pending-upgrade"}
						rows = append(rows, next)
					}
					if mode == "rv-after" && n == 2 {
						rows[0].ResourceVersion = "changed"
					}
					if mode == "uid-after" && n == 2 {
						rows[0].UID = "changed"
					}
					token := ""
					rv := "collection"
					if mode == "pagination" {
						if n%2 == 1 {
							token = "cursor"
						} else {
							other := row
							other.UID = "other"
							other.Name = "sh.helm.release.v1.other.v2"
							other.Labels = map[string]string{"owner": "helm", "name": "other", "version": "2", "status": "deployed"}
							rows = []metav1.PartialObjectMetadata{other}
							if r.URL.Query().Get("continue") != "cursor" {
								bad.Store(true)
							}
						}
					}
					if mode == "late-cancel" && n == 2 {
						cancel()
					}
					_, _ = w.Write(releasePage(token, rv, rows...))
				case "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v2":
					content.Add(1)
					if r.Header.Get("Accept") != "application/json" {
						bad.Store(true)
					}
					if mode == "secret-denied" {
						w.WriteHeader(http.StatusForbidden)
						_, _ = w.Write([]byte("PRIVATE_ERROR"))
						return
					}
					_, _ = w.Write(preparationWire(t, mode == "unsupported-content"))
				case "/api/v1":
					discovery.Add(1)
					d := liveDiscovery()
					if mode == "wrong-route" {
						d.APIResources[0].Name = "secrets"
					}
					if mode == "cluster-route" {
						d.APIResources[0].Namespaced = false
					}
					_ = json.NewEncoder(w).Encode(d)
				case "/api/v1/namespaces/team/configmaps/sample":
					objects.Add(1)
					if r.Header.Get("Accept") != liveMetadataAccept {
						bad.Store(true)
					}
					if mode == "denied-object" {
						w.WriteHeader(403)
						_, _ = w.Write([]byte("PRIVATE_ERROR"))
						return
					}
					if mode == "missing-object" {
						w.WriteHeader(404)
						_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Reason: metav1.StatusReasonNotFound, Code: 404, Details: &metav1.StatusDetails{Kind: "configmaps", Name: "sample"}})
						return
					}
					if mode == "proxy404" {
						w.WriteHeader(404)
						_, _ = w.Write([]byte("PRIVATE_PROXY"))
						return
					}
					m := liveObject()
					switch mode {
					case "keep":
						m.Annotations["helm.sh/resource-policy"] = "keep"
					case "hook":
						m.Annotations["helm.sh/hook"] = "test"
					case "owner":
						m.OwnerReferences = []metav1.OwnerReference{{Name: "owner"}}
					case "finalizer":
						m.Finalizers = []string{"example/hold"}
					case "terminating":
						v := metav1.Now()
						m.DeletionTimestamp = &v
					case "missing-marker":
						delete(m.Labels, "app.kubernetes.io/managed-by")
					case "wrong-marker":
						m.Annotations["meta.helm.sh/release-name"] = "other"
					case "wrong-identity":
						m.Name = "other"
					case "full-object":
						m.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}
					}
					_ = json.NewEncoder(w).Encode(m)
				default:
					bad.Store(true)
					w.WriteHeader(500)
				}
			}))
			defer srv.Close()
			cfg := &rest.Config{Host: srv.URL + "/prefix", BearerToken: "current", RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()}
			b, e := New(cfg)
			if e != nil {
				t.Fatal(e)
			}
			r := selectedHelmRevision()
			session := "session"
			ack := mode != "unacknowledged"
			if mode == "invalid-session" {
				session = ""
			}
			if mode == "canceled" {
				cancel()
			}
			if mode == "nondeployed" {
				r.Status = "uninstalling"
			}
			p, e := b.PrepareHelmUninstall(ctx, "team", r, session, ack)
			if mode == "success" || mode == "pagination" {
				if e != nil || p == nil || !p.Matches(b, session, "team", r, time.Now()) {
					t.Fatal("preparation", e)
				}
				want := p.Targets()
				if len(want) != 1 || want[0].UID != "live-uid" || want[0].Resource != "configmaps" {
					t.Fatal(want)
				}
				p.Targets()[0].UID = "tampered"
				if !reflect.DeepEqual(want, p.Targets()) {
					t.Fatal("alias")
				}
				other, e := New(cfg)
				if e != nil {
					t.Fatal(e)
				}
				if p.Matches(other, session, "team", r, time.Now()) || p.Matches(b, "other", "team", r, time.Now()) || p.Matches(b, session, "team", r, p.ExpiresAt()) {
					t.Fatal("backend/session/expiry binding")
				}
				payload, _ := json.Marshal(p)
				if strings.Contains(string(payload), "PRIVATE") || string(payload) != "{}" {
					t.Fatal("private internals serialized")
				}
				wantLists := int32(2)
				if mode == "pagination" {
					wantLists = 4
				}
				if lists.Load() != wantLists || content.Load() != 1 || objects.Load() != 1 || discovery.Load() != 1 {
					t.Fatal("unexpected read count", lists.Load(), content.Load(), objects.Load(), discovery.Load())
				}
			} else {
				if e == nil || p != nil {
					t.Fatal("unsafe preparation accepted")
				}
				if strings.Contains(e.Error(), "PRIVATE") {
					t.Fatal("private response exposed")
				}
				if mode == "unacknowledged" || mode == "invalid-session" || mode == "canceled" || mode == "nondeployed" {
					if lists.Load()+content.Load()+objects.Load()+discovery.Load() != 0 {
						t.Fatal("unconsented/invalid work")
					}
				}
				if mode == "older-before" && content.Load() != 0 {
					t.Fatal("content fetched for multiple revisions")
				}
				if mode == "unsupported-content" || mode == "wrong-route" || mode == "cluster-route" {
					if objects.Load() != 0 {
						t.Fatal("unsupported scope caused object access")
					}
				}
				if mode == "unacknowledged" && !errors.Is(e, helmrelease.ErrPreparationConsent) {
					t.Fatal(e)
				}
			}
			if bad.Load() {
				t.Fatal("request method/identity/path/negotiation changed")
			}
		})
	}
}
