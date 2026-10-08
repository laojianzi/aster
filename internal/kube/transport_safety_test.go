package kube

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
)

var safetyGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

func safetyObject() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "example", "namespace": "team", "uid": "one", "resourceVersion": "1"}}}
}
func TestCredentialedReadsDoNotFollowRedirects(t *testing.T) {
	for _, action := range []string{"get", "logs", "discovery"} {
		t.Run(action, func(t *testing.T) {
			var destinationCalls atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				destinationCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(safetyObject())
			}))
			defer destination.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-token" {
					t.Error("credential absent on the intended API endpoint")
				}
				http.Redirect(w, r, destination.URL+"/untrusted?secret=do-not-display", http.StatusFound)
			}))
			defer origin.Close()
			b, err := New(&rest.Config{Host: origin.URL, BearerToken: "synthetic-token", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			switch action {
			case "get":
				_, err = b.GetObject(ctx, safetyGVR, "team", "example")
			case "logs":
				err = b.ReadLogs(ctx, "team", "pod", "http", false, false, 10, func(string) error { return nil })
			case "discovery":
				_, _, err = b.Discover(ctx)
			}
			if destinationCalls.Load() != 0 || err == nil {
				t.Fatalf("redirect must be rejected before any request to the destination; calls=%d, err=%v", destinationCalls.Load(), err)
			}
			if strings.Contains(err.Error(), "do-not-display") || strings.Contains(err.Error(), "synthetic-token") {
				t.Fatal("redirect target or credential exposed in the error")
			}
		})
	}
}
func TestMutationRetryAfterNeverReplays(t *testing.T) {
	for _, verb := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		for _, status := range []int{429, 500, 503} {
			t.Run(verb+"/"+http.StatusText(status), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != verb {
						t.Errorf("unexpected method %s", r.Method)
					}
					w.Header().Set("Content-Type", "application/json")
					if calls.Add(1) == 1 {
						w.Header().Set("Retry-After", "0")
						w.WriteHeader(status)
						_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: "Failure", Reason: metav1.StatusReasonInternalError, Code: int32(status)})
						return
					}
					_ = json.NewEncoder(w).Encode(safetyObject())
				}))
				defer server.Close()
				b, err := New(&rest.Config{Host: server.URL})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
				defer cancel()
				switch verb {
				case http.MethodPost:
					_, err = b.CreateObject(ctx, safetyGVR, "team", safetyObject(), metav1.CreateOptions{})
				case http.MethodPatch:
					_, err = b.PatchObject(ctx, safetyGVR, "team", "example", types.MergePatchType, []byte(`{"data":{"key":"value"}}`), metav1.PatchOptions{})
				case http.MethodDelete:
					err = b.DeleteObject(ctx, safetyGVR, "team", "example", metav1.DeleteOptions{})
				}
				if calls.Load() != 1 || err == nil {
					t.Fatalf("mutation must return the first result without replay; calls=%d err=%v", calls.Load(), err)
				}
			})
		}
	}
}

type closeRecord struct{ closed bool }

func (b *closeRecord) Read([]byte) (int, error) { return 0, io.EOF }
func (b *closeRecord) Close() error             { b.closed = true; return nil }
func TestAPIRedirectClosesResponseWithoutDisclosingLocation(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		body := &closeRecord{}
		tr := rejectAPIRedirects{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: body, Header: http.Header{"Location": []string{"https://outside.invalid/?secret=synthetic"}}}, nil
		})}
		req, _ := http.NewRequest(http.MethodGet, "https://cluster.invalid/api/v1/pods", nil)
		response, err := tr.RoundTrip(req)
		if response != nil || !errors.Is(err, ErrAPIRedirect) || !body.closed || strings.Contains(err.Error(), "synthetic") {
			t.Fatal("unsafe redirect handling")
		}
	}
}
func TestWritePathsRejectEscapingAndPreserveAPIGroups(t *testing.T) {
	for _, gvr := range []schema.GroupVersionResource{{Version: "v1", Resource: "../pods"}, {Group: "../other", Version: "v1", Resource: "pods"}, {Version: "", Resource: "pods"}} {
		if _, err := writePath(gvr, "team", "pod"); err == nil {
			t.Fatal("path escape accepted")
		}
	}
	for _, ns := range []string{"..", "team/other", "*"} {
		if _, err := writePath(safetyGVR, ns, "example"); err == nil {
			t.Fatal("invalid namespace accepted")
		}
	}
	path, err := writePath(schema.GroupVersionResource{Group: "example.test", Version: "v1", Resource: "mice"}, "team", "mouse")
	if err != nil || strings.Join(path, "/") != "apis/example.test/v1/namespaces/team/mice/mouse" {
		t.Fatal(path, err)
	}
}
