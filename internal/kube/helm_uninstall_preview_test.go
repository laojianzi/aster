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

func TestHelmUninstallPreviewExactReadAndPrivateFailures(t *testing.T) {
	var calls, bad atomic.Int32
	var status atomic.Int32
	status.Store(200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/prefix/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v2" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer current" || r.Header.Get("Accept") != "application/json" {
			bad.Add(1)
		}
		w.WriteHeader(int(status.Load()))
		if status.Load() != 200 {
			_, _ = w.Write([]byte("PRIVATE_ERROR"))
			return
		}
		_ = json.NewEncoder(w).Encode(helmfixture.WithManifest("team", "example", "deployed", 2, helmfixture.PreviewManifest("team")))
	}))
	defer srv.Close()
	b, err := New(&rest.Config{Host: srv.URL + "/prefix", BearerToken: "current"})
	if err != nil {
		t.Fatal(err)
	}
	row := selectedHelmRevision()
	for _, tc := range []struct {
		ns, session string
		ack         bool
		want        error
	}{{"team", "session", false, helmrelease.ErrInspectionConsent}, {"team", "", true, helmrelease.ErrPreviewSession}, {"../team", "session", true, helmrelease.ErrInspectionSelection}} {
		p, e := b.PreviewHelmUninstall(context.Background(), tc.ns, row, tc.session, tc.ack)
		if p != nil || !errors.Is(e, tc.want) {
			t.Fatal("precondition", e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, e := b.PreviewHelmUninstall(ctx, "team", row, "session", true); p != nil || !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if calls.Load() != 0 {
		t.Fatal("unapproved network access")
	}
	p, err := b.PreviewHelmUninstall(context.Background(), "team", row, "session", true)
	if err != nil || len(p.Items()) != 2 || !p.Matches("session", "team", row, time.Now()) || calls.Load() != 1 || bad.Load() != 0 {
		t.Fatal("exact GET", err)
	}
	for _, code := range []int{401, 403, 404, 409, 410, 406, 429, 500} {
		status.Store(int32(code))
		before := calls.Load()
		out, e := b.PreviewHelmUninstall(context.Background(), "team", row, "session", true)
		if out != nil || e == nil || calls.Load() != before+1 || strings.Contains(e.Error(), "PRIVATE") {
			t.Fatal("error/retry boundary", code, e)
		}
	}
}

func TestHelmUninstallPreviewStaleRedirectAndCancellation(t *testing.T) {
	for _, mode := range []string{"uid", "resourceVersion", "body", "large"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s := helmfixture.WithManifest("team", "example", "deployed", 2, helmfixture.PreviewManifest("team"))
				switch mode {
				case "uid":
					s.UID = "new"
				case "resourceVersion":
					s.ResourceVersion = "8"
				case "body":
					_, _ = w.Write([]byte("PRIVATE"))
					return
				case "large":
					_, _ = w.Write([]byte(strings.Repeat("x", helmrelease.MaxSecretBytes+1)))
					return
				}
				_ = json.NewEncoder(w).Encode(s)
			}))
			defer srv.Close()
			b, _ := New(&rest.Config{Host: srv.URL})
			p, e := b.PreviewHelmUninstall(context.Background(), "team", selectedHelmRevision(), "session", true)
			if p != nil || e == nil || strings.Contains(e.Error(), "PRIVATE") {
				t.Fatal(e)
			}
		})
	}
	var redirects atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirects.Add(1) }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer srv.Close()
	b, _ := New(&rest.Config{Host: srv.URL})
	if p, e := b.PreviewHelmUninstall(context.Background(), "team", selectedHelmRevision(), "session", true); p != nil || !errors.Is(e, helmrelease.ErrInspectionUnavailable) || redirects.Load() != 0 {
		t.Fatal("redirect", e)
	}
	started, stopped := make(chan struct{}), make(chan struct{})
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer hanging.Close()
	b, _ = New(&rest.Config{Host: hanging.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		p, e := b.PreviewHelmUninstall(ctx, "team", selectedHelmRevision(), "session", true)
		if p != nil {
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
		t.Fatal("transport alive")
	}
}
