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

	"github.com/laojianzi/aster/internal/helmrelease"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

func releaseMeta(number int) metav1.PartialObjectMetadata {
	v := strconv.Itoa(number)
	key := "sh.helm.release.v1.app.v" + v
	return metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: key, Namespace: "team", UID: types.UID("uid-" + v), ResourceVersion: "7", Labels: map[string]string{"owner": "helm", "name": "app", "version": v, "status": "deployed"}}}
}
func releasePage(token, rv string, items ...metav1.PartialObjectMetadata) []byte {
	if items == nil {
		items = []metav1.PartialObjectMetadata{}
	}
	b, _ := json.Marshal(metav1.PartialObjectMetadataList{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadataList"}, ListMeta: metav1.ListMeta{ResourceVersion: rv, Continue: token}, Items: items})
	return b
}
func TestHelmReleaseMetadataTransportAndPagination(t *testing.T) {
	var calls atomic.Int32
	const token = "private+continue/=?&"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/prefix/api/v1/namespaces/team/secrets" || r.Header.Get("Authorization") != "Bearer current" || r.Header.Get("Accept") != helmrelease.Accept || r.URL.Query().Get("labelSelector") != "owner=helm" || r.URL.Query().Get("limit") != "64" {
			t.Errorf("request contract: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			if r.URL.Query().Get("continue") != "" {
				t.Error("unexpected initial token")
			}
			_, _ = w.Write(releasePage(token, "snapshot", releaseMeta(9)))
		} else {
			if r.URL.Query().Get("continue") != token {
				t.Error("token changed")
			}
			_, _ = w.Write(releasePage("", "snapshot", releaseMeta(10)))
		}
	}))
	defer server.Close()
	b, err := New(&rest.Config{Host: server.URL + "/prefix", BearerToken: "current"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.HelmReleaseMetadata(context.Background(), "team")
	if err != nil || calls.Load() != 2 || len(result.Revisions) != 2 || result.Revisions[0].Number != 10 || result.ResourceVersion != "snapshot" || result.ObservedAt.IsZero() {
		t.Fatalf("%+v %v calls %d", result, err, calls.Load())
	}
}
func TestHelmReleaseMetadataRejectsFailuresWithoutFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"denied", 403, "PRIVATE", helmrelease.ErrForbidden}, {"unauthenticated", 401, "PRIVATE", helmrelease.ErrForbidden},
		{"unsupported", 406, "PRIVATE", helmrelease.ErrUnsupported}, {"expired", 410, "PRIVATE", helmrelease.ErrExpired},
		{"throttled", 429, "PRIVATE", helmrelease.ErrUnavailable}, {"server", 500, "PRIVATE", helmrelease.ErrUnavailable},
		{"malformed", 200, "PRIVATE", helmrelease.ErrInvalid}, {"full-secret", 200, `{"apiVersion":"v1","kind":"SecretList","items":[],"metadata":{"resourceVersion":"1"}}`, helmrelease.ErrUnsupported},
		{"oversize", 200, strings.Repeat("x", helmrelease.MaxPageBytes+1), helmrelease.ErrBudget},
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
			snapshot, err := b.HelmReleaseMetadata(context.Background(), "team")
			if !errors.Is(err, tc.want) || len(snapshot.Revisions) != 0 || calls.Load() != 1 {
				t.Fatalf("%v calls %d", err, calls.Load())
			}
			if strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("response leaked")
			}
		})
	}
	for _, mode := range []string{"loop", "duplicate", "uid-duplicate", "changed-rv", "pages", "items", "total-bytes", "late-denied"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := int(calls.Add(1))
				token, rv := strconv.Itoa(n), "snapshot"
				items := []metav1.PartialObjectMetadata{releaseMeta(n)}
				switch mode {
				case "loop":
					token = "loop"
				case "duplicate":
					items = []metav1.PartialObjectMetadata{releaseMeta(1)}
				case "uid-duplicate":
					items[0].UID = "same"
				case "changed-rv":
					rv = strconv.Itoa(n)
				case "pages":
					items = nil
				case "items":
					items = nil
					for i := 0; i < helmrelease.PageSize; i++ {
						items = append(items, releaseMeta((n-1)*helmrelease.PageSize+i+1))
					}
				case "total-bytes":
					items[0].Annotations = map[string]string{"private": strings.Repeat("x", 240<<10)}
				case "late-denied":
					if n > 1 {
						w.WriteHeader(403)
						return
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(releasePage(token, rv, items...))
			}))
			defer server.Close()
			b, _ := New(&rest.Config{Host: server.URL, RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()})
			s, err := b.HelmReleaseMetadata(context.Background(), "team")
			if err == nil || len(s.Revisions) != 0 || s.ResourceVersion != "" || calls.Load() > helmrelease.MaxPages {
				t.Fatalf("partial or unbounded: %+v %v calls=%d", s, err, calls.Load())
			}
		})
	}
}
func TestHelmReleaseMetadataCancellationRedirectAndInvalidNamespace(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL+"/PRIVATE", 302) }))
	defer source.Close()
	b, _ := New(&rest.Config{Host: source.URL, BearerToken: "PRIVATE"})
	if _, err := b.HelmReleaseMetadata(context.Background(), "team"); !errors.Is(err, helmrelease.ErrUnavailable) || leaked.Load() != 0 {
		t.Fatal("followed redirect", err)
	}
	var calls atomic.Int32
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer stalled.Close()
	b, _ = New(&rest.Config{Host: stalled.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := b.HelmReleaseMetadata(ctx, "team"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	before := calls.Load()
	for _, ns := range []string{"", "*", "../team", "team/other"} {
		if _, err := b.HelmReleaseMetadata(context.Background(), ns); !errors.Is(err, helmrelease.ErrNamespace) {
			t.Fatal(ns, err)
		}
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := b.HelmReleaseMetadata(ctx2, "team"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls.Load() != before {
		t.Fatal("invalid/cancelled input reached server")
	}
}
