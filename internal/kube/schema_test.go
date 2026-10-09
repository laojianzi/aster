package kube

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/schemaassist"
	"github.com/laojianzi/aster/internal/testschema"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

func TestSchemaTransportUsesIdentityAndFixedPath(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("wrong identity/method")
		}
		switch r.URL.Path {
		case "/prefix/openapi/v3":
			fmt.Fprint(w, testschema.Index)
		case "/prefix/openapi/v3/api/v1":
			if r.URL.RawQuery != "hash=AB12" {
				t.Error("hash lost")
			}
			fmt.Fprint(w, testschema.Document)
		default:
			t.Error("unexpected path", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	b, e := New(&rest.Config{Host: server.URL + "/prefix", BearerToken: "fixture"})
	if e != nil {
		t.Fatal(e)
	}
	d, e := b.SchemaDocument(context.Background(), schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.Help(context.Background(), "/spec"); e != nil || calls.Load() != 2 {
		t.Fatalf("%d %v", calls.Load(), e)
	}
}
func TestSchemaRejectsHostPathAndQueryInjection(t *testing.T) {
	for _, bad := range []string{"https://evil.test/openapi/v3/api/v1", "//evil.test/openapi/v3/api/v1", "/openapi/v3/api/v1/../v2", "/openapi/v3/api%2Fv1", "/openapi/v3/api/v1?hash=x", "/openapi/v3/api/v1?hash=AB&token=secret", "/openapi/v3/api/v1?hash=AB&hash=12", "/openapi/v3/api/v1#fragment"} {
		t.Run(bad, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				fmt.Fprintf(w, `{"paths":{"api/v1":{"serverRelativeURL":%q}}}`, bad)
			}))
			defer server.Close()
			b, _ := New(&rest.Config{Host: server.URL})
			_, e := b.SchemaDocument(context.Background(), schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
			if e == nil || calls.Load() != 1 || strings.Contains(e.Error(), "secret") {
				t.Fatalf("calls=%d err=%v", calls.Load(), e)
			}
		})
	}
}
func TestSchemaTransportRejectsRedirectAndSanitizesErrors(t *testing.T) {
	for _, status := range []int{301, 302, 307, 308, 401, 403, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var escaped atomic.Int32
			other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { escaped.Add(1) }))
			defer other.Close()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", other.URL)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				fmt.Fprint(w, "PRIVATE SERVER BODY")
			}))
			defer server.Close()
			b, _ := New(&rest.Config{Host: server.URL})
			_, e := b.SchemaDocument(context.Background(), schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
			if e == nil || escaped.Load() != 0 || calls.Load() != 1 || strings.Contains(e.Error(), "PRIVATE") {
				t.Fatalf("calls=%d escaped=%d err=%v", calls.Load(), escaped.Load(), e)
			}
		})
	}
}
func TestSchemaDecodedSizeAndCancellation(t *testing.T) {
	t.Run("gzip", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")
			g := gzip.NewWriter(w)
			_, _ = g.Write([]byte(strings.Repeat(" ", schemaassist.MaxIndexBytes+1)))
			_ = g.Close()
		}))
		defer server.Close()
		b, _ := New(&rest.Config{Host: server.URL})
		_, e := b.SchemaDocument(context.Background(), schema.GroupVersionKind{Version: "v1", Kind: "Pod"})
		if !errors.Is(e, schemaassist.ErrLimit) {
			t.Fatal(e)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		started, stopped := make(chan struct{}), make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(stopped) }))
		defer server.Close()
		b, _ := New(&rest.Config{Host: server.URL})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, e := b.SchemaDocument(ctx, schema.GroupVersionKind{Version: "v1", Kind: "Pod"}); done <- e }()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("not started")
		}
		cancel()
		if e := <-done; !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("server did not cancel")
		}
	})
}
