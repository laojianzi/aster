package operation

import (
	"context"
	"errors"
	"fmt"

	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// ApplyFieldManager is deliberately stable across Aster installations. It is a
// field-ownership workflow identity, not a user identity or authorization role.
// Optimistic concurrency is enforced separately with the reviewed UID and RV.
const ApplyFieldManager = "aster-apply"

// PrepareApply applies an explicit partial intent to an EXISTING object only.
// Server dry-run supplies both value and ownership previews. It never forces a
// conflict, imports the live object's fields as intent, or silently creates an
// object removed after preflight. New objects use the separate POST workflow.
func (s *Service) PrepareApply(ctx context.Context, target resource.Identity, text []byte) (*Prepared, error) {
	desired, err := manifest.Decode(text)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if target.GVR.Resource == "secrets" || desired.GetKind() == "Secret" {
		return nil, errors.New("Secret apply is disabled in the desktop editor")
	}
	metadata, ok := desired.Object["metadata"].(map[string]interface{})
	if !ok {
		return nil, errors.New("apply metadata must be an object")
	}
	// Only declarative identity, labels and annotations are accepted here. A
	// copied live document's server fields/finalizers/owners are not silently
	// stripped; require the operator to provide deliberate, minimal intent.
	for key := range metadata {
		switch key {
		case "name", "namespace", "labels", "annotations":
		default:
			return nil, fmt.Errorf("apply intent must not include metadata.%s", key)
		}
	}
	if _, ok := desired.Object["status"]; ok {
		return nil, errors.New("apply intent must not include status")
	}
	meaningful := len(desired.GetLabels()) > 0 || len(desired.GetAnnotations()) > 0
	for key := range desired.Object {
		if key != "apiVersion" && key != "kind" && key != "metadata" {
			meaningful = true
		}
	}
	if !meaningful {
		return nil, errors.New("add explicitly managed fields; identity-only apply is not supported")
	}
	old, err := s.baseline(ctx, target)
	if err != nil {
		return nil, err
	}
	if old.GetKind() == "Secret" {
		return nil, errors.New("Secret apply is disabled in the desktop editor")
	}
	if old.GetResourceVersion() == "" || old.GetDeletionTimestamp() != nil {
		return nil, errors.New("apply requires a versioned, non-terminating resource")
	}
	if desired.GroupVersionKind() != old.GroupVersionKind() || desired.GetName() != old.GetName() || desired.GetNamespace() != old.GetNamespace() || old.GetAPIVersion() != target.GVR.GroupVersion().String() {
		return nil, ErrConflict
	}
	// Nonempty resourceVersion prevents the apply patch from taking the create
	// path; UID additionally pins the incarnation. Both remain in the exact
	// payload submitted after review, closing the GET -> PATCH race.
	desired.SetUID(old.GetUID())
	desired.SetResourceVersion(old.GetResourceVersion())
	body, err := desired.MarshalJSON()
	if err != nil {
		return nil, err
	}
	force := false
	after, err := s.client.PatchObject(ctx, target.GVR, target.Namespace, target.Name, types.ApplyPatchType, body, metav1.PatchOptions{
		DryRun: []string{metav1.DryRunAll}, FieldManager: ApplyFieldManager, FieldValidation: "Strict", Force: &force,
	})
	if err != nil {
		return nil, err
	}
	if after == nil || after.GetUID() != target.UID || after.GetName() != target.Name || after.GetNamespace() != target.Namespace || after.GroupVersionKind() != old.GroupVersionKind() {
		return nil, errors.New("apply preview returned a different resource identity")
	}
	return s.prepared(target, "apply", old, after, body)
}
