package kube

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/helmrelease"
	"k8s.io/client-go/rest"
)

func selectedHelmRevision() helmrelease.Revision {
	return helmrelease.Revision{Release: "example", Number: 2, Status: "deployed", StorageName: "sh.helm.release.v1.example.v2", UID: "uid2", ResourceVersion: "7"}
}
func TestHelmInspectionExactGetConsentAndCurrentIdentity(t *testing.T) {
	var calls, bad atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/prefix/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v2" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer current" || r.Header.Get("Accept") != "application/json" {
			bad.Add(1)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(helmfixture.Secret("team", "example", "deployed", 2))
	}))
	defer server.Close()
	b, err := New(&rest.Config{Host: server.URL + "/prefix", BearerToken: "current"})
	if err != nil {
		t.Fatal(err)
	}
	row := selectedHelmRevision()
	if _, err := b.InspectHelmRelease(context.Background(), "team", row, false); !errors.Is(err, helmrelease.ErrInspectionConsent) {
		t.Fatal(err)
	}
	if _, err := b.InspectHelmRelease(context.Background(), "../team", row, true); !errors.Is(err, helmrelease.ErrInspectionSelection) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.InspectHelmRelease(ctx, "team", row, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("unconsented/invalid/cancelled request reached network")
	}
	summary, err := b.InspectHelmRelease(context.Background(), "team", row, true)
	if err != nil || summary.ChartName != "example-chart" || calls.Load() != 1 || bad.Load() != 0 {
		t.Fatal(summary, err, calls.Load(), bad.Load())
	}
	data, _ := json.Marshal(summary)
	if strings.Contains(string(data), "PRIVATE") {
		t.Fatal("raw content escaped")
	}
}
func TestHelmInspectionPrivateErrorsNoRetryOrFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"denied", 403, "PRIVATE_ERROR", helmrelease.ErrInspectionForbidden}, {"unauthenticated", 401, "PRIVATE_ERROR", helmrelease.ErrInspectionForbidden},
		{"missing", 404, "PRIVATE_ERROR", helmrelease.ErrInspectionStale}, {"gone", 410, "PRIVATE_ERROR", helmrelease.ErrInspectionStale},
		{"throttled", 429, "PRIVATE_ERROR", helmrelease.ErrInspectionUnavailable}, {"server", 500, "PRIVATE_ERROR", helmrelease.ErrInspectionUnavailable},
		{"negotiation", 406, "PRIVATE_ERROR", helmrelease.ErrInspectionUnavailable}, {"bad-json", 200, "PRIVATE_ERROR", helmrelease.ErrInspectionInvalid},
		{"oversize", 200, strings.Repeat("x", helmrelease.MaxSecretBytes+1), helmrelease.ErrInspectionBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(tc.status)
				w.(http.Flusher).Flush()
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			b, _ := New(&rest.Config{Host: srv.URL})
			out, err := b.InspectHelmRelease(context.Background(), "team", selectedHelmRevision(), true)
			if !errors.Is(err, tc.want) || out != (helmrelease.Inspection{}) || calls.Load() != 1 {
				t.Fatal(err, calls.Load())
			}
		})
	}
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL+"/PRIVATE", 302) }))
	defer source.Close()
	b, _ := New(&rest.Config{Host: source.URL, BearerToken: "PRIVATE"})
	if _, err := b.InspectHelmRelease(context.Background(), "team", selectedHelmRevision(), true); !errors.Is(err, helmrelease.ErrInspectionUnavailable) || redirected.Load() != 0 {
		t.Fatal("redirect followed", err)
	}
}
func TestHelmInspectionStaleStorageAndCancellation(t *testing.T) {
	for _, mode := range []string{"uid", "version", "label"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s := helmfixture.Secret("team", "example", "deployed", 2)
				switch mode {
				case "uid":
					s.UID = "replacement"
				case "version":
					s.ResourceVersion = "8"
				case "label":
					s.Labels["status"] = "failed"
				}
				_ = json.NewEncoder(w).Encode(s)
			}))
			defer srv.Close()
			b, _ := New(&rest.Config{Host: srv.URL})
			out, err := b.InspectHelmRelease(context.Background(), "team", selectedHelmRevision(), true)
			if !errors.Is(err, helmrelease.ErrInspectionStale) || out != (helmrelease.Inspection{}) {
				t.Fatal(err)
			}
		})
	}
	started := make(chan struct{})
	cancelled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer srv.Close()
	b, _ := New(&rest.Config{Host: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := b.InspectHelmRelease(ctx, "team", selectedHelmRevision(), true); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request not started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("read did not cancel")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("transport remained active")
	}
}
