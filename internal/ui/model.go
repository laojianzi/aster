package uiworkbench

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

type resourceRow struct{UID,Name,Namespace,Created,Status,RV string}
func(r resourceRow)key()string{return r.Namespace+"/"+r.Name+"/"+r.UID}
func project(o *unstructured.Unstructured)resourceRow{
	status,_,_:=unstructured.NestedString(o.Object,"status","phase")
	if values,found,_:=unstructured.NestedSlice(o.Object,"status","containerStatuses");found{for _,value:=range values{m,ok:=value.(map[string]interface{});if !ok{continue};if reason,ok,_:=unstructured.NestedString(m,"state","waiting","reason");ok{status=reason;break}}}
	if status==""{if ready,ok,_:=unstructured.NestedInt64(o.Object,"status","readyReplicas");ok{if desired,ok,_:=unstructured.NestedInt64(o.Object,"spec","replicas");ok{status=formatReplicas(ready,desired)}}}
	if status==""{status="—"}
	if len(status)>160{status=string([]rune(status)[:min(len([]rune(status)),120)])+"…"}
	created:="—";if !o.GetCreationTimestamp().IsZero(){created=o.GetCreationTimestamp().UTC().Format(time.RFC3339)}
	return resourceRow{UID:string(o.GetUID()),Name:o.GetName(),Namespace:o.GetNamespace(),Created:created,Status:status,RV:o.GetResourceVersion()}
}

type resourceSnapshot struct{Rows []resourceRow;Status,Error,Query,Sort string;Total int}
type rowStore struct{mu sync.Mutex;rows map[string]resourceRow;status,err,query,sortBy string;dirty bool}
func newRowStore()*rowStore{return &rowStore{rows:map[string]resourceRow{},status:"Synchronizing",sortBy:"Name",dirty:true}}
func(s *rowStore)event(e kube.ResourceEvent){
	r:=project(e.Object);key:=r.Namespace+"/"+r.Name
	s.mu.Lock();defer s.mu.Unlock()
	if e.Type==watch.Deleted{if old,ok:=s.rows[key];ok&&old.UID==r.UID{delete(s.rows,key)}}else{s.rows[key]=r};s.dirty=true
}
func(s *rowStore)setStatus(status,err string){s.mu.Lock();defer s.mu.Unlock();s.status=status;s.err=err;s.dirty=true}
func(s *rowStore)setQuery(query,sortBy string){s.mu.Lock();defer s.mu.Unlock();s.query=query;s.sortBy=sortBy;s.dirty=true}
func(s *rowStore)snapshot()(resourceSnapshot,bool){
	s.mu.Lock();if !s.dirty{s.mu.Unlock();return resourceSnapshot{},false};s.dirty=false
	out:=resourceSnapshot{Status:s.status,Error:s.err,Query:s.query,Sort:s.sortBy,Total:len(s.rows)}
	rows:=make([]resourceRow,0,len(s.rows));for _,r:=range s.rows{rows=append(rows,r)};s.mu.Unlock()
	query:=strings.ToLower(strings.TrimSpace(out.Query))
	for _,r:=range rows{if query==""||strings.Contains(strings.ToLower(r.Name+" "+r.Namespace+" "+r.Status),query){out.Rows=append(out.Rows,r)}}
	sort.Slice(out.Rows,func(i,j int)bool{a,b:=out.Rows[i],out.Rows[j];switch out.Sort{case "Namespace":if a.Namespace!=b.Namespace{return a.Namespace<b.Namespace};case "Status":if a.Status!=b.Status{return a.Status<b.Status}};if a.Name!=b.Name{return a.Name<b.Name};return a.key()<b.key()})
	return out,true
}
func catalog()[]kube.ResourceKind{return []kube.ResourceKind{
	{GVR:schema.GroupVersionResource{Version:"v1",Resource:"pods"},Kind:"Pod",Namespaced:true},
	{GVR:schema.GroupVersionResource{Group:"apps",Version:"v1",Resource:"deployments"},Kind:"Deployment",Namespaced:true},
	{GVR:schema.GroupVersionResource{Group:"apps",Version:"v1",Resource:"statefulsets"},Kind:"StatefulSet",Namespaced:true},
	{GVR:schema.GroupVersionResource{Group:"apps",Version:"v1",Resource:"daemonsets"},Kind:"DaemonSet",Namespaced:true},
	{GVR:schema.GroupVersionResource{Group:"batch",Version:"v1",Resource:"jobs"},Kind:"Job",Namespaced:true},
	{GVR:schema.GroupVersionResource{Group:"batch",Version:"v1",Resource:"cronjobs"},Kind:"CronJob",Namespaced:true},
	{GVR:schema.GroupVersionResource{Version:"v1",Resource:"services"},Kind:"Service",Namespaced:true},
	{GVR:schema.GroupVersionResource{Group:"networking.k8s.io",Version:"v1",Resource:"ingresses"},Kind:"Ingress",Namespaced:true},
	{GVR:schema.GroupVersionResource{Version:"v1",Resource:"configmaps"},Kind:"ConfigMap",Namespaced:true},
	{GVR:schema.GroupVersionResource{Version:"v1",Resource:"secrets"},Kind:"Secret",Namespaced:true},
	{GVR:schema.GroupVersionResource{Version:"v1",Resource:"persistentvolumeclaims"},Kind:"PersistentVolumeClaim",Namespaced:true},
	{GVR:schema.GroupVersionResource{Version:"v1",Resource:"nodes"},Kind:"Node"},
	{GVR:schema.GroupVersionResource{Version:"v1",Resource:"namespaces"},Kind:"Namespace"},
}}
