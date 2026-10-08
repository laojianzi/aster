//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestPortForwardTransfersRealTrafficAndClosesListener(t *testing.T){
	f:=testcluster.NewHTTPPod(t)
	backend,err:=kube.New(f.Config);if err!=nil{t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
	ports:=make(chan uint16,1);done:=make(chan error,1)
	go func(){done<-backend.ForwardPod(ctx,f.Target("port-test"),8080,func(port uint16){ports<-port})}()
	var local uint16
	select{case local=<-ports:case err:=<-done:t.Fatalf("forward failed: %v",err);case<-ctx.Done():t.Fatal(ctx.Err())}
	address:=fmt.Sprintf("127.0.0.1:%d",local);client:=&http.Client{Timeout:5*time.Second}
	response,err:=client.Get("http://"+address+"/");if err!=nil{t.Fatal(err)}
	data,err:=io.ReadAll(io.LimitReader(response.Body,4096));response.Body.Close();if err!=nil||response.StatusCode!=http.StatusOK||string(data)!=testcluster.Message{t.Fatalf("forward response: %d %q %v",response.StatusCode,data,err)}
	cancel()
	select{case<-done:case<-time.After(15*time.Second):t.Fatal("forward did not terminate on cancellation")}
	conn,err:=net.DialTimeout("tcp",address,time.Second);if err==nil{conn.Close();t.Fatal("listener survived forward cancellation")}
}
func TestPortForwardRejectsReadOnlyIdentityAndReplacedPod(t *testing.T){
	f:=testcluster.NewHTTPPod(t);ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
	const name="port-reader"
	_,err:=f.Client.RbacV1().Roles(f.Pod.Namespace).Create(ctx,&rbacv1.Role{ObjectMeta:metav1.ObjectMeta{Name:name},Rules:[]rbacv1.PolicyRule{{APIGroups:[]string{""},Resources:[]string{"pods"},Verbs:[]string{"get"}}}},metav1.CreateOptions{});if err!=nil{t.Fatal(err)}
	_,err=f.Client.RbacV1().RoleBindings(f.Pod.Namespace).Create(ctx,&rbacv1.RoleBinding{ObjectMeta:metav1.ObjectMeta{Name:name},Subjects:[]rbacv1.Subject{{Kind:"User",Name:name}},RoleRef:rbacv1.RoleRef{APIGroup:rbacv1.GroupName,Kind:"Role",Name:name}},metav1.CreateOptions{});if err!=nil{t.Fatal(err)}
	cfg:=rest.CopyConfig(f.Config);cfg.Impersonate=rest.ImpersonationConfig{UserName:name}
	restricted,err:=kubernetes.NewForConfig(cfg);if err!=nil{t.Fatal(err)}
	for{_,err=restricted.CoreV1().Pods(f.Pod.Namespace).Get(ctx,f.Pod.Name,metav1.GetOptions{});if err==nil{break};select{case<-ctx.Done():t.Fatalf("get permission not effective: %v",err);case<-time.After(100*time.Millisecond):}}
	backend,err:=kube.New(cfg);if err!=nil{t.Fatal(err)};called:=false
	err=backend.ForwardPod(ctx,f.Target("restricted"),8080,func(uint16){called=true})
	if !apierrors.IsForbidden(err)||called{t.Fatalf("read-only identity could forward: callback=%v err=%v",called,err)}
	backend,err=kube.New(f.Config);if err!=nil{t.Fatal(err)};target:=f.Target("replaced");target.UID="not-the-current-pod"
	err=backend.ForwardPod(ctx,target,8080,func(uint16){called=true});if err==nil||called{t.Fatal("replaced Pod was accepted")}
}
