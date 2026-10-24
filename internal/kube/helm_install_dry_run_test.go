package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

type installDryFixture struct {
	mode         string
	calls, posts atomic.Int32
	bad          atomic.Bool
	onPost       func(int)
}

func (f *installDryFixture) handler(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer current" {
		f.bad.Store(true)
	}
	path := strings.TrimPrefix(r.URL.Path, "/prefix")
	if r.Method == http.MethodPost {
		n := int(f.posts.Add(1))
		if f.onPost != nil {
			f.onPost(n)
		}
		q := r.URL.Query()
		if (path != "/api/v1/namespaces/team/secrets" && path != "/api/v1/namespaces/team/configmaps") ||
			q.Get("dryRun") != "All" || q.Get("fieldValidation") != "Strict" || q.Get("fieldManager") != helmrelease.InstallFieldManager || len(q) != 3 || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept-Encoding") != "identity" {
			f.bad.Store(true)
		}
		if n == 1 && !strings.HasSuffix(path, "/secrets") || n == 2 && !strings.HasSuffix(path, "/configmaps") {
			f.bad.Store(true)
		}
		var v map[string]any
		raw, e := io.ReadAll(io.LimitReader(r.Body, helmrelease.MaxSecretBytes+1))
		if e != nil || json.Unmarshal(raw, &v) != nil {
			f.bad.Store(true)
			w.WriteHeader(400)
			return
		}
		clear(raw)
		m := v["metadata"].(map[string]any)
		m["uid"] = "temporary-uid"
		m["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
		switch f.mode {
		case "deny-secret":
			w.WriteHeader(403)
			_, _ = w.Write([]byte("PRIVATE_SERVER_ERROR"))
			return
		case "deny-target":
			if n == 2 {
				w.WriteHeader(403)
				return
			}
		case "conflict":
			w.WriteHeader(409)
			return
		case "retry-after":
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		case "redirect":
			w.Header().Set("Location", "/unexpected")
			w.WriteHeader(307)
			return
		case "warning":
			w.Header().Set("Warning", "299 example PRIVATE_SERVER_WARNING")
		case "compressed":
			w.Header().Set("Content-Encoding", "gzip")
		case "wrong-type":
			w.Header().Set("Content-Type", "text/plain")
		case "wrong-status":
			w.WriteHeader(200)
			_ = json.NewEncoder(w).Encode(v)
			return
		case "bad-payload":
			if n == 2 {
				v["data"] = map[string]any{"message": "PRIVATE_ALTERED"}
			}
		case "wrong-identity":
			m["name"] = "other"
		case "malformed":
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"kind":`))
			return
		case "oversized":
			w.Header().Set("Content-Length", fmt.Sprint(helmrelease.MaxSecretBytes+1))
			w.WriteHeader(201)
			return
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(v)
		return
	}
	if r.Method != "GET" {
		f.bad.Store(true)
		w.WriteHeader(405)
		return
	}
	switch path {
	case "/api/v1":
		d := installHTTPDiscovery()
		d.APIResources[0].Verbs = append(d.APIResources[0].Verbs, "create")
		d.APIResources[1].Verbs = append(d.APIResources[1].Verbs, "create")
		if f.mode == "missing-create" {
			d.APIResources[0].Verbs = []string{"get"}
		}
		_ = json.NewEncoder(w).Encode(d)
	case "/api/v1/namespaces/team":
		m := metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{Kind: "PartialObjectMetadata", APIVersion: "meta.k8s.io/v1"}, ObjectMeta: metav1.ObjectMeta{Name: "team", UID: "namespace-uid", ResourceVersion: "namespace-rv"}}
		if f.mode == "pre-namespace" || f.mode == "post-namespace" && f.posts.Load() == 2 {
			m.UID = "replacement"
		}
		_ = json.NewEncoder(w).Encode(m)
	case "/api/v1/namespaces/team/secrets":
		list := metav1.PartialObjectMetadataList{TypeMeta: metav1.TypeMeta{Kind: "PartialObjectMetadataList", APIVersion: "meta.k8s.io/v1"}, ListMeta: metav1.ListMeta{ResourceVersion: "snapshot"}}
		if f.mode == "post-history" && f.posts.Load() == 2 {
			list.Items = []metav1.PartialObjectMetadata{{ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.example.v2", Namespace: "team", UID: "other", ResourceVersion: "other-rv", Labels: map[string]string{"owner": "helm", "name": "example", "version": "2", "status": "failed"}}}}
		}
		_ = json.NewEncoder(w).Encode(list)
	case "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v1", "/api/v1/namespaces/team/configmaps/example":
		parts := strings.Split(path, "/")
		resource, name := parts[len(parts)-2], parts[len(parts)-1]
		if f.mode == "post-target" && resource == "configmaps" && f.posts.Load() == 2 {
			_ = json.NewEncoder(w).Encode(metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{Kind: "PartialObjectMetadata", APIVersion: "meta.k8s.io/v1"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team", UID: "competitor", ResourceVersion: "competitor-rv"}})
			return
		}
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: "Failure", Code: 404, Reason: metav1.StatusReasonNotFound, Details: &metav1.StatusDetails{Name: name, Kind: resource}})
	default:
		f.bad.Store(true)
		w.WriteHeader(500)
	}
}
func installDrySetup(t *testing.T) (*Backend, *helmrelease.InstallCandidate, *HelmInstallPreparation, *installDryFixture) {
	t.Helper()
	f := &installDryFixture{}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	b, err := New(&rest.Config{Host: srv.URL + "/prefix", BearerToken: "current", RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()})
	if err != nil {
		t.Fatal(err)
	}
	c := installHTTPCandidate(t, time.Now())
	p, err := b.PrepareHelmInstall(context.Background(), c, "dry-session", true)
	if err != nil {
		t.Fatal(err)
	}
	f.calls.Store(0)
	return b, c, p, f
}
func TestHelmInstallDryRunHTTP(t *testing.T) {
	modes := []string{"success", "missing-create", "pre-namespace", "post-namespace", "post-target", "post-history", "deny-secret", "deny-target", "conflict", "retry-after", "redirect", "warning", "compressed", "wrong-type", "wrong-status", "bad-payload", "wrong-identity", "malformed", "oversized", "cancelled", "nil-context", "wrong-session", "wrong-backend", "candidate-copy", "candidate-replaced", "preparation-replaced", "revoked", "cancel-first", "revoke-first"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			b, c, p, f := installDrySetup(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			grant, err := b.AcknowledgeHelmInstallDryRun(ctx, p, c, "dry-session", true)
			if err != nil {
				t.Fatal(err)
			}
			originalID := p.ID()
			if f.calls.Load() != 0 {
				t.Fatal("acknowledgement performed network")
			}
			f.mode = mode
			session := "dry-session"
			caller := b
			switch mode {
			case "cancelled":
				cancel()
			case "nil-context":
				ctx = nil
			case "wrong-session":
				session = "other"
			case "wrong-backend":
				caller, _ = New(b.Config())
			case "candidate-copy":
				copy := *c
				c = &copy
			case "candidate-replaced":
				*c = *installHTTPCandidate(t, time.Now())
			case "preparation-replaced":
				p.id = "changed"
				p.expires = time.Now().Add(time.Hour)
				p.session = "changed"
			case "revoked":
				grant.Discard()
			case "cancel-first":
				f.onPost = func(n int) {
					if n == 1 {
						cancel()
					}
				}
			case "revoke-first":
				f.onPost = func(n int) {
					if n == 1 {
						grant.Discard()
					}
				}
			}
			r, err := caller.PreflightHelmInstall(ctx, grant, c, session)
			good := mode == "success" || mode == "preparation-replaced"
			if good {
				if err != nil || r == nil || !r.Complete() || !r.Matches(b, c, "dry-session", time.Now()) || r.PreparationID() != originalID {
					t.Fatal("valid preflight", err)
				}
				if f.posts.Load() != 2 || len(r.Steps()) != 2 || !r.Steps()[0].Verified || !r.Steps()[1].Verified {
					t.Fatal("missing exact steps")
				}
				steps := r.Steps()
				steps[0].Verified = false
				if !r.Steps()[0].Verified {
					t.Fatal("mutable report")
				}
				if r.Matches(b, c, "other", time.Now()) || r.Matches(b, c, "dry-session", r.ExpiresAt()) {
					t.Fatal("report binding")
				}
				grant.Discard()
				if !r.Complete() || r.Matches(b, c, "dry-session", time.Now()) {
					t.Fatal("revocation erased facts or left authority")
				}
			} else if err == nil || r != nil && r.Complete() {
				t.Fatal("unsafe preflight accepted")
			}
			switch mode {
			case "deny-target", "bad-payload":
				if r == nil || len(r.Steps()) != 2 || !r.Steps()[0].Verified || r.Steps()[1].Verified {
					t.Fatal("lost partial verification")
				}
			case "post-namespace", "post-target", "post-history":
				if r == nil || len(r.Steps()) != 2 || !r.Steps()[1].Verified {
					t.Fatal("postflight failure erased verified responses")
				}
			case "cancelled", "nil-context", "wrong-session", "wrong-backend", "candidate-copy", "candidate-replaced", "revoked":
				if f.calls.Load() != 0 {
					t.Fatal("bad consent made request")
				}
			case "missing-create", "pre-namespace":
				if f.posts.Load() != 0 {
					t.Fatal("failed preflight posted")
				}
			case "retry-after", "redirect", "deny-secret", "conflict":
				if f.posts.Load() != 1 {
					t.Fatal("automatic retry")
				}
			}
			before := f.calls.Load()
			if _, err = b.PreflightHelmInstall(context.Background(), grant, c, "dry-session"); !errors.Is(err, ErrHelmDryRunConsent) || f.calls.Load() != before {
				t.Fatal("consent reused", err)
			}
			if f.bad.Load() {
				t.Fatal("wrong route, identity or request policy")
			}
			for _, value := range []any{grant, r} {
				j, _ := json.Marshal(value)
				if strings.Contains(fmt.Sprintf("%#v", value), "PRIVATE") || strings.Contains(string(j), "PRIVATE") {
					t.Fatal("serialization leak")
				}
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("diagnostic leak")
			}
		})
	}
}
func TestHelmInstallDryRunAcknowledgement(t *testing.T) {
	for _, mode := range []string{"unacknowledged", "nil-context", "nil-preparation", "nil-candidate", "wrong-session", "expired", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			b, c, p, f := installDrySetup(t)
			ctx := context.Background()
			session := "dry-session"
			switch mode {
			case "nil-context":
				ctx = nil
			case "nil-preparation":
				p = nil
			case "nil-candidate":
				c = nil
			case "wrong-session":
				session = "other"
			case "expired":
				p.expires = time.Now().Add(-time.Second)
			case "cancelled":
				var stop context.CancelFunc
				ctx, stop = context.WithCancel(ctx)
				stop()
			}
			grant, err := b.AcknowledgeHelmInstallDryRun(ctx, p, c, session, mode != "unacknowledged")
			if err == nil || grant != nil || f.calls.Load() != 0 {
				t.Fatal("invalid acknowledgement")
			}
		})
	}
}
func TestHelmInstallDryRunConcurrentCopies(t *testing.T) {
	b, c, p, f := installDrySetup(t)
	grant, err := b.AcknowledgeHelmInstallDryRun(context.Background(), p, c, "dry-session", true)
	if err != nil {
		t.Fatal(err)
	}
	var ok, denied atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		copy := *grant
		go func() {
			defer wg.Done()
			r, e := b.PreflightHelmInstall(context.Background(), &copy, c, "dry-session")
			if e == nil && r.Complete() {
				ok.Add(1)
			} else if errors.Is(e, ErrHelmDryRunConsent) {
				denied.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || denied.Load() != 11 || f.posts.Load() != 2 || f.bad.Load() {
		t.Fatal("copies duplicated or lost operation", ok.Load(), denied.Load())
	}
}

func TestHelmInstallDryRunRejectsReplacedPreparationCandidate(t *testing.T) {
	b, c, p, f := installDrySetup(t)
	*c = *installHTTPCandidate(t, time.Now())
	if p.Matches(b, c, "dry-session", time.Now()) {
		t.Fatal("preparation retargeted by overwriting original candidate wrapper")
	}
	if grant, err := b.AcknowledgeHelmInstallDryRun(context.Background(), p, c, "dry-session", true); err == nil || grant != nil || f.calls.Load() != 0 {
		t.Fatal("replacement accepted before freeze")
	}
}

type installDryRoundTrip func(*http.Request) (*http.Response, error)

func (f installDryRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type installUnreadBody struct{ reads atomic.Int32 }

func (b *installUnreadBody) Read([]byte) (int, error) {
	b.reads.Add(1)
	return 0, errors.New("PRIVATE_ERROR_BODY")
}
func (b *installUnreadBody) Close() error { return nil }
func TestHelmInstallDryRunTransportNoReplayOrErrorBody(t *testing.T) {
	for _, status := range []int{301, 307, 401, 403, 409, 422, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			b, _, _, _ := installDrySetup(t)
			body := &installUnreadBody{}
			calls := 0
			b.metricsHTTP = &http.Client{Transport: installDryRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "POST" || r.GetBody != nil || r.Header.Get("Idempotency-Key") != "" || r.Header.Get("X-Idempotency-Key") != "" {
					t.Fatal("replay surface")
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"0"}}, Body: body, Request: r}, nil
			})}
			remaining := 1000
			raw, attempted, err := b.helmInstallDryRunPOST(context.Background(), "team", "secrets", []byte(`{}`), &remaining)
			if err == nil || raw != nil || !attempted || calls != 1 || body.reads.Load() != 0 || remaining != 1000 || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("error consumed, leaked or replayed")
			}
		})
	}
}
func TestHelmInstallDryRunTransportBudgetAndRoutes(t *testing.T) {
	for _, mode := range []string{"nil-context", "cancelled", "zero-budget", "empty-body", "oversized-body", "wrong-resource", "wrong-namespace", "oversized-stream", "trailer-warning", "short-body", "duplicate-type", "duplicate-encoding", "uncompressed"} {
		t.Run(mode, func(t *testing.T) {
			b, _, _, _ := installDrySetup(t)
			calls := 0
			b.metricsHTTP = &http.Client{Transport: installDryRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				h := http.Header{"Content-Type": []string{"application/json"}}
				text := "{}"
				length := int64(-1)
				trailer := http.Header{}
				uncompressed := false
				switch mode {
				case "oversized-stream":
					text = strings.Repeat(" ", 1025)
				case "trailer-warning":
					trailer.Set("Warning", "299 private")
				case "short-body":
					length = 10
				case "duplicate-type":
					h["Content-Type"] = []string{"application/json", "application/json"}
				case "duplicate-encoding":
					h["Content-Encoding"] = []string{"identity", "gzip"}
				case "uncompressed":
					uncompressed = true
				}
				return &http.Response{StatusCode: 201, Header: h, Body: io.NopCloser(strings.NewReader(text)), ContentLength: length, Trailer: trailer, Request: r, Uncompressed: uncompressed}, nil
			})}
			ctx := context.Background()
			body := []byte(`{}`)
			remaining := 1024
			ns, resource := "team", "secrets"
			switch mode {
			case "nil-context":
				ctx = nil
			case "cancelled":
				var stop context.CancelFunc
				ctx, stop = context.WithCancel(ctx)
				stop()
			case "zero-budget":
				remaining = 0
			case "empty-body":
				body = nil
			case "oversized-body":
				body = make([]byte, helmrelease.MaxSecretBytes+1)
			case "wrong-resource":
				resource = "pods"
			case "wrong-namespace":
				ns = "../team"
			}
			raw, attempted, err := b.helmInstallDryRunPOST(ctx, ns, resource, body, &remaining)
			if err == nil || raw != nil {
				t.Fatal("bad request or response admitted")
			}
			pre := mode == "nil-context" || mode == "cancelled" || mode == "zero-budget" || mode == "empty-body" || mode == "oversized-body" || mode == "wrong-resource" || mode == "wrong-namespace"
			if pre && (calls != 0 || attempted) || !pre && (calls != 1 || !attempted) {
				t.Fatal("dispatch accounting")
			}
		})
	}
}
