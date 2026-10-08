//go:build e2e

package uiworkbench

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/manifest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// This exercises actual native controls and the real Kubernetes write path.
// It is not an OS-window/IME/driver test; those are separate release gates.
func TestNativeWorkbenchEditAgainstRealCluster(t *testing.T){
	cfg,err:=clientcmd.BuildConfigFromFlags("",clientcmd.RecommendedHomeFile);if err!=nil{t.Fatal(err)}
	admin,err:=kubernetes.NewForConfig(cfg);if err!=nil{t.Fatal(err)}
	ctx,cancel:=context.WithTimeout(context.Background(),90*time.Second)
	ns,err:=admin.CoreV1().Namespaces().Create(ctx,&corev1.Namespace{ObjectMeta:metav1.ObjectMeta{GenerateName:"aster-ui-"}},metav1.CreateOptions{});if err!=nil{cancel();t.Fatal(err)}
	t.Cleanup(func(){bg,done:=context.WithTimeout(context.Background(),20*time.Second);defer done();_ = admin.CoreV1().Namespaces().Delete(bg,ns.Name,metav1.DeleteOptions{})})
	cm,err:=admin.CoreV1().ConfigMaps(ns.Name).Create(ctx,&corev1.ConfigMap{ObjectMeta:metav1.ObjectMeta{Name:"aster-native-edit"},Data:map[string]string{"value":"before"}},metav1.CreateOptions{});if err!=nil{cancel();t.Fatal(err)}
	_,current,err:=kubeconfig.Contexts("");if err!=nil{cancel();t.Fatal(err)}
	updates:=make(chan func(),128)
	w:=New();w.currentContext=current;w.contexts=[]string{current};w.namespace=ns.Name
	w.attach(ctx,func(fn func()){select{case updates<-fn:case<-ctx.Done():}})
	t.Cleanup(func(){cancel();w.Close()})
	tt:=ui.NewTester(w.View,1440,1000)
	pump:=func(predicate func()bool){t.Helper();for{select{case fn:=<-updates:fn();default:};tt.Frame();if predicate(){return};select{case<-ctx.Done():saveNativeScreenshot(t,tt);t.Fatalf("UI timeout: status=%s namespace=%s kind=%s rows=%v error=%s detail=%s",w.status,w.namespace,w.currentKind.GVR.Resource,w.rows,w.errText,w.detailMessage);case<-time.After(20*time.Millisecond):}}}
	click:=func(label string){t.Helper();if err:=tt.Click(label);err!=nil{saveNativeScreenshot(t,tt);t.Fatal(err)}}
	click("Connect");pump(func()bool{return w.status=="Live"})
	click("ConfigMaps");pump(func()bool{return containsRowUID(w.rows,string(cm.UID))})
	click(cm.Name);pump(func()bool{return w.detail!=nil})
	click("Edit");click("Manifest editor")
	next:=w.detail.DeepCopy();if err:=unstructured.SetNestedField(next.Object,"after","data","value");err!=nil{t.Fatal(err)}
	text,err:=manifest.Display(next,false);if err!=nil{t.Fatal(err)}
	tt.Key(ui.Cmd,ui.KeyA);tt.Type(text)
	if !strings.Contains(w.editor,"after"){t.Fatal("native editor did not receive typed YAML")}
	click("Preview edit");pump(func()bool{return w.plan!=nil||(!w.preparing&&w.detailMessage!="")})
	if w.plan==nil{t.Fatalf("preview failed: %s",w.detailMessage)}
	unchanged,err:=admin.CoreV1().ConfigMaps(ns.Name).Get(ctx,cm.Name,metav1.GetOptions{});if err!=nil{t.Fatal(err)}
	if unchanged.Data["value"]!="before"{t.Fatal("preview persisted a mutation")}
	if !strings.Contains(w.diff,"after"){t.Fatal("review does not show new value")}
	click("Confirm resource name");tt.Type(cm.Name)
	click("Execute reviewed change");pump(func()bool{return w.pendingWrites==0&&len(w.history)>0})
	changed,err:=admin.CoreV1().ConfigMaps(ns.Name).Get(ctx,cm.Name,metav1.GetOptions{});if err!=nil{t.Fatal(err)}
	if changed.Data["value"]!="after"{t.Fatalf("write not persisted: %v; UI=%s",changed.Data,w.detailMessage)}
	if !strings.Contains(w.history[len(w.history)-1],"Succeeded"){t.Fatal(w.history)}
	saveNativeScreenshot(t,tt)
}

func containsRowUID(rows []resourceRow,uid string)bool{for _,row:=range rows{if row.UID==uid{return true}};return false}
