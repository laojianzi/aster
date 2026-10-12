package operation

import (
	"context"
	"errors"

	"github.com/laojianzi/aster/internal/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Lookup reads an exact resource through this operation service's client. A
// caller deciding between create-only and apply must not probe one connection
// and prepare a write through another. An empty UID permits discovery of the
// current incarnation; a supplied UID is still a precondition. The result is
// an owned copy, not a prepared operation or an authorization to write.
func (s *Service) Lookup(ctx context.Context, target resource.Identity) (*unstructured.Unstructured, error) {
	if s == nil || s.client == nil || target.SessionID != s.session {
		return nil, errors.New("lookup requires this operation service and session")
	}
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, target.GVR, target.Namespace, target.Name)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if obj == nil || obj.GetName() != target.Name || obj.GetNamespace() != target.Namespace ||
		obj.GetAPIVersion() != target.GVR.GroupVersion().String() ||
		(target.UID != "" && obj.GetUID() != target.UID) {
		return nil, ErrConflict
	}
	return obj.DeepCopy(), nil
}
