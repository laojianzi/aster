package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
)
func recoveryObject(name,uid,rv string)map[string]interface{}{return map[string]interface{}{"apiVersion":"v1","kind":"ConfigMap","metadata":map[string]interface{}{"name":name,"namespace":"team","uid":uid,"resourceVersion":rv}}}
func sendList(w http.ResponseWriter,rv string,items ...map[string]interface{}){w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]interface{}{"apiVersion":"v1","kind":"ConfigMapList","metadata":map[string]interface{}{"resourceVersion":rv},"items":items})}
func sendEvent(w http.ResponseWriter,kind string,obj interface{}){w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]interface{}{"type":kind,"object":obj});if f,ok:=w.(http.Flusher);ok{f.Flush()}}
func TestExpiredWatchRemovesGhostRowsAfterRelist(t *testing.T){
	var lists atomic.Int32
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if r.URL.Query().Get("watch")=="true"{sendEvent(w,"ERROR",&metav1.Status{TypeMeta:metav1.TypeMeta{APIVersion:"v1",Kind:"Status"},Status:"Failure",Code:410,Reason:metav1.StatusReasonExpired});return}
		if lists.Add(1)==1{sendList(w,"10",recoveryObject("same","old","10"),recoveryObject("gone","gone","10"))}else{sendList(w,"20",recoveryObject("same","new","20"))}
	}));defer server.Close()
	backend,err:=New(&rest.Config{Host:server.URL});if err!=nil{t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();stop:=errors.New("observed reconciled snapshot");seen:=map[string]bool{}
	err=backend.WatchResource(ctx,schema.GroupVersionResource{Version:"v1",Resource:"configmaps"},"team",metav1.ListOptions{},func(e ResourceEvent)error{uid:=string(e.Object.GetUID());if e.Type==watch.Deleted{delete(seen,uid)}else{seen[uid]=true};if uid=="new"{return stop};return nil})
	if !errors.Is(err,stop)||len(seen)!=1||!seen["new"]{t.Fatalf("error=%v state=%v",err,seen)}
}
func TestWatchReconnectResumesFromBookmark(t *testing.T){
	var lists,streams atomic.Int32;var resumed atomic.Bool
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		if r.URL.Query().Get("watch")!="true"{lists.Add(1);sendList(w,"10",recoveryObject("demo","uid","10"));return}
		if streams.Add(1)==1{sendEvent(w,"BOOKMARK",recoveryObject("demo","uid","11"));return}
		resumed.Store(r.URL.Query().Get("resourceVersion")=="11");sendEvent(w,"MODIFIED",recoveryObject("demo","uid","12"))
	}));defer server.Close()
	backend,err:=New(&rest.Config{Host:server.URL});if err!=nil{t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel();stop:=errors.New("done")
	err=backend.WatchResource(ctx,schema.GroupVersionResource{Version:"v1",Resource:"configmaps"},"team",metav1.ListOptions{},func(e ResourceEvent)error{if e.Object.GetResourceVersion()=="12"{return stop};return nil})
	if !errors.Is(err,stop)||!resumed.Load()||lists.Load()!=1{t.Fatalf("err=%v resumed=%v lists=%d",err,resumed.Load(),lists.Load())}
}
func TestSnapshotPaginationAndCancellation(t *testing.T){
	var requests atomic.Int32
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){n:=requests.Add(1);w.Header().Set("Content-Type","application/json");token:="next";if n>1{token="";if r.URL.Query().Get("continue")!="next"{t.Error("continuation not sent")}};_ = json.NewEncoder(w).Encode(map[string]interface{}{"apiVersion":"v1","kind":"ConfigMapList","metadata":map[string]interface{}{"resourceVersion":"5","continue":token},"items":[]interface{}{recoveryObject(fmt.Sprint(n),fmt.Sprint(n),"5")}})}));defer server.Close()
	backend,err:=New(&rest.Config{Host:server.URL});if err!=nil{t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second);defer cancel()
	snapshot,rv,err:=listSnapshot(ctx,resourceInterface(backend.dynamic,schema.GroupVersionResource{Version:"v1",Resource:"configmaps"},"team"),metav1.ListOptions{})
	if err!=nil||len(snapshot.objects)!=2||rv!="5"{t.Fatalf("%v %s %v",snapshot,rv,err)}
}

var _ = unstructured.Unstructured{}
