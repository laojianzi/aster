package kube

import (
 "context"
 "encoding/json"
 "errors"
 "net/http"
 "net/http/httptest"
 "sync/atomic"
 "testing"
 "time"
 "github.com/laojianzi/aster/internal/execsession"
 "github.com/laojianzi/aster/internal/resource"
 corev1 "k8s.io/api/core/v1"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/runtime/schema"
 "k8s.io/client-go/rest"
)
func execTarget()resource.Identity{return resource.Identity{SessionID:"unit",GVR:schema.GroupVersionResource{Version:"v1",Resource:"pods"},Namespace:"team",Name:"pod",UID:"one"}}
func runningPod()corev1.Pod{return corev1.Pod{TypeMeta:metav1.TypeMeta{APIVersion:"v1",Kind:"Pod"},ObjectMeta:metav1.ObjectMeta{Name:"pod",Namespace:"team",UID:"one"},Status:corev1.PodStatus{Phase:corev1.PodRunning,ContainerStatuses:[]corev1.ContainerStatus{{Name:"http",State:corev1.ContainerState{Running:&corev1.ContainerStateRunning{}}}}}}}
func TestCommandRejectsInvalidInputBeforeNetwork(t *testing.T){
 b:=&Backend{};out,_:=execsession.NewOutput(100,nil)
 for _,cmd:=range []execsession.Command{{Container:"http"},{Argv:[]string{"id"}},{Container:"http",Argv:[]string{"id"},Timeout:time.Hour}} {
  r,err:=b.RunPodCommand(context.Background(),execTarget(),cmd,out);if err==nil || r.State!=execsession.Rejected {t.Fatal(r,err)}
 }
}
func TestExecDenialDoesNotFallBackOrExposeCommandArguments(t *testing.T){
 var execCalls atomic.Int32
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  w.Header().Set("Content-Type","application/json")
  if r.URL.Path=="/api/v1/namespaces/team/pods/pod"{_ = json.NewEncoder(w).Encode(runningPod());return}
  execCalls.Add(1)
  if r.Method!=http.MethodGet || r.URL.Query().Get("container")!="http" || r.URL.Query().Get("stdin")=="true" || r.URL.Query().Get("tty")=="true" {t.Error("wrong exec request")}
  w.WriteHeader(http.StatusForbidden)
  _=json.NewEncoder(w).Encode(metav1.Status{TypeMeta:metav1.TypeMeta{APIVersion:"v1",Kind:"Status"},Status:"Failure",Reason:metav1.StatusReasonForbidden,Code:403,Message:"denied synthetic-secret-argument"})
 }));defer server.Close()
 b,err:=New(&rest.Config{Host:server.URL});if err!=nil{t.Fatal(err)}
 out,_:=execsession.NewOutput(100,nil)
 result,err:=b.RunPodCommand(context.Background(),execTarget(),execsession.Command{Container:"http",Argv:[]string{"echo","synthetic-secret-argument"}},out)
 if err==nil || result.State!=execsession.Rejected || execCalls.Load()!=1 || result.ExitKnown {t.Fatalf("%+v %v calls=%d",result,err,execCalls.Load())}
 if got:=err.Error();got!=result.String(){t.Fatalf("command or server detail escaped sanitization: %q",got)}
}
func TestExecReplacedPodAndStoppedContainerNeverUpgrade(t *testing.T){
 for _,replace:=range []bool{true,false}{t.Run(map[bool]string{true:"replaced",false:"stopped"}[replace],func(t *testing.T){
  var requests atomic.Int32
  server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){requests.Add(1);pod:=runningPod();if replace{pod.UID="two"}else{pod.Status.ContainerStatuses=nil};w.Header().Set("Content-Type","application/json");_=json.NewEncoder(w).Encode(pod)}));defer server.Close()
  b,err:=New(&rest.Config{Host:server.URL});if err!=nil{t.Fatal(err)};out,_:=execsession.NewOutput(100,nil)
  result,err:=b.RunPodCommand(context.Background(),execTarget(),execsession.Command{Container:"http",Argv:[]string{"id"}},out)
  if err==nil || result.State!=execsession.Rejected || requests.Load()!=1 {t.Fatal(result,err,requests.Load())}
 })}
}
type testExit int
func(e testExit)Error()string{return "command exited"}
func(e testExit)ExitStatus()int{return int(e)}
func TestRemoteExitIsSeparateFromDisconnection(t *testing.T){
 ctx,cancel:=context.WithCancel(context.Background());cancel()
 for _,test:=range []struct{ctx context.Context;limited bool;err error;state execsession.State;known bool;code int}{
  {context.Background(),false,nil,execsession.Succeeded,true,0},
  {context.Background(),false,testExit(7),execsession.Failed,true,7},
  {context.Background(),false,errors.New("EOF"),execsession.Unknown,false,-1},
  {ctx,false,context.Canceled,execsession.Interrupted,false,-1},
  {ctx,true,context.Canceled,execsession.OutputLimited,false,-1},
 }{
  got,_:=commandResult(test.ctx,test.limited,true,test.err)
  if got.State!=test.state||got.ExitKnown!=test.known||got.ExitCode!=test.code{t.Fatalf("%+v",got)}
 }
}
func TestExecRejectsRedirectAndMarksUpgrade(t *testing.T){
 for _,status:=range []int{302,101}{
  var upgraded atomic.Bool
  tr:=execHandshake{base:sessionHandshake{parent:context.Background(),base:roundTripFunc(func(*http.Request)(*http.Response,error){return &http.Response{StatusCode:status},nil})},upgraded:&upgraded}
  req,_:=http.NewRequest(http.MethodGet,"https://example.invalid/exec",nil)
  _,err:=tr.RoundTrip(req)
  if status==302 && (err==nil||upgraded.Load()){t.Fatal("redirect permitted")}
  if status==101 && (err!=nil||!upgraded.Load()){t.Fatal("upgrade unrecorded")}
 }
}
func TestOutputOverflowCancelsProtocolContext(t *testing.T){
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 out,_:=execsession.NewOutput(2,nil)
 writer:=commandWriter{out.Stdout(),cancel}
 if _,err:=writer.Write([]byte("123"));!errors.Is(err,execsession.ErrOutputLimit)||ctx.Err()==nil{t.Fatal("overflow did not stop stream")}
}
