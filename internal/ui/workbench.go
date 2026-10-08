package uiworkbench

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/cluster"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/operation"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Workbench struct{
	ctx context.Context
	cancel context.CancelFunc
	life *cluster.Session
	dispatch func(func())
	contextEpoch,scopeEpoch,detailEpoch,draftRevision uint64
	scopeCancel,logCancel context.CancelFunc
	backend *kube.Backend
	ops *operation.Service
	sessionID string
	path,currentContext,namespace,labelSelector string
	contexts []string
	trustedContext string
	trustRequired bool
	kinds []kube.ResourceKind
	currentKind kube.ResourceKind
	kindChoice string
	status,errText,notice string
	filter,sortBy string
	rows []resourceRow
	total int
	store *rowStore
	table,logList ui.ListState
	selected int
	detail *unstructured.Unstructured
	detailKind kube.ResourceKind
	detailMode,detailText,editor,diff,confirmation,detailMessage string
	containers []string
	container,replicas string
	plan *operation.Prepared
	preparing bool
	pendingWrites int
	logRows []string
	logStatus string
	logDropped uint64
	history []string
	frames int
}

func New()*Workbench{
	w:=&Workbench{ctx:context.Background(),namespace:"default",status:"Not connected",sortBy:"Name",selected:-1,replicas:"1",detailMode:"YAML"}
	w.kinds=catalog();w.currentKind=w.kinds[0];w.kindChoice=w.currentKind.Label()
	w.table.Selected=&w.selected
	w.table.Key=func(i int)any{if i<0||i>=len(w.rows){return ""};return w.rows[i].key()}
	return w
}
func formatReplicas(ready,desired int64)string{return fmt.Sprintf("%d / %d ready",ready,desired)}

func(w *Workbench)View(c *ui.Context){
	w.frames++
	t:=c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func(){
		ui.Row(c).Height(58).Padding(12).Gap(10).Children(func(){
			ui.Text(c,"Aster").Bold().FontSize(22)
			ui.Text(c,"Native Kubernetes Workbench").FontSize(12).TextColor(t.TextMuted)
			if ui.Select(c,&w.currentContext,w.contexts).Label("Cluster context").Width(250).Changed(){w.trustedContext="";w.trustRequired=false}
			ui.TextInput(c,&w.namespace).Label("Namespace").Placeholder("Namespace or *").Width(150)
			if ui.PrimaryButton(c,"Connect").Disabled(w.currentContext=="").Clicked(){w.connect()}
			if ui.Button(c,"Disconnect").Disabled(w.backend==nil).Clicked(){w.disconnect()}
		})
		ui.Row(c).Height(44).Padding(6,12).Gap(8).Children(func(){
			ui.TextInput(c,&w.path).Label("Kubeconfig path").Placeholder("Kubeconfig path (empty: default)").Width(320)
			if ui.Button(c,"Load contexts").Clicked(){w.loadContexts()}
			ui.Text(c,w.notice).FontSize(12).TextColor(t.TextMuted)
		})
		if w.trustRequired{
			ui.Row(c).Padding(10).Gap(10).Children(func(){
				ui.Text(c,"This context requests local credential access or executable authentication. Trust only a configuration you control.").FontSize(12)
				if ui.Button(c,"Trust this context and connect").Clicked(){w.trustedContext=w.currentContext;w.connect()}
			})
		}
		ui.Row(c).Grow(1).Children(func(){
			ui.Column(c).Width(174).Padding(10).Gap(5).Children(func(){
				ui.Text(c,"RESOURCES").FontSize(11).Bold().TextColor(t.TextMuted)
				for _,item:=range []struct{Label,Resource string}{{"Pods","pods"},{"Deployments","deployments"},{"StatefulSets","statefulsets"},{"DaemonSets","daemonsets"},{"Jobs","jobs"},{"CronJobs","cronjobs"},{"Services","services"},{"Ingresses","ingresses"},{"ConfigMaps","configmaps"},{"Secrets","secrets"},{"Volume Claims","persistentvolumeclaims"},{"Nodes","nodes"},{"Namespaces","namespaces"}}{
					if ui.Button(c,item.Label).Width(152).Height(30).Clicked(){for _,kind:=range catalog(){if kind.GVR.Resource==item.Resource{w.chooseKind(kind);break}}}
				}
			})
			ui.Column(c).Grow(1).Padding(12).Gap(8).Children(func(){
				ui.Row(c).Gap(8).Children(func(){
					ui.Text(c,w.currentKind.Kind).Bold().FontSize(20)
					ui.Text(c,fmt.Sprintf("%d / %d resources",len(w.rows),w.total)).FontSize(12).TextColor(t.TextMuted)
					if ui.Button(c,"Refresh").Disabled(w.backend==nil).Clicked(){w.startScope()}
				})
				labels:=make([]string,0,len(w.kinds));for _,k:=range w.kinds{labels=append(labels,k.Label())}
				if ui.Select(c,&w.kindChoice,labels).Label("Resource kind").Changed(){for _,k:=range w.kinds{if k.Label()==w.kindChoice{w.chooseKind(k);break}}}
				ui.Row(c).Gap(8).Children(func(){
					if ui.TextInput(c,&w.filter).Label("Filter resources").Placeholder("Filter name, namespace or status").Grow(1).Changed(){w.queryChanged()}
					if ui.Select(c,&w.sortBy,[]string{"Name","Namespace","Status"}).Label("Sort resources").Width(115).Changed(){w.queryChanged()}
				})
				ui.Row(c).Gap(8).Children(func(){
					ui.TextInput(c,&w.labelSelector).Label("Label selector").Placeholder("Server label selector, e.g. app=api").Grow(1)
					if ui.Button(c,"Apply selector").Disabled(w.backend==nil).Clicked(){w.startScope()}
				})
				if w.errText!=""{ui.Text(c,w.errText).FontSize(12).TextColor(t.Danger)}
				cols:=[]ui.TableColumn{{ID:"name",Title:"Name"},{ID:"namespace",Title:"Namespace",Width:130},{ID:"status",Title:"Status",Width:150}}
				ui.Table(c,&w.table,cols,len(w.rows),func(row,col int){r:=w.rows[row];switch col{case 0:if ui.Button(c,r.Name).Clicked(){w.openResource(r)};case 1:ui.Text(c,r.Namespace).SingleLine();case 2:ui.Text(c,r.Status).SingleLine()}}).Grow(1)
				if len(w.rows)==0&&w.status=="Live"{ui.Text(c,"No resources match this scope and filter.").TextColor(t.TextMuted)}
				ui.Text(c,"Select a resource to inspect or prepare a change.").FontSize(11).TextColor(t.TextMuted)
			})
			if w.detail!=nil{w.detailView(c)}
		})
		ui.Row(c).Height(34).Padding(6,12).Gap(12).Children(func(){
			ui.Text(c,"Status: "+w.status).FontSize(12)
			ui.Text(c,fmt.Sprintf("%d active write(s)",w.pendingWrites)).FontSize(12).TextColor(t.TextMuted)
			if len(w.history)>0{ui.Text(c,w.history[len(w.history)-1]).FontSize(11).SingleLine().TextColor(t.TextMuted)}
		})
	})
}

func(w *Workbench)detailView(c *ui.Context){
	t:=c.Theme()
	ui.Column(c).Width(510).Padding(12).Gap(8).Children(func(){
		ui.Row(c).Gap(8).Children(func(){ui.Text(c,w.detail.GetName()).Bold().FontSize(18);if ui.Button(c,"Close detail").Clicked(){w.clearDetail()}})
		if w.detail==nil{return}
		ui.Text(c,w.currentContext+" / "+w.detail.GetNamespace()+" / "+w.detailKind.Kind).FontSize(11).TextColor(t.TextMuted)
		ui.Row(c).Gap(6).Children(func(){
			if ui.Button(c,"YAML").Clicked(){w.detailMode="YAML";w.stopLogs()}
			if ui.Button(c,"Edit").Disabled(w.detail.GetKind()=="Secret").Clicked(){w.detailMode="Edit";w.stopLogs()}
			if ui.Button(c,"Events").Clicked(){w.loadEvents()}
			if ui.Button(c,"Logs").Disabled(w.detailKind.GVR.Resource!="pods").Clicked(){w.detailMode="Logs"}
			if ui.Button(c,"Refresh detail").Clicked(){w.openResource(project(w.detail))}
		})
		if w.detailMessage!=""{ui.Text(c,w.detailMessage).FontSize(12).TextColor(t.TextMuted)}
		switch w.detailMode{
		case "Edit":
			if ui.TextArea(c,&w.editor).Label("Manifest editor").Grow(1).Changed(){w.draftRevision++;w.plan=nil;w.diff="";w.confirmation=""}
			if ui.PrimaryButton(c,"Preview edit").Disabled(w.preparing).Clicked(){w.prepare("edit")}
		case "Diff":
			ui.Text(c,"Server dry-run preview · live state is rechecked at execution").FontSize(12)
			ui.TextArea(c,&w.diff).Label("Change diff").ReadOnly(true).Grow(1)
			ui.TextInput(c,&w.confirmation).Label("Confirm resource name").Placeholder("Type the resource name to confirm")
			if ui.PrimaryButton(c,"Execute reviewed change").Disabled(w.plan==nil||w.confirmation!=w.detail.GetName()||w.pendingWrites>0).Clicked(){w.execute()}
		case "Logs":
			ui.Select(c,&w.container,w.containers).Label("Container")
			ui.Row(c).Gap(6).Children(func(){if ui.Button(c,"Follow logs").Clicked(){w.startLogs(false,true)};if ui.Button(c,"Previous logs").Clicked(){w.startLogs(true,false)};if ui.Button(c,"Stop logs").Clicked(){w.stopLogs();w.logStatus="Stopped"}})
			ui.List(c,&w.logList,len(w.logRows),func(i int){ui.Text(c,w.logRows[i]).FontSize(11).SingleLine()}).Grow(1)
			ui.Text(c,fmt.Sprintf("%s · %d truncated line(s)",w.logStatus,w.logDropped)).FontSize(11)
		default:
			ui.TextArea(c,&w.detailText).Label("Resource document").ReadOnly(true).Grow(1)
		}
		if w.detail!=nil&&w.detailMode!="Diff"{
			ui.Row(c).Gap(6).Children(func(){
				ui.TextInput(c,&w.replicas).Label("Replica count").Width(70)
				if ui.Button(c,"Preview scale").Disabled(w.preparing).Clicked(){w.prepare("scale")}
				if ui.Button(c,"Preview restart").Disabled(w.preparing).Clicked(){w.prepare("restart")}
				if ui.Button(c,"Preview delete").Disabled(w.preparing).Clicked(){w.prepare("delete")}
			})
		}
	})
}

func Open(parent context.Context)*Workbench{
	w:=New()
	win:=mygo.NewWindow(mygo.WindowOptions{Title:"Aster",Width:1440,Height:900,MinWidth:1100,MinHeight:700,StateKey:"main",Content:ui.View(w.View)})
	w.attach(parent,func(fn func()){win.Update(fn)})
	win.OnClosed(func(){w.cancel();mygo.App.Quit()})
	if os.Getenv("ASTER_NATIVE_SMOKE")=="1"{
		w.notice="Smoke fixture · no kubeconfig is read"
		w.rows=[]resourceRow{{UID:"fixture",Name:"aster-native-smoke",Namespace:"fixture",Status:"Rendered"}};w.total=1;w.status="Smoke fixture"
		w.run(func(ctx context.Context){ticker:=time.NewTicker(100*time.Millisecond);defer ticker.Stop();for{select{case<-ctx.Done():return;case<-ticker.C:w.emit(func(){if w.frames>0{fmt.Println("ASTER_NATIVE_SMOKE_OK");win.Close()}})}}})
	}else{w.loadContexts()}
	return w
}
func(w *Workbench)attach(parent context.Context,dispatch func(func())){w.ctx,w.cancel=context.WithCancel(parent);w.life=cluster.NewSession(w.ctx,"desktop");w.dispatch=dispatch}
func(w *Workbench)run(fn func(context.Context)){if w.life!=nil{_ = w.life.Go(fn)}}
func(w *Workbench)emit(fn func()){if w.dispatch==nil||w.ctx.Err()!=nil{return};w.dispatch(func(){if w.ctx.Err()==nil{fn()}})}
// Close is called after the event loop, or by the owning UI test goroutine.
func(w *Workbench)Close(){if w.cancel!=nil{w.cancel()};if w.life!=nil{w.life.Close()}}
func parseReplicaCount(text string)(int64,error){return strconv.ParseInt(text,10,64)}
