package uiworkbench

import (
 "context"
 "crypto/rand"
 "encoding/hex"
 "errors"
 "fmt"
 "strings"
 "sync/atomic"
 "time"
 "github.com/laojianzi/aster/internal/kube"
 "github.com/laojianzi/aster/internal/kubeconfig"
 "github.com/laojianzi/aster/internal/manifest"
 "github.com/laojianzi/aster/internal/operation"
 "github.com/laojianzi/aster/internal/resource"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
 "k8s.io/apimachinery/pkg/runtime/schema"
)

func(w *Workbench)loadContexts(){
 path:=w.path;w.disconnect();epoch:=w.contextEpoch
 w.run(func(ctx context.Context){items,current,err:=kubeconfig.Contexts(path);w.emit(func(){if epoch!=w.contextEpoch{return};if err!=nil{w.errText=err.Error();return};w.contexts=nil;for _,item:=range items{w.contexts=append(w.contexts,item.Name)};w.currentContext=current;w.trustedFingerprint="";w.trustRequired=false;if len(items)==0{w.status="No kubeconfig contexts";return};if w.currentContext==""{w.currentContext=items[0].Name};w.connect()})})
}
func(w *Workbench)disconnect(){
 w.contextEpoch++;w.scopeEpoch++;if w.connectionCancel!=nil{w.connectionCancel();w.connectionCancel=nil};w.activeContext="";w.activeNamespace="";w.notice="";w.kinds=catalog();if w.scopeCancel!=nil{w.scopeCancel();w.scopeCancel=nil};w.clearDetail();w.backend=nil;w.ops=nil;w.rows=nil;w.total=0;w.selected=-1;w.store=nil;w.status="Disconnected";w.errText=""
}
func(w *Workbench)connect(){
 w.disconnect();w.status="Connecting";epoch:=w.contextEpoch
 connectionCtx,connectionCancel:=context.WithCancel(w.ctx);w.connectionCtx=connectionCtx;w.connectionCancel=connectionCancel
 opts:=kubeconfig.Options{Path:w.path,Context:w.currentContext,Namespace:w.namespace,TrustToken:w.trustedFingerprint}
 w.run(func(ctx context.Context){
  conn,err:=kubeconfig.Load(opts);var backend *kube.Backend;if err==nil{backend,err=kube.New(conn.Config)}
  var sessionBytes[16]byte;if err==nil{_,err=rand.Read(sessionBytes[:])}
  w.emit(func(){
   if epoch!=w.contextEpoch{return}
   if err!=nil{w.status="Connection failed";w.errText=err.Error();var trust *kubeconfig.TrustRequiredError;w.trustRequired=errors.As(err,&trust);w.pendingTrustFingerprint="";if w.trustRequired{w.pendingTrustFingerprint=trust.Fingerprint};return}
   w.backend=backend;w.activeContext=conn.ContextName;w.currentContext=conn.ContextName;w.namespace=conn.Namespace;w.sessionID=hex.EncodeToString(sessionBytes[:]);w.ops=operation.NewService(backend,w.sessionID);w.trustRequired=false;w.errText="";w.startScope()
   w.run(func(ctx context.Context){kinds,warnings,err:=backend.Discover(connectionCtx);w.emit(func(){if epoch!=w.contextEpoch{return};if err!=nil{w.notice="Discovery unavailable; built-in resources remain accessible";return};if len(kinds)>0{w.kinds=kinds};w.notice=fmt.Sprintf("%d resource types · %d unavailable API group(s)",len(kinds),len(warnings))})})
  })
 })
}
func(w *Workbench)chooseKind(kind kube.ResourceKind){w.currentKind=kind;w.kindChoice=kind.Label();w.startScope()}
func(w *Workbench)queryChanged(){if w.store!=nil{w.store.setQuery(w.filter,w.sortBy)}}
func(w *Workbench)startScope(){
 w.scopeEpoch++;epoch:=w.scopeEpoch;if w.scopeCancel!=nil{w.scopeCancel()};w.clearDetail();w.rows=nil;w.total=0;w.selected=-1;w.errText=""
 if w.backend==nil{w.status="Not connected";return}
 parent:=w.operationContext();ctx,cancel:=context.WithCancel(parent);w.scopeCancel=cancel
 kind,backend:=w.currentKind,w.backend;ns:=strings.TrimSpace(w.namespace);if !kind.Namespaced||ns=="*"{ns=""}
 w.activeNamespace=ns;if !kind.Namespaced{w.activeNamespace="cluster-scoped"}else if ns==""{w.activeNamespace="all namespaces"}
 opts:=metav1.ListOptions{LabelSelector:w.labelSelector};store:=newRowStore();store.setQuery(w.filter,w.sortBy);w.store=store;w.status="Synchronizing"
 w.run(func(context.Context){err:=backend.WatchWithStatus(ctx,kind.GVR,ns,opts,func(e kube.ResourceEvent)error{store.event(e);return nil},func(status string){store.setStatus(status,"")});if err!=nil&&ctx.Err()==nil{store.setStatus("Unavailable",err.Error())}})
 w.run(func(context.Context){
  ticker:=time.NewTicker(50*time.Millisecond);defer ticker.Stop();var pending atomic.Bool
  for{select{case<-ctx.Done():return;case<-ticker.C:
   if !pending.CompareAndSwap(false,true){continue};snapshot,changed:=store.snapshot();if !changed{pending.Store(false);continue}
   w.emit(func(){defer pending.Store(false);if epoch!=w.scopeEpoch||ctx.Err()!=nil{return};if snapshot.Query!=w.filter||snapshot.Sort!=w.sortBy{store.setQuery(w.filter,w.sortBy);return};selectedKey:="";if w.selected>=0&&w.selected<len(w.rows){selectedKey=w.rows[w.selected].key()};w.rows=snapshot.Rows;w.total=snapshot.Total;w.status=snapshot.Status;w.errText=snapshot.Error;w.selected=-1;for i,r:=range w.rows{if r.key()==selectedKey{w.selected=i;break}}})
  }}
 })
}
func(w *Workbench)clearDetail(){
 w.detailEpoch++;w.draftRevision++;w.eventsRevision++;w.eventsText="";w.stopLogs();w.logRows=nil;w.logDropped=0;w.logStatus="";w.detail=nil;w.plan=nil;w.diff="";w.editor="";w.detailText="";w.confirmation="";w.detailMessage="";w.preparing=false;w.creating=false
}
func(w *Workbench)openResource(row resourceRow){
 if w.backend==nil{return};w.clearDetail();epoch,scope:=w.detailEpoch,w.scopeEpoch;backend,kind:=w.backend,w.currentKind;parent:=w.operationContext()
 w.run(func(context.Context){ctx,cancel:=context.WithTimeout(parent,20*time.Second);defer cancel();obj,err:=backend.GetObject(ctx,kind.GVR,row.Namespace,row.Name);text:="";if err==nil&&string(obj.GetUID())!=row.UID{err=operation.ErrConflict};if err==nil{text,err=manifest.Display(obj,false)}
  w.emit(func(){if epoch!=w.detailEpoch||scope!=w.scopeEpoch{return};if err!=nil{w.errText=err.Error();return};w.detail=obj;w.detailKind=kind;w.detailText=text;w.editor=text;w.detailMode="YAML";w.containers=nil;for _,field:=range []string{"containers","initContainers","ephemeralContainers"}{items,_,_:=unstructured.NestedSlice(obj.Object,"spec",field);for _,value:=range items{if m,ok:=value.(map[string]interface{});ok{if name,ok:=m["name"].(string);ok{w.containers=append(w.containers,name)}}}};w.container="";if len(w.containers)>0{w.container=w.containers[0]};w.replicas="1";if n,ok,_:=unstructured.NestedInt64(obj.Object,"spec","replicas");ok{w.replicas=fmt.Sprint(n)}})
 })
}
func(w *Workbench)target()resource.Identity{return resource.Identity{SessionID:w.sessionID,GVR:w.detailKind.GVR,Namespace:w.detail.GetNamespace(),Name:w.detail.GetName(),UID:w.detail.GetUID()}}
func(w *Workbench)prepare(kind string){
 if w.detail==nil||w.ops==nil||w.preparing{return}
 epoch,revision:=w.detailEpoch,w.draftRevision;target,service:=w.target(),w.ops;text:=[]byte(w.editor);replicas:=w.replicas
 if kind=="create"{obj,err:=manifest.Decode(text);if err!=nil{w.detailMessage=err.Error();return};target.Name=obj.GetName()}
 parent:=w.operationContext();w.preparing=true;w.plan=nil;w.confirmation="";w.detailMessage="Performing server-side dry-run…"
 w.run(func(context.Context){
  ctx,cancel:=context.WithTimeout(parent,30*time.Second);defer cancel();var plan *operation.Prepared;var err error
  switch kind{case "create":plan,err=service.PrepareCreate(ctx,target,text);case "edit":plan,err=service.PrepareEdit(ctx,target,text);case "delete":plan,err=service.PrepareDelete(ctx,target);case "scale":var n int64;n,err=parseReplicaCount(replicas);if err==nil{plan,err=service.PrepareScale(ctx,target,n)};case "restart":plan,err=service.PrepareRestart(ctx,target);default:err=errors.New("unknown operation")}
  diff:="";if err==nil{if target.GVR.Resource=="secrets"{diff="Delete Secret "+target.Namespace+"/"+target.Name+"\nSecret payload is not included in the preview."}else{before,after:=plan.Preview();var changes []manifest.Change;changes,err=manifest.Compare(before,after);if err==nil{diff=manifest.Summary(changes);if diff==""{diff="No field changes after server-side validation."}}}}
  w.emit(func(){if epoch!=w.detailEpoch{return};w.preparing=false;if revision!=w.draftRevision{w.detailMessage="Draft changed; prepare a new preview.";return};if err!=nil{w.detailMessage=err.Error();return};w.plan=plan;w.diff=diff;w.detailMode="Diff";w.detailMessage="Review "+kind+" for "+target.Namespace+"/"+target.Name+" · expires "+plan.Expires().Local().Format("15:04:05")})
 })
}
func(w *Workbench)execute(){
 if w.plan==nil||w.ops==nil||w.pendingWrites>0{return};plan,service,epoch:=w.plan,w.ops,w.detailEpoch;contextName:=w.activeContext;target:=plan.Target();if w.confirmation!=target.Name{return}
 w.plan=nil;w.pendingWrites++;w.detailMessage="Submitting reviewed change…";parent:=w.operationContext()
 w.run(func(context.Context){ctx,cancel:=context.WithTimeout(parent,30*time.Second);defer cancel();result,err:=service.Execute(ctx,plan)
  w.emit(func(){w.pendingWrites--;line:=fmt.Sprintf("%s [%s] %s %s/%s · %s",plan.ID()[:12],contextName,plan.Kind(),target.Namespace,target.Name,result.State);w.history=append(w.history,line);if len(w.history)>100{w.history=append([]string(nil),w.history[len(w.history)-100:]...)};if epoch!=w.detailEpoch{return};if err!=nil{w.detailMessage=result.State+": "+err.Error()}else{w.detailMessage="API accepted the change. Workload readiness is observed separately in the live resource list.";if w.creating&&result.Object!=nil{w.creating=false;w.detail=result.Object.DeepCopy();if text,displayErr:=manifest.Display(w.detail,false);displayErr==nil{w.detailText=text;w.editor=text};w.detailMode="YAML"}};w.confirmation=""})
 })
}
func(w *Workbench)loadEvents(){
 if w.detail==nil||w.backend==nil{return};w.stopLogs();epoch:=w.detailEpoch;w.eventsRevision++;revision:=w.eventsRevision;backend,target:=w.backend,w.target();w.detailMode="Events";w.eventsText="Loading events…";parent:=w.operationContext()
 w.run(func(context.Context){ctx,cancel:=context.WithTimeout(parent,20*time.Second);defer cancel();items,_,err:=backend.ListObjects(ctx,schema.GroupVersionResource{Version:"v1",Resource:"events"},target.Namespace,metav1.ListOptions{FieldSelector:"involvedObject.uid="+string(target.UID),Limit:200});var lines []string;for _,item:=range items{reason,_,_:=unstructured.NestedString(item.Object,"reason");message,_,_:=unstructured.NestedString(item.Object,"message");lines=append(lines,reason+": "+message)};w.emit(func(){if epoch!=w.detailEpoch||revision!=w.eventsRevision{return};if err!=nil{w.eventsText="Events unavailable: "+err.Error()}else if len(lines)==0{w.eventsText="No retained events for this resource UID."}else{w.eventsText=strings.Join(lines,"\n\n")}})})
}
