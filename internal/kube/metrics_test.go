package kube

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/resourcemetrics"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

func TestMetricsTransportNegotiatesVersionsAndBoundsResponse(t *testing.T) {
	for _, version := range []string{"v1", "v1beta1"} {
		t.Run(version, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer test-credential" || r.Method != "GET" {
					t.Error("wrong credential or method")
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/apis/metrics.k8s.io" {
					fmt.Fprintf(w, `{"name":"metrics.k8s.io","versions":[{"version":"%s","groupVersion":"metrics.k8s.io/%s"}]}`, version, version)
					return
				}
				if r.URL.Path != "/apis/metrics.k8s.io/"+version+"/namespaces/team/pods/pod" {
					t.Error(r.URL.Path)
				}
				fmt.Fprintf(w, `{"apiVersion":"metrics.k8s.io/%s","kind":"PodMetrics","metadata":{"name":"pod","namespace":"team"}}`, version)
			}))
			defer server.Close()
			b, err := New(&rest.Config{Host: server.URL, BearerToken: "test-credential"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = b.MetricsObject(context.Background(), schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "team", "pod")
			if err != nil || calls.Load() != 2 {
				t.Fatalf("%v calls=%d", err, calls.Load())
			}
		})
	}
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"absent", 404, "private-response", resourcemetrics.ErrNotInstalled},
		{"forbidden", 403, "private-response", resourcemetrics.ErrForbidden},
		{"unauthorized", 401, "private-response", resourcemetrics.ErrForbidden},
		{"unavailable", 503, "private-response", resourcemetrics.ErrUnavailable},
		{"too large", 200, strings.Repeat("x", (256<<10)+1), resourcemetrics.ErrInvalid},
		{"bad json", 200, "{bad", resourcemetrics.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(tc.status)
				w.(http.Flusher).Flush()
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			b, _ := New(&rest.Config{Host: server.URL})
			_, err := b.MetricsObject(context.Background(), schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "team", "pod")
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "private-response") || calls.Load() != 1 {
				t.Fatalf("%v calls=%d", err, calls.Load())
			}
		})
	}
}
func TestMetricsTransportNeverRedirectsAndCancels(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL+"/sensitive", 302) }))
	defer server.Close()
	b, _ := New(&rest.Config{Host: server.URL, BearerToken: "private"})
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	_, err := b.MetricsObject(context.Background(), gvr, "team", "pod")
	if err == nil || leaked.Load() != 0 || strings.Contains(err.Error(), "sensitive") {
		t.Fatal("redirect followed or leaked")
	}
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer stalled.Close()
	b, _ = New(&rest.Config{Host: stalled.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = b.MetricsObject(ctx, gvr, "team", "pod")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
