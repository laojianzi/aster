//go:build e2e

package e2e

import (
 "context"
 "fmt"
 "testing"
 "time"
 "github.com/laojianzi/aster/internal/kube"
 "github.com/laojianzi/aster/internal/operation"
 "github.com/laojianzi/aster/internal/resource"
 corev1 "k8s.io/api/core/v1"
 apierrors "k8s.io/apimachinery/pkg/api/errors"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/apimachinery/pkg/runtime/schema"
 "k8s.io/client-go/kubernetes"
 "k8s.io/client-go/tools/clientcmd"
)
func TestCreateDryRunAndConcurrentCreateNeverOverwrite(t *testing.T){
 cfg,err:=clientcmd.BuildConfigFromFlags("",clientcmd.RecommendedHomeFile);if err!=nil{t.Fatal(err)};admin,err:=kubernetes.NewForConfig(cfg);if err!=nil{t.Fatal(err)};backend,err:=kube.New(cfg);if err!=nil{t.Fatal(err)}
 ctx,cancel:=context.WithTimeout(context.Background(),60*time.Second);defer cancel()
 ns,err:=admin.CoreV1().Namespaces().Create(ctx,&corev1.Namespace{ObjectMeta:metav1.ObjectMeta{GenerateName:"aster-create-"}},metav1.CreateOptions{});if err!=nil{t.Fatal(err)}
 t.Cleanup(func(){ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel();_ = admin.CoreV1().Namespaces().Delete(ctx,ns.Name,metav1.DeleteOptions{})})
 target:=resource.Identity{SessionID:"create-test",GVR:schema.GroupVersionResource{Version:"v1",Resource:"configmaps"},Namespace:ns.Name,Name:"reviewed-create"}
 text:=[]byte(fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\n  namespace: %s\ndata:\n  owner: aster\n",target.Name,ns.Name));service:=operation.NewService(backend,target.SessionID)
 first,err:=service.PrepareCreate(ctx,target,text);if err!=nil{t.Fatal(err)};second,err:=service.PrepareCreate(ctx,target,text);if err!=nil{t.Fatal(err)}
 if _,err=admin.CoreV1().ConfigMaps(ns.Name).Get(ctx,target.Name,metav1.GetOptions{});!apierrors.IsNotFound(err){t.Fatalf("dry-run persisted: %v",err)}
 if r,err:=service.Execute(ctx,first);err!=nil||r.State!="Succeeded"{t.Fatalf("create: %v %v",r,err)}
 live,err:=admin.CoreV1().ConfigMaps(ns.Name).Get(ctx,target.Name,metav1.GetOptions{});if err!=nil{t.Fatal(err)};live.Data["owner"]="concurrent-user";live,err=admin.CoreV1().ConfigMaps(ns.Name).Update(ctx,live,metav1.UpdateOptions{});if err!=nil{t.Fatal(err)}
 result,err:=service.Execute(ctx,second);if !apierrors.IsAlreadyExists(err)||result.State!="Conflict"{t.Fatalf("expected create conflict: %v %v",result,err)}
 final,err:=admin.CoreV1().ConfigMaps(ns.Name).Get(ctx,target.Name,metav1.GetOptions{});if err!=nil||final.Data["owner"]!="concurrent-user"||final.UID!=live.UID{t.Fatalf("concurrent object overwritten: %v",err)}
}
