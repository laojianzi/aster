package operation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type mutationFake struct{mu sync.Mutex;object *unstructured.Unstructured;dryRuns,writes int;writeErr error}
func(f *mutationFake)GetObject(context.Context,schema.GroupVersionResource,string,string)(*unstructured.Unstructured,error){f.mu.Lock();defer f.mu.Unlock();return f.object.DeepCopy(),nil}
func(f *mutationFake)PatchObject(_ context.Context,_ schema.GroupVersionResource,_,_ string,_ types.PatchType,_ []byte,o metav1.PatchOptions)(*unstructured.Unstructured,error){f.mu.Lock();defer f.mu.Unlock();if len(o.DryRun)>0{f.dryRuns++;return f.object.DeepCopy(),nil};f.writes++;return f.object.DeepCopy(),f.writeErr}
func(f *mutationFake)DeleteObject(_ context.Context,_ schema.GroupVersionResource,_,_ string,o metav1.DeleteOptions)error{f.mu.Lock();defer f.mu.Unlock();if o.Preconditions==nil||o.Preconditions.UID==nil||o.Preconditions.ResourceVersion==nil{return errors.New("missing delete preconditions")};if len(o.DryRun)>0{f.dryRuns++;return nil};f.writes++;return f.writeErr}
func mutationFixture()(*mutationFake,resource.Identity){
	o:=&unstructured.Unstructured{Object:map[string]interface{}{"apiVersion":"apps/v1","kind":"Deployment","metadata":map[string]interface{}{"name":"demo","namespace":"team","uid":"original","resourceVersion":"10"},"spec":map[string]interface{}{"replicas":int64(1)}}}
	return &mutationFake{object:o},resource.Identity{SessionID:"session-a",GVR:schema.GroupVersionResource{Group:"apps",Version:"v1",Resource:"deployments"},Namespace:"team",Name:"demo",UID:"original"}
}
func TestPrepareDoesNotWriteAndPlanIsSingleUse(t *testing.T){
	f,target:=mutationFixture();s:=NewService(f,"session-a");p,err:=s.PrepareScale(context.Background(),target,3);if err!=nil{t.Fatal(err)}
	if f.writes!=0||f.dryRuns!=1{t.Fatal("prepare persisted a change")}
	var wg sync.WaitGroup;var mu sync.Mutex;success:=0
	for i:=0;i<12;i++{wg.Add(1);go func(){defer wg.Done();_,err:=s.Execute(context.Background(),p);if err==nil{mu.Lock();success++;mu.Unlock()}}()};wg.Wait()
	if success!=1||f.writes!=1{t.Fatalf("success=%d writes=%d",success,f.writes)}
}
func TestPlanCannotCrossSessionsAndPreviewOwnsBytes(t *testing.T){
	f,target:=mutationFixture();s:=NewService(f,"session-a");p,err:=s.PrepareDelete(context.Background(),target);if err!=nil{t.Fatal(err)}
	before,_:=p.Preview();before[0]='X';again,_:=p.Preview();if again[0]=='X'{t.Fatal("preview aliases plan")}
	other:=NewService(f,"session-b");if _,err:=other.Execute(context.Background(),p);err==nil{t.Fatal("cross-session plan accepted")}
	if f.writes!=0{t.Fatal("cross-session write")}
}
func TestChangedResourceAndExpiredPlansAreRejected(t *testing.T){
	f,target:=mutationFixture();s:=NewService(f,"session-a");p,err:=s.PrepareScale(context.Background(),target,3);if err!=nil{t.Fatal(err)}
	f.object.SetResourceVersion("11")
	if _,err:=s.Execute(context.Background(),p);!errors.Is(err,ErrConflict){t.Fatal(err)}
	p,err=s.PrepareScale(context.Background(),target,3);if err!=nil{t.Fatal(err)}
	s.now=func()time.Time{return p.Expires().Add(time.Second)}
	if _,err:=s.Execute(context.Background(),p);err==nil{t.Fatal("expired plan accepted")}
	if f.writes!=0{t.Fatal("stale plan wrote")}
}
func TestUnknownResultIsNotRetried(t *testing.T){
	f,target:=mutationFixture();s:=NewService(f,"session-a");p,err:=s.PrepareDelete(context.Background(),target);if err!=nil{t.Fatal(err)}
	f.writeErr=context.DeadlineExceeded
	r,err:=s.Execute(context.Background(),p);if err==nil||r.State!="Unknown"{t.Fatalf("%v %v",r,err)}
	_,_=s.Execute(context.Background(),p);if f.writes!=1{t.Fatal("unknown write was replayed")}
}
