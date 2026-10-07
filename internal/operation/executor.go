package operation

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

type Executor struct {
	sessionID    string
	client       dynamic.Interface
	fieldManager string
}

func NewExecutor(sessionID string, client dynamic.Interface) (*Executor, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("operation: empty session id")
	}
	if client == nil {
		return nil, fmt.Errorf("operation: nil dynamic client")
	}
	return &Executor{sessionID: sessionID, client: client, fieldManager: "aster"}, nil
}

func (e *Executor) Preview(ctx context.Context, plan Plan) (*unstructured.Unstructured, error) {
	return e.run(ctx, plan, true)
}

func (e *Executor) Execute(ctx context.Context, plan Plan) (*unstructured.Unstructured, error) {
	return e.run(ctx, plan, false)
}

func (e *Executor) run(ctx context.Context, plan Plan, dryRun bool) (*unstructured.Unstructured, error) {
	if err := plan.ValidateForSession(e.sessionID); err != nil {
		return nil, err
	}
	if err := plan.Target.Validate(); err != nil {
		return nil, err
	}
	ri := operationResource(e.client, plan.Target.GVR, plan.Target.Namespace)
	switch plan.Kind {
	case Apply:
		if len(plan.Payload) == 0 {
			return nil, fmt.Errorf("operation: apply payload is empty")
		}
		opts := v1.PatchOptions{FieldManager: e.fieldManager}
		if dryRun {
			opts.DryRun = []string{v1.DryRunAll}
		}
		obj, err := ri.Patch(ctx, plan.Target.Name, types.ApplyPatchType, plan.Payload, opts)
		if err != nil {
			return nil, fmt.Errorf("apply %s: %w", plan.Target.Key(), err)
		}
		return obj, nil
	case Delete:
		opts := v1.DeleteOptions{}
		if dryRun {
			opts.DryRun = []string{v1.DryRunAll}
		}
		if plan.Preconditions.UID != "" || plan.Preconditions.ResourceVersion != "" {
			pre := &v1.Preconditions{}
			if plan.Preconditions.UID != "" {
				uid := types.UID(plan.Preconditions.UID)
				pre.UID = &uid
			}
			if plan.Preconditions.ResourceVersion != "" {
				rv := plan.Preconditions.ResourceVersion
				pre.ResourceVersion = &rv
			}
			opts.Preconditions = pre
		}
		if err := ri.Delete(ctx, plan.Target.Name, opts); err != nil {
			return nil, fmt.Errorf("delete %s: %w", plan.Target.Key(), err)
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("operation: unsupported kind %q", plan.Kind)
	}
}

func operationResource(client dynamic.Interface, gvr schema.GroupVersionResource, namespace string) dynamic.ResourceInterface {
	r := client.Resource(gvr)
	if namespace == "" {
		return r
	}
	return r.Namespace(namespace)
}
