package delivery

import (
	"bytes"
	"context"
	"errors"

	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var ErrAcknowledgementRequired = errors.New("review and acknowledge field ownership and omitted-field effects before applying an existing object")

// Prepare only performs reads and server dry-runs. The same operation service
// owns the existence lookup, the prepared plan and eventual explicit execution.
// Only a real API NotFound permits create; no failure falls back to another verb.
// The caller owns item ordering and must invalidate plans after any source,
// session, namespace or action-epoch change, cancellation or failed execution.
func Prepare(ctx context.Context, ops *operation.Service, set *Set, index int, acknowledged bool) (*operation.Prepared, error) {
	if ctx == nil {
		return nil, errors.New("manifest delivery: missing context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ops == nil || set == nil || set.id == "" || set.sessionID == "" || index < 0 || index >= len(set.documents) {
		return nil, errors.New("manifest delivery: invalid service, set or item")
	}
	doc := set.documents[index]
	target := resource.Identity{SessionID: set.sessionID, GVR: doc.item.Kind.GVR, Namespace: doc.item.Namespace, Name: doc.item.Name}
	current, err := ops.Lookup(ctx, target)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var plan *operation.Prepared
	if apierrors.IsNotFound(err) {
		plan, err = ops.PrepareCreate(ctx, target, bytes.Clone(doc.payload))
	} else if err != nil {
		return nil, err
	} else {
		if current == nil || current.GetAPIVersion() != target.GVR.GroupVersion().String() || current.GetKind() != doc.item.Kind.Kind || current.GetName() != target.Name || current.GetNamespace() != target.Namespace || current.GetUID() == "" || current.GetResourceVersion() == "" || current.GetDeletionTimestamp() != nil {
			return nil, operation.ErrConflict
		}
		if !acknowledged {
			return nil, ErrAcknowledgementRequired
		}
		target.UID = current.GetUID()
		plan, err = ops.PrepareApply(ctx, target, bytes.Clone(doc.payload))
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, operation.ErrConflict
	}
	before, after := plan.Preview()
	if len(before) > manifest.MaxBytes || len(after) == 0 || len(after) > manifest.MaxBytes {
		return nil, errors.New("manifest delivery: preview exceeds the complete review budget")
	}
	preview := &unstructured.Unstructured{}
	if err := preview.UnmarshalJSON(after); err != nil || preview.GetName() != target.Name || preview.GetNamespace() != target.Namespace || preview.GetAPIVersion() != target.GVR.GroupVersion().String() || preview.GetKind() != doc.item.Kind.Kind || (target.UID != "" && preview.GetUID() != target.UID) {
		return nil, operation.ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return plan, nil
}
