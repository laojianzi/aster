package uiworkbench

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
)

func TestNativeNavigationAndUnicodeInput(t *testing.T){
	w:=New();tt:=ui.NewTester(w.View,1440,900)
	if !tt.HasText("Aster"){t.Fatal("brand not rendered")}
	if err:=tt.Click("ConfigMaps");err!=nil{t.Fatal(err)}
	if w.currentKind.GVR.Resource!="configmaps"{t.Fatal("navigation did not select ConfigMaps")}
	if err:=tt.Click("Filter resources");err!=nil{t.Fatal(err)}
	tt.Type("api-工作负载")
	if w.filter!="api-工作负载"{t.Fatalf("input = %q",w.filter)}
	tt.Key(ui.Cmd,ui.KeyA);tt.Type("replacement")
	if w.filter!="replacement"{t.Fatalf("select-all replacement = %q",w.filter)}
	for _,dark:=range []bool{false,true}{tt.SetDark(dark);tt.SetScale(2);tt.Frame();if !tt.HasText("Aster"){t.Fatal("theme lost view")}}
	saveNativeScreenshot(t,tt)
}
func saveNativeScreenshot(t *testing.T,tt *ui.Tester){
	t.Helper();dir:=os.Getenv("ASTER_TEST_ARTIFACTS");if dir==""{return}
	if err:=os.MkdirAll(dir,0755);err!=nil{t.Fatal(err)}
	path:=filepath.Join(dir,strings.ReplaceAll(t.Name(),"/","_")+".png")
	f,err:=os.Create(path);if err!=nil{t.Fatal(err)}
	if err=png.Encode(f,tt.Image());err!=nil{f.Close();t.Fatal(err)}
	if err=f.Close();err!=nil{t.Fatal(err)}
}
func modelObject(uid,name string)*unstructured.Unstructured{o:=&unstructured.Unstructured{Object:map[string]interface{}{"apiVersion":"v1","kind":"Pod","metadata":map[string]interface{}{"uid":uid,"name":name,"namespace":"team","resourceVersion":"1"},"status":map[string]interface{}{"phase":"Running"}}};return o}
func TestProjectionRejectsOldUIDDeletionAndOwnsSnapshot(t *testing.T){
	s:=newRowStore();s.event(kube.ResourceEvent{Type:watch.Added,Object:modelObject("old","api")});s.event(kube.ResourceEvent{Type:watch.Added,Object:modelObject("new","api")});s.event(kube.ResourceEvent{Type:watch.Deleted,Object:modelObject("old","api")})
	snap,ok:=s.snapshot();if !ok||len(snap.Rows)!=1||snap.Rows[0].UID!="new"{t.Fatalf("%+v",snap)}
	snap.Rows[0].Name="mutated";s.setQuery("Running","Name");again,_:=s.snapshot();if len(again.Rows)!=1||again.Rows[0].Name!="api"{t.Fatal("snapshot mutation changed store")}
	s.setQuery("missing","Name");again,_=s.snapshot();if len(again.Rows)!=0||again.Total!=1{t.Fatal("filter lost total")}
}
