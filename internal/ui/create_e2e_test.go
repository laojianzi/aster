//go:build e2e

package uiworkbench

import (
 "context"
 "fmt"
 "strings"
 "testing"
 "time"
 "github.com/egoist/mygo/ui"
 "github.com/laojianzi/aster/internal/kubeconfig"
 corev1 "k8s.io/api/core/v1"
 apierrors "k8s.io/apimachinery/pkg/api/errors"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 "k8s.io/client-go/kubernetes"
 "k8s.io/client-go/tools/clientcmd"
)

func TestNativeCreatePreviewConfirmationAndPersistence(t *testing.T){
 cfg,err:=clientcmd.BuildConfigFromFlags("",clientcmd.RecommendedHomeFile);if err!=nil{t.Fatal(err)};admin,err:=kubernetes.NewForConfig(cfg);if err!=nil{t.Fatal(err)}
 ctx,cancel:=context.WithTimeout(context.Background(),90*time.Second);defer cancel()
 ns,err:=admin.CoreV1().Namespaces().Create(ctx,&corev1.Namespace{ObjectMeta:metav1.ObjectMeta{GenerateName:"aster-ui-create-"}},metav1.CreateOptions{});if err!=nil{t.Fatal(err)}
 t.Cleanup(func(){bg,done:=context.WithTimeout(context.Background(),15*time.Second);defer done();_ = admin.CoreV1().Namespaces().Delete(bg,ns.Name,metav1.DeleteOptions{})})
 _,current,err:=kubeconfig.Contexts("");if err!=nil{t.Fatal(err)}
 updates:=make(chan func(),128);w:=New();w.currentContext=current;w.contexts=[]string{current};w.namespace=ns.Name
 w.attach(ctx,func(fn func()){select{case updates<-fn:case<-ctx.Done():}});t.Cleanup(func(){cancel();w.Close()});tt:=ui.NewTester(w.View,1440,1000)
 pump:=func(predicate func()bool){t.Helper();for{select{case fn:=<-updates:fn();default:};tt.Frame();if predicate(){return};select{case<-ctx.Done():saveNativeScreenshot(t,tt);t.Fatalf("timeout: %s %s %s",w.status,w.errText,w.detailMessage);case<-time.After(20*time.Millisecond):}}}
 click:=func(label string){t.Helper();if err:=tt.Click(label);err!=nil{t.Fatal(err)}}
 click("Connect");pump(func()bool{return w.status=="Live"});click("ConfigMaps");pump(func()bool{return w.status=="Live"})
 click("New resource");if !w.creating{t.Fatalf("create view not opened: %s",w.errText)}
 click("Manifest editor");tt.Key(ui.Cmd,ui.KeyA);tt.Type(fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: aster-native-created\n  namespace: %s\ndata:\n  example: created-through-native-ui\n",ns.Name))
 click("Preview create");pump(func()bool{return w.plan!=nil||(!w.preparing&&w.detailMessage!="")});if w.plan==nil{t.Fatal(w.detailMessage)}
 if !strings.Contains(w.diff,"created-through-native-ui"){t.Fatal("preview omitted new data")}
 if _,err=admin.CoreV1().ConfigMaps(ns.Name).Get(ctx,"aster-native-created",metav1.GetOptions{});!apierrors.IsNotFound(err){t.Fatalf("preview wrote: %v",err)}
 click("Confirm resource name");tt.Type("wrong-name");_ = tt.Click("Execute reviewed change")
 if w.pendingWrites!=0{t.Fatal("wrong confirmation submitted a write")}
 click("Confirm resource name");tt.Key(ui.Cmd,ui.KeyA);tt.Type("aster-native-created");click("Execute reviewed change");pump(func()bool{return w.pendingWrites==0&&len(w.history)>0})
 created,err:=admin.CoreV1().ConfigMaps(ns.Name).Get(ctx,"aster-native-created",metav1.GetOptions{});if err!=nil{t.Fatal(err)}
 if created.Data["example"]!="created-through-native-ui"||w.creating||w.detail.GetUID()!=created.UID{t.Fatalf("create result not reflected: %s",w.detailMessage)}
 saveNativeScreenshot(t,tt)
}
