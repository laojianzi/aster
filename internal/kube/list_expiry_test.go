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
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/runtime/schema"
 "k8s.io/client-go/rest"
)
func TestExpiredListPageRestartsWithoutPublishingPartialSnapshot(t *testing.T){
 var requests atomic.Int32
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  n:=requests.Add(1);w.Header().Set("Content-Type","application/json")
  switch n{case 1:_ = json.NewEncoder(w).Encode(map[string]interface{}{"apiVersion":"v1","kind":"ConfigMapList","metadata":map[string]interface{}{"resourceVersion":"1","continue":"expired-page"},"items":[]interface{}{recoveryObject("partial","partial","1")}})
  case 2:if r.URL.Query().Get("continue")!="expired-page"{t.Error("missing continuation")};w.WriteHeader(http.StatusGone);_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta:metav1.TypeMeta{APIVersion:"v1",Kind:"Status"},Status:"Failure",Code:410,Reason:metav1.StatusReasonExpired})
  default:if r.URL.Query().Get("continue")!=""{t.Error("stale continuation reused")};sendList(w,"2",recoveryObject("complete","complete","2"))}
 }));defer server.Close()
 backend,err:=New(&rest.Config{Host:server.URL});if err!=nil{t.Fatal(err)};ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();stop:=errors.New("complete snapshot received");var seen []string
 err=backend.WatchResource(ctx,schema.GroupVersionResource{Version:"v1",Resource:"configmaps"},"team",metav1.ListOptions{},func(e ResourceEvent)error{seen=append(seen,string(e.Object.GetUID()));return stop})
 if !errors.Is(err,stop)||len(seen)!=1||seen[0]!="complete"||requests.Load()!=3{t.Fatalf("err=%v seen=%v requests=%d",err,seen,requests.Load())}
}
