package kube

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)
func watchObject(name,uid,rv string) *unstructured.Unstructured { o := &unstructured.Unstructured{}; o.SetName(name); o.SetUID(types.UID(uid)); o.SetResourceVersion(rv); return o }
func TestOldDeleteDoesNotRemoveRecreatedObject(t *testing.T) {
	s := newWatchCache(); old := watchObject("pod","old","1"); newer := watchObject("pod","new","2")
	if err := s.put(newer); err != nil { t.Fatal(err) }; s.remove(old)
	if s.objects["/pod"] == nil { t.Fatal("deleted newer UID") }
}
func TestExpiredStreamRequestsRelist(t *testing.T) {
	stream := watch.NewRaceFreeFake(); defer stream.Stop()
	stream.Error(&metav1.Status{Status:"Failure",Code:410,Reason:metav1.StatusReasonExpired})
	rv := "123"
	expired,err := consumeResourceStream(context.Background(),stream,newWatchCache(),&rv,func(ResourceEvent)error{return nil})
	if !expired || err != nil { t.Fatalf("%v %v",expired,err) }
}
func TestForbiddenIsNotRetried(t *testing.T) {
	if retryable(apierrors.NewUnauthorized("expired identity")) { t.Fatal("authorization failure retried") }
	if !retryable(apierrors.NewTooManyRequests("back off",1)) { t.Fatal("429 not retried") }
}
func TestCancelledBackoff(t *testing.T) {
	ctx,cancel := context.WithCancel(context.Background()); cancel()
	if err := retryWait(ctx,nil,1000000000); !errors.Is(err,context.Canceled) { t.Fatal(err) }
}
func TestStreamAdvancesRVAndOwnsObjects(t *testing.T) {
	stream := watch.NewRaceFreeFake()
	obj := watchObject("pod","uid","2"); stream.Add(obj); stream.Stop()
	s := newWatchCache(); rv := "1"
	_,err := consumeResourceStream(context.Background(),stream,s,&rv,func(e ResourceEvent)error{e.Object.SetName("changed");return nil})
	if err != nil || rv != "2" || s.objects["/pod"].GetName() != "pod" { t.Fatalf("rv=%s err=%v",rv,err) }
}
