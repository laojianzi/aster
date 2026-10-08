package kube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

func TestRelationshipPagePreservesContinuationAndRejectsUnboundedScope(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("limit") != "1" || r.URL.Path != "/api/v1/namespaces/team/configmaps" {
			t.Error("unexpected bounded request", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		page := &unstructured.UnstructuredList{Object: map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMapList", "metadata": map[string]interface{}{"resourceVersion": "list-version"}}, Items: []unstructured.Unstructured{*safetyObject()}}
		if r.URL.Query().Get("continue") == "" {
			page.SetContinue("next-page")
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()
	backend, err := New(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	page, err := backend.ListPage(context.Background(), safetyGVR, "team", metav1.ListOptions{Limit: 1})
	if err != nil || page.GetContinue() != "next-page" {
		t.Fatal("continuation lost", page, err)
	}
	page, err = backend.ListPage(context.Background(), safetyGVR, "team", metav1.ListOptions{Limit: 1, Continue: page.GetContinue()})
	if err != nil || page.GetContinue() != "" {
		t.Fatal(page, err)
	}
	for _, opts := range []metav1.ListOptions{{}, {Limit: 201}, {Limit: 1, Watch: true}} {
		if _, err = backend.ListPage(context.Background(), safetyGVR, "team", opts); err == nil {
			t.Fatal("unbounded query accepted")
		}
	}
	for _, ns := range []string{"", "*"} {
		if _, err = backend.ListPage(context.Background(), safetyGVR, ns, metav1.ListOptions{Limit: 1}); err == nil {
			t.Fatal("cluster-wide query accepted")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("invalid query reached the server")
	}
}
