package operation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

type Client interface{
	GetObject(context.Context,schema.GroupVersionResource,string,string)(*unstructured.Unstructured,error)
	PatchObject(context.Context,schema.GroupVersionResource,string,string,types.PatchType,[]byte,metav1.PatchOptions)(*unstructured.Unstructured,error)
	DeleteObject(context.Context,schema.GroupVersionResource,string,string,metav1.DeleteOptions)error
}
var ErrConflict=errors.New("resource changed since it was reviewed; refresh and prepare a new plan")

type Service struct{ client Client; session string; now func()time.Time }
func NewService(client Client,session string)*Service{return &Service{client:client,session:session,now:time.Now}}

// Prepared has no exported mutable fields. It binds the exact previewed bytes,
// identity, UID, resourceVersion, issuer and expiry. A plan can be used only once.
type Prepared struct{
	issuer *Service
	id string
	target resource.Identity
	kind string
	rv string
	patch []byte
	before,after []byte
	expires time.Time
	used atomic.Bool
}
func(p *Prepared)ID()string{return p.id}
func(p *Prepared)Kind()string{return p.kind}
func(p *Prepared)Target()resource.Identity{return p.target}
func(p *Prepared)Expires()time.Time{return p.expires}
func(p *Prepared)Preview()([]byte,[]byte){return append([]byte(nil),p.before...),append([]byte(nil),p.after...)}

type Result struct{ID,State string;Object *unstructured.Unstructured}

func(s *Service)baseline(ctx context.Context,target resource.Identity)(*unstructured.Unstructured,error){
	if err:=target.Validate();err!=nil{return nil,err}
	if s.client==nil||target.SessionID!=s.session||target.UID==""{return nil,errors.New("invalid operation session or missing UID")}
	o,err:=s.client.GetObject(ctx,target.GVR,target.Namespace,target.Name);if err!=nil{return nil,err}
	if o.GetUID()!=target.UID{return nil,ErrConflict};return o,nil
}
type patchStep struct{Op string `json:"op"`;Path string `json:"path"`;Value interface{} `json:"value"`}
func tests(o *unstructured.Unstructured)[]patchStep{return []patchStep{{"test","/metadata/uid",string(o.GetUID())},{"test","/metadata/resourceVersion",o.GetResourceVersion()}}}

func(s *Service)PrepareEdit(ctx context.Context,target resource.Identity,text []byte)(*Prepared,error){
	desired,err:=manifest.Decode(text);if err!=nil{return nil,err}
	old,err:=s.baseline(ctx,target);if err!=nil{return nil,err}
	if old.GetKind()=="Secret"{return nil,errors.New("secret editing is disabled in the desktop editor")}
	if desired.GetName()!=old.GetName()||desired.GetNamespace()!=old.GetNamespace()||desired.GetKind()!=old.GetKind()||desired.GetAPIVersion()!=old.GetAPIVersion()||desired.GetUID()!=old.GetUID()||desired.GetResourceVersion()!=old.GetResourceVersion(){return nil,ErrConflict}
	desired.SetManagedFields(old.GetManagedFields())
	if status,ok:=old.Object["status"];ok{desired.Object["status"]=status}else{delete(desired.Object,"status")}
	steps:=append(tests(old),patchStep{"replace","",desired.Object})
	return s.prepare(ctx,target,"edit",old,steps)
}
func(s *Service)PrepareScale(ctx context.Context,target resource.Identity,replicas int64)(*Prepared,error){
	if replicas<0||replicas>100000{return nil,errors.New("replicas must be in [0,100000]")}
	switch target.GVR.Resource{case "deployments","statefulsets","replicasets","replicationcontrollers":default:return nil,errors.New("resource does not support scaling")}
	old,err:=s.baseline(ctx,target);if err!=nil{return nil,err}
	return s.prepare(ctx,target,"scale",old,append(tests(old),patchStep{"add","/spec/replicas",replicas}))
}
func(s *Service)PrepareRestart(ctx context.Context,target resource.Identity)(*Prepared,error){
	switch target.GVR.Resource{case "deployments","statefulsets","daemonsets":default:return nil,errors.New("resource does not support rollout restart")}
	old,err:=s.baseline(ctx,target);if err!=nil{return nil,err}
	annotations,_,err:=unstructured.NestedStringMap(old.Object,"spec","template","metadata","annotations");if err!=nil{return nil,err};if annotations==nil{annotations=map[string]string{}}
	annotations["aster.dev/restartedAt"]=s.now().UTC().Format(time.RFC3339Nano)
	return s.prepare(ctx,target,"restart",old,append(tests(old),patchStep{"add","/spec/template/metadata/annotations",annotations}))
}
func(s *Service)PrepareDelete(ctx context.Context,target resource.Identity)(*Prepared,error){
	old,err:=s.baseline(ctx,target);if err!=nil{return nil,err}
	uid,rv:=old.GetUID(),old.GetResourceVersion()
	policy:=metav1.DeletePropagationBackground
	err=s.client.DeleteObject(ctx,target.GVR,target.Namespace,target.Name,metav1.DeleteOptions{DryRun:[]string{metav1.DryRunAll},Preconditions:&metav1.Preconditions{UID:&uid,ResourceVersion:&rv},PropagationPolicy:&policy})
	if err!=nil{return nil,err}
	return s.prepared(target,"delete",old,nil,nil)
}
func(s *Service)prepare(ctx context.Context,target resource.Identity,kind string,old *unstructured.Unstructured,steps []patchStep)(*Prepared,error){
	body,err:=json.Marshal(steps);if err!=nil{return nil,err}
	after,err:=s.client.PatchObject(ctx,target.GVR,target.Namespace,target.Name,types.JSONPatchType,body,metav1.PatchOptions{DryRun:[]string{metav1.DryRunAll},FieldManager:"aster-desktop",FieldValidation:"Strict"})
	if err!=nil{return nil,err};return s.prepared(target,kind,old,after,body)
}
func(s *Service)prepared(target resource.Identity,kind string,before,after *unstructured.Unstructured,patch []byte)(*Prepared,error){
	pre,err:=before.MarshalJSON();if err!=nil{return nil,err}
	post:=[]byte("null");if after!=nil{post,err=after.MarshalJSON();if err!=nil{return nil,err}}
	expires:=s.now().Add(5*time.Minute)
	sum:=sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%s\n%s\n%d",target.Key(),kind,before.GetResourceVersion(),patch,expires.UnixNano())))
	return &Prepared{issuer:s,id:hex.EncodeToString(sum[:]),target:target,kind:kind,rv:before.GetResourceVersion(),patch:append([]byte(nil),patch...),before:pre,after:post,expires:expires},nil
}

// Execute never retries a write. Transport loss after submission is Unknown,
// not Success or Cancelled; the caller must read and reconcile the live state.
func(s *Service)Execute(ctx context.Context,p *Prepared)(Result,error){
	result:=Result{State:"Rejected"}
	if p==nil||p.issuer!=s||p.target.SessionID!=s.session{return result,errors.New("plan belongs to another service/session")}
	result.ID=p.id
	if !s.now().Before(p.expires){return result,errors.New("plan expired")}
	if err:=ctx.Err();err!=nil{return result,err}
	if !p.used.CompareAndSwap(false,true){return result,errors.New("plan already consumed")}
	latest,err:=s.baseline(ctx,p.target);if err!=nil{return result,err}
	if latest.GetResourceVersion()!=p.rv{return result,ErrConflict}
	if p.kind=="delete"{uid:=p.target.UID;rv:=p.rv;policy:=metav1.DeletePropagationBackground;err=s.client.DeleteObject(ctx,p.target.GVR,p.target.Namespace,p.target.Name,metav1.DeleteOptions{Preconditions:&metav1.Preconditions{UID:&uid,ResourceVersion:&rv},PropagationPolicy:&policy})}else{result.Object,err=s.client.PatchObject(ctx,p.target.GVR,p.target.Namespace,p.target.Name,types.JSONPatchType,p.patch,metav1.PatchOptions{FieldManager:"aster-desktop",FieldValidation:"Strict"})}
	if err==nil{result.State="Succeeded";return result,nil}
	if apierrors.IsConflict(err)||apierrors.IsInvalid(err){result.State="Conflict"}else if apierrors.IsForbidden(err)||apierrors.IsUnauthorized(err)||apierrors.IsBadRequest(err)||apierrors.IsNotFound(err){result.State="Rejected"}else{result.State="Unknown"}
	return result,err
}
