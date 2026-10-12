package operation

import (
	"context"
	"errors"
	"testing"

	"github.com/laojianzi/aster/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type lookupFake struct {
	*mutationFake
	gets   int
	getErr error
	cancel context.CancelFunc
}

func (f *lookupFake) GetObject(_ context.Context, _ schema.GroupVersionResource, _, _ string) (*unstructured.Unstructured, error) {
	f.gets++
	if f.cancel != nil {
		f.cancel()
	}
	// Deliberately return a borrowed object to verify the public API's copy.
	return f.object, f.getErr
}

func TestOperationLookupUsesBoundServiceAndReturnsOwnedIdentity(t *testing.T) {
	fixture, target := mutationFixture()
	f := &lookupFake{mutationFake: fixture}
	s := NewService(f, target.SessionID)
	withoutUID := target
	withoutUID.UID = ""
	obj, err := s.Lookup(context.Background(), withoutUID)
	if err != nil || obj.GetUID() != target.UID || f.gets != 1 {
		t.Fatalf("lookup: object=%v err=%v reads=%d", obj, err, f.gets)
	}
	obj.SetUID("changed-copy")
	obj.Object["spec"].(map[string]interface{})["replicas"] = int64(9)
	if f.object.GetUID() != target.UID || f.object.Object["spec"].(map[string]interface{})["replicas"] != int64(1) {
		t.Fatal("lookup result aliases the service client object")
	}
	if _, err = s.Lookup(context.Background(), target); err != nil || f.gets != 2 {
		t.Fatalf("UID-bound lookup failed: %v", err)
	}
	other := target
	other.SessionID = "other-session"
	if _, err = s.Lookup(context.Background(), other); err == nil || f.gets != 2 {
		t.Fatal("cross-session lookup reached the client")
	}
	for _, empty := range []*Service{nil, NewService(nil, target.SessionID)} {
		if _, err = empty.Lookup(context.Background(), target); err == nil {
			t.Fatal("missing service client was accepted")
		}
	}
	for _, change := range []func(*resource.Identity){
		func(i *resource.Identity) { i.Name = "" },
		func(i *resource.Identity) { i.GVR.Version = "" },
		func(i *resource.Identity) { i.GVR.Resource = "" },
	} {
		invalid := target
		change(&invalid)
		if _, err = s.Lookup(context.Background(), invalid); err == nil || f.gets != 2 {
			t.Fatal("invalid target reached the client")
		}
	}
	if f.dryRuns != 0 || f.writes != 0 {
		t.Fatal("lookup prepared or submitted a mutation")
	}
}

func TestOperationLookupRejectsChangedIdentityAndPreservesReadFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*unstructured.Unstructured) *unstructured.Unstructured
	}{
		{"nil", func(*unstructured.Unstructured) *unstructured.Unstructured { return nil }},
		{"name", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetName("other"); return o }},
		{"namespace", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetNamespace("other"); return o }},
		{"version", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetAPIVersion("apps/v2"); return o }},
		{"uid", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetUID("replacement"); return o }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, target := mutationFixture()
			fixture.object = tc.change(fixture.object)
			f := &lookupFake{mutationFake: fixture}
			if _, err := NewService(f, target.SessionID).Lookup(context.Background(), target); !errors.Is(err, ErrConflict) {
				t.Fatalf("different lookup identity accepted: %v", err)
			}
		})
	}
	fixture, target := mutationFixture()
	f := &lookupFake{mutationFake: fixture}
	s := NewService(f, target.SessionID)
	for _, err := range []error{
		apierrors.NewNotFound(target.GVR.GroupResource(), target.Name),
		apierrors.NewForbidden(target.GVR.GroupResource(), target.Name, errors.New("denied")),
		apierrors.NewUnauthorized("denied"),
		context.DeadlineExceeded,
	} {
		f.getErr = err
		if obj, got := s.Lookup(context.Background(), target); obj != nil || got != err {
			t.Fatalf("read failure was altered: object=%v err=%v", obj, got)
		}
	}
	f.getErr = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reads := f.gets
	if _, err := s.Lookup(ctx, target); !errors.Is(err, context.Canceled) || f.gets != reads {
		t.Fatal("cancelled lookup reached the client")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	f.cancel = cancel
	if obj, err := s.Lookup(ctx, target); obj != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled successful read was published")
	}
	if f.dryRuns != 0 || f.writes != 0 {
		t.Fatal("lookup read failure caused a mutation")
	}
}
