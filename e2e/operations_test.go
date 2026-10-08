//go:build e2e

package e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/resource"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)
func TestAsterMutationDryRunConflictAndDelete(t *testing.T){
	cfg,err:=clientcmd.BuildConfigFromFlags("",clientcmd.RecommendedHomeFile);if err!=nil{t.Fatal(err)}
	admin,err:=kubernetes.NewForConfig(cfg);if err!=nil{t.Fatal(err)}
	backend,err:=kube.New(cfg);if err!=nil{t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),60*time.Second);defer cancel()
	cm,err:=admin.CoreV1().ConfigMaps("default").Create(ctx,&corev1.ConfigMap{ObjectMeta:metav1.ObjectMeta{GenerateName:"aster-plan-"},Data:map[string]string{"value":"before"}},metav1.CreateOptions{});if err!=nil{t.Fatal(err)}
	t.Cleanup(func(){bg,done:=context.WithTimeout(context.Background(),15*time.Second);defer done();_ = admin.CoreV1().ConfigMaps("default").Delete(bg,cm.Name,metav1.DeleteOptions{})})
	gvr:=schema.GroupVersionResource{Version:"v1",Resource:"configmaps"};target:=resource.Identity{SessionID:"e2e-session",GVR:gvr,Namespace:"default",Name:cm.Name,UID:cm.UID};service:=operation.NewService(backend,target.SessionID)
	object,err:=backend.GetObject(ctx,gvr,"default",cm.Name);if err!=nil{t.Fatal(err)}
	if err:=unstructured.SetNestedField(object.Object,"after","data","value");err!=nil{t.Fatal(err)}
	text,err:=manifest.Display(object,false);if err!=nil{t.Fatal(err)}
	plan,err:=service.PrepareEdit(ctx,target,[]byte(text));if err!=nil{t.Fatal(err)}
	live,err:=admin.CoreV1().ConfigMaps("default").Get(ctx,cm.Name,metav1.GetOptions{});if err!=nil||live.Data["value"]!="before"{t.Fatalf("preview persisted: %v",err)}
	result,err:=service.Execute(ctx,plan);if err!=nil||result.State!="Succeeded"{t.Fatalf("%v %v",result,err)}
	if _,err:=service.Execute(ctx,plan);err==nil{t.Fatal("replay accepted")}
	live,err=admin.CoreV1().ConfigMaps("default").Get(ctx,cm.Name,metav1.GetOptions{});if err!=nil||live.Data["value"]!="after"{t.Fatalf("write failed: %v",err)}
	deletePlan,err:=service.PrepareDelete(ctx,target);if err!=nil{t.Fatal(err)}
	live.Data["concurrent"]="preserve-me"
	if _,err=admin.CoreV1().ConfigMaps("default").Update(ctx,live,metav1.UpdateOptions{});err!=nil{t.Fatal(err)}
	if _,err=service.Execute(ctx,deletePlan);!errors.Is(err,operation.ErrConflict){t.Fatalf("stale delete: %v",err)}
	deletePlan,err=service.PrepareDelete(ctx,target);if err!=nil{t.Fatal(err)}
	if _,err=service.Execute(ctx,deletePlan);err!=nil{t.Fatal(err)}
	if _,err=admin.CoreV1().ConfigMaps("default").Get(ctx,cm.Name,metav1.GetOptions{});!apierrors.IsNotFound(err){t.Fatalf("delete did not persist: %v",err)}
}
