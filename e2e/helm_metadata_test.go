//go:build e2e

package e2e

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

type helmMetadataReadTransport struct {
	base      http.RoundTripper
	namespace string
	calls     atomic.Int32
	rejected  atomic.Int32
}

func (r *helmMetadataReadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls.Add(1)
	if req.Method != "GET" || req.URL.Path != "/api/v1/namespaces/"+r.namespace+"/secrets" || req.Header.Get("Accept") != helmrelease.Accept || req.URL.Query().Get("labelSelector") != "owner=helm" {
		r.rejected.Add(1)
		return nil, errors.New("fixture blocked a non-metadata request")
	}
	return r.base.RoundTrip(req)
}
func TestRealHelmReleaseMetadataReadOnlyPaginationAndRBAC(t *testing.T) {
	f := testcluster.NewHelmMetadata(t, helmrelease.PageSize+1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := rest.CopyConfig(f.Config)
	observed := &helmMetadataReadTransport{namespace: f.Namespace}
	cfg.Wrap(func(base http.RoundTripper) http.RoundTripper { observed.base = base; return observed })
	b, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := b.HelmReleaseMetadata(ctx, f.Namespace)
	if err != nil || len(snapshot.Revisions) != len(f.Secrets) || observed.calls.Load() != 2 || observed.rejected.Load() != 0 || snapshot.Revisions[0].Number != int32(len(f.Secrets)) {
		t.Fatalf("metadata-only list/pagination: count=%d requests=%d rejected=%d err=%v", len(snapshot.Revisions), observed.calls.Load(), observed.rejected.Load(), err)
	}
	for _, before := range f.Secrets {
		after, err := f.Client.CoreV1().Secrets(f.Namespace).Get(ctx, before.Name, metav1.GetOptions{})
		if err != nil || after.UID != before.UID || after.ResourceVersion != before.ResourceVersion || !reflect.DeepEqual(before.Data, after.Data) || !reflect.DeepEqual(before.Labels, after.Labels) {
			t.Fatal("metadata read changed storage", err)
		}
	}
	// Current identity, not metadata negotiation, controls permission. No fallback
	// to the fixture administrator is allowed after impersonation denial.
	deniedCfg := rest.CopyConfig(f.Config)
	deniedCfg.Impersonate = rest.ImpersonationConfig{UserName: "aster-helm-denied-" + f.Namespace}
	denied, err := kube.New(deniedCfg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := denied.HelmReleaseMetadata(ctx, f.Namespace)
	if !errors.Is(err, helmrelease.ErrForbidden) || len(result.Revisions) != 0 {
		t.Fatal("denied identity acquired metadata", err)
	}
}
