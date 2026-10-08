package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

const MaxSnapshotObjects = 20000
const MaxSnapshotBytes = 64 << 20
var ErrWorkingSet = errors.New("resource working set exceeds limit; narrow namespace or label selector")

type ResourceEvent struct { Type watch.EventType; Object *unstructured.Unstructured }
type EventHandler func(ResourceEvent) error

type watchCache struct { objects map[string]*unstructured.Unstructured; sizes map[string]int; bytes int }
func newWatchCache() *watchCache { return &watchCache{objects: map[string]*unstructured.Unstructured{}, sizes: map[string]int{}} }
func objectKey(o *unstructured.Unstructured) string { return o.GetNamespace()+"/"+o.GetName() }
func (s *watchCache) put(o *unstructured.Unstructured) error {
	data, err := json.Marshal(o.Object); if err != nil { return err }
	key := objectKey(o)
	size := len(data)
	_, exists := s.objects[key]
	if (!exists && len(s.objects) >= MaxSnapshotObjects) || s.bytes-s.sizes[key]+size > MaxSnapshotBytes { return ErrWorkingSet }
	s.bytes += size-s.sizes[key]; s.sizes[key] = size; s.objects[key] = o
	return nil
}
func (s *watchCache) remove(o *unstructured.Unstructured) {
	key := objectKey(o)
	if old := s.objects[key]; old != nil && old.GetUID() == o.GetUID() { s.bytes -= s.sizes[key]; delete(s.objects,key); delete(s.sizes,key) }
}

func (b *Backend) WatchResource(ctx context.Context, gvr schema.GroupVersionResource, ns string, opts metav1.ListOptions, handler EventHandler) error {
	return b.WatchWithStatus(ctx,gvr,ns,opts,handler,nil)
}

// WatchWithStatus reconciles complete paginated snapshots after 410, resumes
// normal disconnects from the last observed resourceVersion, and never turns a
// permission error into an empty list. Callbacks execute serially on the caller.
func (b *Backend) WatchWithStatus(ctx context.Context, gvr schema.GroupVersionResource, ns string, opts metav1.ListOptions, handler EventHandler, status func(string)) error {
	if handler == nil { return errors.New("kube: nil event handler") }
	if opts.Continue != "" { return errors.New("watch cannot start from a partial list") }
	state := func(s string) { if status != nil { status(s) } }
	ri := resourceInterface(b.dynamic,gvr,ns)
	known := newWatchCache()
	rv := ""
	backoff := 100*time.Millisecond
	for {
		if err := ctx.Err(); err != nil { return err }
		if rv == "" {
			state("Synchronizing")
			next, version, err := listSnapshot(ctx,ri,opts)
			if err != nil {
				if !retryable(err) { return err }
				state("Reconnecting (stale)")
				if err := retryWait(ctx,err,backoff); err != nil { return err }; backoff = min(backoff*2,5*time.Second); continue
			}
			for key, old := range known.objects {
				current := next.objects[key]
				if current == nil || current.GetUID() != old.GetUID() { if err := handler(ResourceEvent{watch.Deleted,old.DeepCopy()}); err != nil { return err } }
			}
			for _, obj := range next.objects { if err := handler(ResourceEvent{watch.Added,obj.DeepCopy()}); err != nil { return err } }
			known, rv = next, version
		}
		wo := opts; wo.Continue = ""; wo.Limit = 0; wo.ResourceVersion = rv; wo.ResourceVersionMatch = ""; wo.AllowWatchBookmarks = true
		seconds := int64(300); wo.TimeoutSeconds = &seconds
		stream, err := ri.Watch(ctx,wo)
		if err != nil {
			if apierrors.IsGone(err) || apierrors.IsResourceExpired(err) { rv = "" } else if !retryable(err) { return err }
			state("Reconnecting (stale)")
			if err := retryWait(ctx,err,backoff); err != nil { return err }; backoff = min(backoff*2,5*time.Second); continue
		}
		state("Live")
		started := time.Now()
		expired, err := consumeResourceStream(ctx,stream,known,&rv,handler)
		stream.Stop()
		if ctx.Err() != nil { return ctx.Err() }
		if err != nil && !retryable(err) { return err }
		if expired { rv = "" }
		if time.Since(started)>5*time.Second { backoff = 100*time.Millisecond }
		state("Reconnecting (stale)")
		if err := retryWait(ctx,err,backoff); err != nil { return err }; backoff = min(backoff*2,5*time.Second)
	}
}

func listSnapshot(ctx context.Context, ri dynamic.ResourceInterface, opts metav1.ListOptions) (*watchCache,string,error) {
	out := newWatchCache()
	opts.ResourceVersion = ""; opts.ResourceVersionMatch = ""; opts.Continue = ""; opts.Watch = false
	if opts.Limit <= 0 || opts.Limit > 500 { opts.Limit = 500 }
	version := ""
	for {
		requestCtx,cancel := context.WithTimeout(ctx,30*time.Second)
		list,err := ri.List(requestCtx,opts); cancel()
		if err != nil { return nil,"",err }
		if version == "" { version = list.GetResourceVersion() } else if list.GetResourceVersion() != version { return nil,"",errors.New("paginated list changed resourceVersion") }
		for i := range list.Items { if err := out.put(list.Items[i].DeepCopy()); err != nil { return nil,"",err } }
		if list.GetContinue() == "" { return out,version,nil }
		if opts.Continue == list.GetContinue() { return nil,"",errors.New("non-progressing continuation token") }
		opts.Continue = list.GetContinue()
	}
}

func consumeResourceStream(ctx context.Context,w watch.Interface,known *watchCache,rv *string,handler EventHandler) (bool,error) {
	for { select {
	case <-ctx.Done(): return false,ctx.Err()
	case event,ok := <-w.ResultChan():
		if !ok { return false,nil }
		if event.Type == watch.Error {
			err := apierrors.FromObject(event.Object)
			if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) { return true,nil }
			return false,err
		}
		obj,ok := event.Object.(*unstructured.Unstructured)
		if !ok { return false,fmt.Errorf("unexpected watch object %T",event.Object) }
		if event.Type == watch.Bookmark { if obj.GetResourceVersion() != "" { *rv = obj.GetResourceVersion() }; continue }
		if event.Type != watch.Added && event.Type != watch.Modified && event.Type != watch.Deleted { return false,fmt.Errorf("unknown watch event %q",event.Type) }
		if event.Type == watch.Deleted { known.remove(obj) } else { if err := known.put(obj.DeepCopy()); err != nil { return false,err } }
		if err := handler(ResourceEvent{event.Type,obj.DeepCopy()}); err != nil { return false,err }
		if obj.GetResourceVersion() != "" { *rv = obj.GetResourceVersion() }
	} }
}
func retryable(err error) bool {
	if err == nil { return true }
	if errors.Is(err,context.Canceled) || errors.Is(err,ErrWorkingSet) { return false }
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || apierrors.IsNotFound(err) { return false }
	var netErr net.Error
	return apierrors.IsTooManyRequests(err) || apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsServiceUnavailable(err) || apierrors.IsInternalError(err) || errors.As(err,&netErr)
}
func retryWait(ctx context.Context,err error,delay time.Duration) error {
	if seconds,ok := apierrors.SuggestsClientDelay(err); ok { delay = max(delay,time.Duration(seconds)*time.Second) }
	delay += time.Duration(rand.Int63n(int64(max(delay/4,time.Millisecond))))
	timer := time.NewTimer(delay); defer timer.Stop()
	select { case <-ctx.Done(): return ctx.Err(); case <-timer.C: return nil }
}
func resourceInterface(client dynamic.Interface,gvr schema.GroupVersionResource,namespace string) dynamic.ResourceInterface {
	r := client.Resource(gvr); if namespace == "" { return r }; return r.Namespace(namespace)
}
