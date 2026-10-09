package operation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/laojianzi/aster/internal/manifest"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

const applyIntent = `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"demo","namespace":"team"},"spec":{"replicas":3}}`

type applyCall struct {
	body    []byte
	kind    types.PatchType
	options metav1.PatchOptions
}
type applyFake struct {
	*mutationFake
	calls        []applyCall
	err          error
	wrongPreview bool
}

func (f *applyFake) PatchObject(_ context.Context, _ schema.GroupVersionResource, _, _ string, kind types.PatchType, body []byte, opts metav1.PatchOptions) (*unstructured.Unstructured, error) {
	f.calls = append(f.calls, applyCall{append([]byte(nil), body...), kind, *opts.DeepCopy()})
	if f.err != nil {
		return nil, f.err
	}
	o := f.object.DeepCopy()
	if f.wrongPreview {
		o.SetUID("replacement")
	}
	return o, nil
}
func TestApplyPinsReviewedIdentityAndNeverForces(t *testing.T) {
	fixture, target := mutationFixture()
	f := &applyFake{mutationFake: fixture}
	s := NewService(f, target.SessionID)
	text := []byte(applyIntent)
	plan, err := s.PrepareApply(context.Background(), target, text)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || len(f.calls[0].options.DryRun) != 1 || f.calls[0].options.DryRun[0] != metav1.DryRunAll {
		t.Fatal("not a dry-run")
	}
	for i := range text {
		text[i] = 'x'
	}
	before, _ := plan.Preview()
	before[0] = 'X'
	r, err := s.Execute(context.Background(), plan)
	if err != nil || r.State != "Succeeded" {
		t.Fatalf("%+v %v", r, err)
	}
	if len(f.calls) != 2 || !bytes.Equal(f.calls[0].body, f.calls[1].body) || len(f.calls[1].options.DryRun) != 0 {
		t.Fatal("execute changed the reviewed payload")
	}
	for _, call := range f.calls {
		if call.kind != types.ApplyPatchType || call.options.FieldManager != ApplyFieldManager || call.options.FieldValidation != "Strict" || call.options.Force == nil || *call.options.Force {
			t.Fatalf("unsafe patch options: %+v", call)
		}
		o := &unstructured.Unstructured{}
		if err := o.UnmarshalJSON(call.body); err != nil {
			t.Fatal(err)
		}
		if o.GetUID() != target.UID || o.GetResourceVersion() != "10" {
			t.Fatal("missing atomic identity/version preconditions")
		}
		if o.GetManagedFields() != nil {
			t.Fatal("imported managed fields as intent")
		}
	}
	if _, err = s.Execute(context.Background(), plan); err == nil || len(f.calls) != 2 {
		t.Fatal("replayed apply")
	}
}
func TestApplyRejectsUnsafeOrAmbiguousIntent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]interface{})
	}{
		{"status", func(m map[string]interface{}) { m["status"] = nil }},
		{"uid", func(m map[string]interface{}) { m["metadata"].(map[string]interface{})["uid"] = "original" }},
		{"version", func(m map[string]interface{}) { m["metadata"].(map[string]interface{})["resourceVersion"] = "10" }},
		{"managedFields", func(m map[string]interface{}) {
			m["metadata"].(map[string]interface{})["managedFields"] = []interface{}{}
		}},
		{"owners", func(m map[string]interface{}) {
			m["metadata"].(map[string]interface{})["ownerReferences"] = []interface{}{}
		}},
		{"finalizers", func(m map[string]interface{}) { m["metadata"].(map[string]interface{})["finalizers"] = []interface{}{} }},
		{"identityOnly", func(m map[string]interface{}) { delete(m, "spec") }},
		{"wrongName", func(m map[string]interface{}) { m["metadata"].(map[string]interface{})["name"] = "other" }},
		{"wrongNamespace", func(m map[string]interface{}) { m["metadata"].(map[string]interface{})["namespace"] = "other" }},
		{"wrongKind", func(m map[string]interface{}) { m["kind"] = "StatefulSet" }},
		{"wrongAPIVersion", func(m map[string]interface{}) { m["apiVersion"] = "apps/v2" }},
		{"secret", func(m map[string]interface{}) { m["kind"] = "Secret" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(applyIntent), &obj); err != nil {
				t.Fatal(err)
			}
			tc.change(obj)
			body, err := json.Marshal(obj)
			if err != nil {
				t.Fatal(err)
			}
			fixture, target := mutationFixture()
			f := &applyFake{mutationFake: fixture}
			s := NewService(f, target.SessionID)
			if _, err = s.PrepareApply(context.Background(), target, body); err == nil {
				t.Fatal("unsafe intent accepted")
			}
			if len(f.calls) != 0 {
				t.Fatal("unsafe intent reached PATCH")
			}
		})
	}
	fixture, target := mutationFixture()
	f := &applyFake{mutationFake: fixture}
	s := NewService(f, target.SessionID)
	if _, err := s.PrepareApply(context.Background(), target, []byte(strings.Repeat("a", manifest.MaxBytes+1))); err == nil || len(f.calls) != 0 {
		t.Fatal("unbounded intent")
	}
}
func TestApplyConflictChangedVersionCrossIssuerAndUnknown(t *testing.T) {
	fixture, target := mutationFixture()
	f := &applyFake{mutationFake: fixture}
	s := NewService(f, target.SessionID)
	f.err = apierrors.NewConflict(target.GVR.GroupResource(), target.Name, errors.New("field owned by other-manager"))
	if p, err := s.PrepareApply(context.Background(), target, []byte(applyIntent)); p != nil || !apierrors.IsConflict(err) {
		t.Fatalf("conflict was hidden: %v", err)
	}
	f.err = nil
	f.calls = nil
	p, err := s.PrepareApply(context.Background(), target, []byte(applyIntent))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewService(f, target.SessionID).Execute(context.Background(), p); err == nil {
		t.Fatal("cross-issuer plan accepted")
	}
	f.object.SetResourceVersion("11")
	if _, err = s.Execute(context.Background(), p); !errors.Is(err, ErrConflict) || len(f.calls) != 1 {
		t.Fatal("stale apply sent")
	}
	p, err = s.PrepareApply(context.Background(), target, []byte(applyIntent))
	if err != nil {
		t.Fatal(err)
	}
	f.err = context.DeadlineExceeded
	r, err := s.Execute(context.Background(), p)
	if r.State != "Unknown" || err == nil {
		t.Fatalf("%+v %v", r, err)
	}
	n := len(f.calls)
	_, _ = s.Execute(context.Background(), p)
	if len(f.calls) != n {
		t.Fatal("unknown apply replayed")
	}
}
func TestApplyRejectsUnversionedTerminatingOrReplacedResource(t *testing.T) {
	for _, mode := range []string{"unversioned", "terminating", "replaced", "preview-replaced", "expired", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			fixture, target := mutationFixture()
			f := &applyFake{mutationFake: fixture}
			s := NewService(f, target.SessionID)
			ctx := context.Background()
			switch mode {
			case "unversioned":
				f.object.SetResourceVersion("")
			case "terminating":
				now := metav1.Now()
				f.object.SetDeletionTimestamp(&now)
			case "replaced":
				f.object.SetUID("other")
			case "preview-replaced":
				f.wrongPreview = true
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			p, err := s.PrepareApply(ctx, target, []byte(applyIntent))
			if mode == "expired" {
				if err != nil {
					t.Fatal(err)
				}
				s.now = p.Expires
				if _, err = s.Execute(ctx, p); err == nil {
					t.Fatal("expired apply accepted")
				}
				return
			}
			if err == nil || p != nil {
				t.Fatal("unsafe resource accepted")
			}
			if mode != "preview-replaced" && len(f.calls) != 0 {
				t.Fatal("unsafe resource sent to PATCH")
			}
		})
	}
}
