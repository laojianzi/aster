//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/resource"
	"github.com/laojianzi/aster/internal/testcluster"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type afterCommitFailure struct {
	base     http.RoundTripper
	method   string
	writes   *atomic.Int32
	injected *atomic.Bool
}

func (f afterCommitFailure) RoundTrip(req *http.Request) (*http.Response, error) {
	dryRun := req.URL.Query().Get("dryRun") != ""
	// DELETE options are in the JSON body, unlike create/patch query options.
	// Inspect a bounded copy and forward exactly the same bytes to the real API.
	if req.Method == http.MethodDelete && req.Body != nil {
		body, err := io.ReadAll(io.LimitReader(req.Body, (1<<20)+1))
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(body) > 1<<20 {
			return nil, fmt.Errorf("test DeleteOptions exceeds budget")
		}
		var options metav1.DeleteOptions
		if err := json.Unmarshal(body, &options); err != nil {
			return nil, fmt.Errorf("test DeleteOptions: %w", err)
		}
		dryRun = dryRun || len(options.DryRun) != 0
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	mutation := req.Method == f.method && !dryRun
	if mutation {
		f.writes.Add(1)
	}
	response, err := f.base.RoundTrip(req)
	if mutation && err == nil && response.StatusCode >= 200 && response.StatusCode < 300 && f.injected.CompareAndSwap(false, true) {
		_ = response.Body.Close()
		// The real API server has already committed. Simulate an intermediary that
		// loses the success result and sends a retryable error to the desktop.
		const body = `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"ServiceUnavailable","code":503,"message":"injected after commit"}`
		response.StatusCode = http.StatusServiceUnavailable
		response.Status = "503 Service Unavailable"
		response.Header = http.Header{"Content-Type": []string{"application/json"}, "Retry-After": []string{"0"}}
		response.Body = io.NopCloser(strings.NewReader(body))
		response.ContentLength = int64(len(body))
	}
	return response, err
}
func TestRealCommittedMutationsAreUnknownAndNeverReplayed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			cfg := testcluster.Config(t)
			adminBackend, err := kube.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			nsName := fmt.Sprintf("aster-no-replay-%s-%d", strings.ToLower(method), time.Now().UnixNano())
			nsGVR := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
			_, err = adminBackend.CreateObject(ctx, nsGVR, "", &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]interface{}{"name": nsName}}}, metav1.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				bg, done := context.WithTimeout(context.Background(), 20*time.Second)
				defer done()
				_ = adminBackend.DeleteObject(bg, nsGVR, "", nsName, metav1.DeleteOptions{})
			})
			var writes atomic.Int32
			var injected atomic.Bool
			cfg.Wrap(func(base http.RoundTripper) http.RoundTripper {
				return afterCommitFailure{base, method, &writes, &injected}
			})
			b, err := kube.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			target := resource.Identity{SessionID: "replay-test", GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Namespace: nsName, Name: "reviewed"}
			obj := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": target.Name, "namespace": nsName}, "data": map[string]interface{}{"value": "before"}}}
			if method != http.MethodPost {
				obj, err = adminBackend.CreateObject(ctx, target.GVR, nsName, obj, metav1.CreateOptions{})
				if err != nil {
					t.Fatal(err)
				}
				target.UID = obj.GetUID()
			}
			service := operation.NewService(b, target.SessionID)
			var prepared *operation.Prepared
			switch method {
			case http.MethodPost:
				text, _ := manifest.Display(obj, false)
				prepared, err = service.PrepareCreate(ctx, target, []byte(text))
			case http.MethodPatch:
				_ = unstructured.SetNestedField(obj.Object, "after", "data", "value")
				text, _ := manifest.Display(obj, false)
				prepared, err = service.PrepareEdit(ctx, target, []byte(text))
			case http.MethodDelete:
				prepared, err = service.PrepareDelete(ctx, target)
			}
			if err != nil {
				t.Fatal(err)
			}
			if writes.Load() != 0 || injected.Load() {
				t.Fatal("preview triggered a committed write")
			}
			result, err := service.Execute(ctx, prepared)
			if err == nil || result.State != "Unknown" || writes.Load() != 1 || !injected.Load() {
				t.Fatalf("lost response was replayed or misclassified: %+v %v writes=%d", result, err, writes.Load())
			}
			if _, err = service.Execute(ctx, prepared); err == nil || writes.Load() != 1 {
				t.Fatal("consumed plan was replayed")
			}
			actual, err := adminBackend.GetObject(ctx, target.GVR, nsName, target.Name)
			if method == http.MethodDelete {
				if !apierrors.IsNotFound(err) {
					t.Fatalf("real delete did not persist: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				value, _, _ := unstructured.NestedString(actual.Object, "data", "value")
				if method == http.MethodPatch && value != "after" {
					t.Fatal("real patch did not persist")
				}
				if method == http.MethodPost && value != "before" {
					t.Fatal("real create did not persist")
				}
			}
		})
	}
}

// This validates the fault-injection harness, not a Kubernetes E2E behavior.
func TestFaultInjectionRecognizesDeleteDryRun(t *testing.T) {
	const body = `{"dryRun":["All"],"preconditions":{"uid":"synthetic-uid"}}`
	var writes atomic.Int32
	var injected atomic.Bool
	req, err := http.NewRequest(http.MethodDelete, "https://cluster.invalid/api/v1/namespaces/team/configmaps/reviewed", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	base := faultRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		actual, err := io.ReadAll(r.Body)
		if err != nil || string(actual) != body {
			t.Fatalf("DeleteOptions changed: %s %v", actual, err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"kind":"Status","apiVersion":"v1","status":"Success"}`)), Header: make(http.Header)}, nil
	})
	response, err := (afterCommitFailure{base, http.MethodDelete, &writes, &injected}).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if writes.Load() != 0 || injected.Load() || response.StatusCode != 200 {
		t.Fatal("dry-run DELETE was mistaken for a persisted mutation")
	}
}

type faultRoundTripFunc func(*http.Request) (*http.Response, error)

func (f faultRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
