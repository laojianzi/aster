package operation

import (
 "context"
 "encoding/json"
 "errors"
 "testing"
 "github.com/laojianzi/aster/internal/manifest"
 "github.com/laojianzi/aster/internal/resource"
 apierrors "k8s.io/apimachinery/pkg/api/errors"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
 "k8s.io/apimachinery/pkg/runtime/schema"
)

func(f *mutationFake)CreateObject(_ context.Context,_ schema.GroupVersionResource,_ string,o *unstructured.Unstructured,opts metav1.CreateOptions)(*unstructured.Unstructured,error){f.mu.Lock();defer f.mu.Unlock();if len(opts.DryRun)>0{f.dryRuns++}else{f.writes++;if f.writeErr!=nil{return nil,f.writeErr}};return o.DeepCopy(),nil}
func createFixture()(resource.Identity,[]byte){return resource.Identity{SessionID:"session-a",GVR:schema.GroupVersionResource{Version:"v1",Resource:"configmaps"},Namespace:"team",Name:"example"},[]byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\n  namespace: team\ndata:\n  example: value\n")}
func TestCreateIsDryRunBoundAndCannotOverwriteExistingObject(t *testing.T){
 f,_:=mutationFixture();target,text:=createFixture();s:=NewService(f,target.SessionID);p,err:=s.PrepareCreate(context.Background(),target,text);if err!=nil{t.Fatal(err)}
 if f.writes!=0||f.dryRuns!=1{t.Fatal("create preview wrote")};before,after:=p.Preview();if string(before)!="null"||len(after)==0{t.Fatal("invalid create preview")};text[0]='X'
 f.writeErr=apierrors.NewAlreadyExists(schema.GroupResource{Resource:"configmaps"},target.Name);result,err:=s.Execute(context.Background(),p)
 if !apierrors.IsAlreadyExists(err)||result.State!="Conflict"{t.Fatalf("result=%v err=%v",result,err)}
 if _,err=s.Execute(context.Background(),p);err==nil||f.writes!=1{t.Fatal("create replayed")}
}
func TestCreateRejectsIdentityMismatchAndCrossSession(t *testing.T){
 f,_:=mutationFixture();target,text:=createFixture();s:=NewService(f,target.SessionID);other:=target;other.Namespace="another-team"
 if _,err:=s.PrepareCreate(context.Background(),other,text);err==nil{t.Fatal("namespace mismatch accepted")};other=target;other.SessionID="another-session"
 if _,err:=s.PrepareCreate(context.Background(),other,text);err==nil{t.Fatal("session mismatch accepted")};if f.dryRuns!=0||f.writes!=0{t.Fatal("invalid request reached backend")}
}
func TestCreateBindsPayloadCopiesAndCancelledExecuteDoesNotWrite(t *testing.T){
 f,_:=mutationFixture();target,text:=createFixture();s:=NewService(f,target.SessionID);p,err:=s.PrepareCreate(context.Background(),target,text);if err!=nil{t.Fatal(err)}
 _,after:=p.Preview();after[0]='X';_,again:=p.Preview();if again[0]=='X'{t.Fatal("preview alias")}
 ctx,cancel:=context.WithCancel(context.Background());cancel();if _,err=s.Execute(ctx,p);!errors.Is(err,context.Canceled)||f.writes!=0{t.Fatalf("cancelled execute: %v",err)}
 r,err:=s.Execute(context.Background(),p);if err!=nil||r.State!="Succeeded"||f.writes!=1{t.Fatalf("result=%v err=%v",r,err)}
}
func TestScaleDoesNotTreatCustomResourcesAsBuiltinDeployments(t *testing.T){
 f,target:=mutationFixture();s:=NewService(f,target.SessionID);target.GVR.Group="custom.example.com"
 if _,err:=s.PrepareScale(context.Background(),target,2);err==nil{t.Fatal("custom deployments treated as apps/v1")};if _,err:=s.PrepareRestart(context.Background(),target);err==nil{t.Fatal("custom deployments treated as apps/v1")}
}
func TestEditorPreservesHiddenLastAppliedAnnotation(t *testing.T){
 f,target:=mutationFixture();s:=NewService(f,target.SessionID);f.object.SetAnnotations(map[string]string{"kubectl.kubernetes.io/last-applied-configuration":"retained-in-write-only"})
 text,err:=manifest.Display(f.object,false);if err!=nil{t.Fatal(err)};p,err:=s.PrepareEdit(context.Background(),target,[]byte(text));if err!=nil{t.Fatal(err)}
 var steps []struct{Op string;Path string;Value json.RawMessage};if err=json.Unmarshal(p.patch,&steps);err!=nil{t.Fatal(err)}
 replacement:=&unstructured.Unstructured{};if err=replacement.UnmarshalJSON(steps[len(steps)-1].Value);err!=nil{t.Fatal(err)}
 if replacement.GetAnnotations()["kubectl.kubernetes.io/last-applied-configuration"]!="retained-in-write-only"{t.Fatal("hidden annotation silently deleted")}
}
