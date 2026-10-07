package kube

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

type ResourceEvent struct {
	Type   watch.EventType
	Object *unstructured.Unstructured
}

type EventHandler func(ResourceEvent) error

func (b *Backend) WatchResource(ctx context.Context, gvr schema.GroupVersionResource, namespace string, opts metav1.ListOptions, handler EventHandler) error {
	if handler == nil {
		return fmt.Errorf("kube: nil event handler")
	}
	var retry time.Duration
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ri := resourceInterface(b.dynamic, gvr, namespace)
		list, err := ri.List(ctx, opts)
		if err != nil {
			return fmt.Errorf("initial list %s: %w", gvr.Resource, err)
		}
		rv := list.GetResourceVersion()
		for i := range list.Items {
			obj := list.Items[i].DeepCopy()
			if err := handler(ResourceEvent{Type: watch.Added, Object: obj}); err != nil {
				return err
			}
		}

		watchOpts := opts
		watchOpts.ResourceVersion = rv
		watchOpts.AllowWatchBookmarks = true
		w, err := ri.Watch(ctx, watchOpts)
		if err != nil {
			if apierrors.IsResourceExpired(err) {
				continue
			}
			return fmt.Errorf("watch %s: %w", gvr.Resource, err)
		}

		relist, err := consumeWatch(ctx, w, handler)
		w.Stop()
		if err != nil {
			return err
		}
		if !relist {
			return ctx.Err()
		}
		if retry == 0 {
			retry = 50 * time.Millisecond
		} else if retry < time.Second {
			retry *= 2
		}
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func consumeWatch(ctx context.Context, w watch.Interface, handler EventHandler) (bool, error) {
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case event, ok := <-w.ResultChan():
			if !ok {
				return true, nil
			}
			if event.Type == watch.Error {
				if statusErr := apierrors.FromObject(event.Object); statusErr != nil {
					if apierrors.IsResourceExpired(statusErr) || apierrors.IsGone(statusErr) {
						return true, nil
					}
					return false, statusErr
				}
				return true, nil
			}
			if event.Type == watch.Bookmark {
				continue
			}
			obj, ok := event.Object.(*unstructured.Unstructured)
			if !ok {
				return false, fmt.Errorf("kube: unexpected watch object %T", event.Object)
			}
			if err := handler(ResourceEvent{Type: event.Type, Object: obj.DeepCopy()}); err != nil {
				return false, err
			}
		}
	}
}

func resourceInterface(client dynamic.Interface, gvr schema.GroupVersionResource, namespace string) dynamic.ResourceInterface {
	r := client.Resource(gvr)
	if namespace == "" {
		return r
	}
	return r.Namespace(namespace)
}
