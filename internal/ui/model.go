package uiworkbench

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/laojianzi/aster/internal/kube"
 "github.com/laojianzi/aster/internal/health"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
)

type resourceRow struct{UID,Name,Namespace,Created,Status,RV string; Health health.State}
func(r resourceRow)key()string{return r.Namespace+"/"+r.Name+"/"+r.UID}
func project(o *unstructured.Unstructured)resourceRow{
	status,_,_:=unstructured.NestedString(o.Object,"status","phase")
	if values,found,_:=unstructured.NestedSlice(o.Object,"status","containerStatuses");found{for _,value:=range values{m,ok:=value.(map[string]interface{});if !ok{continue};if reason,ok,_:=unstructured.NestedString(m,"state","waiting","reason");ok{status=reason;break}}}
	if status==""{if ready,ok,_:=unstructured.NestedInt64(o.Object,"status","readyReplicas");ok{if desired,ok,_:=unstructured.NestedInt64(o.Object,"spec","replicas");ok{status=formatReplicas(ready,desired)}}}
	if status==""{status="—"}
	if len(status)>160{runes:=[]rune(status);status=string(runes[:min(len(runes),120)])+"…"}
	created:="—";stamp:=o.GetCreationTimestamp();if !stamp.IsZero(){created=stamp.UTC().Format(time.RFC3339)}
	return resourceRow{UID:string(o.GetUID()),Name:o.GetName(),Namespace:o.GetNamespace(),Created:created,Status:status,RV:o.GetResourceVersion(),Health:health.Assess(o).State}
}

type resourceSnapshot struct{Rows []resourceRow;Status,Error,Query,Sort string;Total int; Attention bool}
type rowStore struct{mu sync.Mutex;rows map[string]resourceRow;status,err,query,sortBy string;dirty bool; attention bool}
func newRowStore()*rowStore{return &rowStore{rows:map[string]resourceRow{},status:"Synchronizing",sortBy:"Name",dirty:true}}
func(s *rowStore)event(e kube.ResourceEvent){
	if e.Object==nil{return}
	r:=project(e.Object);key:=r.Namespace+"/"+r.Name
	s.mu.Lock();defer s.mu.Unlock()
	if e.Type==watch.Deleted{if old,ok:=s.rows[key];ok&&old.UID==r.UID{delete(s.rows,key)}}else{s.rows[key]=r};s.dirty=true
}
func(s *rowStore)setStatus(status,err string){s.mu.Lock();defer s.mu.Unlock();s.status=status;s.err=err;s.dirty=true}
func(s *rowStore)setQuery(query,sortBy string,attention bool){s.mu.Lock();defer s.mu.Unlock();s.query=query;s.sortBy=sortBy;s.attention=attention;s.dirty=true}
func(s *rowStore)snapshot()(resourceSnapshot,bool){
	s.mu.Lock();if !s.dirty{s.mu.Unlock();return resourceSnapshot{},false};s.dirty=false
	out:=resourceSnapshot{Status:s.status,Error:s.err,Query:s.query,Sort:s.sortBy,Total:len(s.rows),Attention:s.attention}
	rows:=make([]resourceRow,0,len(s.rows));for _,r:=range s.rows{rows=append(rows,r)};s.mu.Unlock()
	query:=strings.ToLower(strings.TrimSpace(out.Query))
	for _,r:=range rows{if out.Attention&&(r.Health==health.Ready||r.Health==health.Completed||r.Health==health.Unsupported){continue};if query==""||strings.Contains(strings.ToLower(r.Name+" "+r.Namespace+" "+r.Status+" "+string(r.Health)),query){out.Rows=append(out.Rows,r)}}
	sort.Slice(out.Rows,func(i,j int)bool{a,b:=out.Rows[i],out.Rows[j];switch out.Sort{case "Namespace":if a.Namespace!=b.Namespace{return a.Namespace<b.Namespace};case "Status":if out.Attention{if a.Health!=b.Health{return a.Health<b.Health}}else if a.Status!=b.Status{return a.Status<b.Status}};if a.Name!=b.Name{return a.Name<b.Name};return a.key()<b.key()})
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
	{GVR:schema.GroupVersionResource{Group:"apps",Version:"v1",Resource:"replicasets"},Kind:"ReplicaSet",Namespaced:true},
	{GVR:schema.GroupVersionResource{Group:"networking.k8s.io",Version:"v1",Resource:"networkpolicies"},Kind:"NetworkPolicy",Namespaced:true},
	{GVR:schema.GroupVersionResource{Group:"discovery.k8s.io",Version:"v1",Resource:"endpointslices"},Kind:"EndpointSlice",Namespaced:true},
	{GVR:schema.GroupVersionResource{Version:"v1",Resource:"persistentvolumes"},Kind:"PersistentVolume"},
	{GVR:schema.GroupVersionResource{Group:"storage.k8s.io",Version:"v1",Resource:"storageclasses"},Kind:"StorageClass"},
	{GVR:schema.GroupVersionResource{Version:"v1",Resource:"serviceaccounts"},Kind:"ServiceAccount",Namespaced:true},
	{GVR:schema.GroupVersionResource{Group:"rbac.authorization.k8s.io",Version:"v1",Resource:"roles"},Kind:"Role",Namespaced:true},
	{GVR:schema.GroupVersionResource{Group:"rbac.authorization.k8s.io",Version:"v1",Resource:"rolebindings"},Kind:"RoleBinding",Namespaced:true},
	{GVR:schema.GroupVersionResource{Group:"rbac.authorization.k8s.io",Version:"v1",Resource:"clusterroles"},Kind:"ClusterRole"},
	{GVR:schema.GroupVersionResource{Group:"rbac.authorization.k8s.io",Version:"v1",Resource:"clusterrolebindings"},Kind:"ClusterRoleBinding"},
	{GVR:schema.GroupVersionResource{Group:"apiextensions.k8s.io",Version:"v1",Resource:"customresourcedefinitions"},Kind:"CustomResourceDefinition"},
}}
