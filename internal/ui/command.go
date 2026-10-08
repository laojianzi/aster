package uiworkbench

import (
 "context"
 "fmt"
 "sync/atomic"
 "time"
 "github.com/egoist/mygo/ui"
 "github.com/laojianzi/aster/internal/execsession"
)

func (w *Workbench) stopCommand() {
 w.commandEpoch++
 if w.commandCancel!=nil { w.commandCancel();w.commandCancel=nil }
 if w.commandActive { w.commandStatus="Disconnected · remote process termination is not confirmed" }
 w.commandActive=false
 w.commandConfirmation=""
}
func (w *Workbench) startCommand() {
 if w.detail==nil || w.backend==nil || w.creating || w.commandActive || w.detailKind.GVR.Group!="" || w.detailKind.GVR.Resource!="pods" { return }
 if w.commandConfirmation!=w.detail.GetName() { w.commandStatus="Confirm the exact Pod name before execution";return }
 argv,err:=execsession.ParseArgv(w.commandArgv)
 if err!=nil { w.commandStatus=err.Error();return }
 cmd:=execsession.Command{Container:w.commandContainer,Argv:argv,Timeout:execsession.DefaultTimeout}
 if err:=cmd.Validate();err!=nil { w.commandStatus=err.Error();return }
 w.stopCommand()
 epoch,revision:=w.detailEpoch,w.commandEpoch
 backend,target,contextName:=w.backend,w.target(),w.activeContext
 ctx,cancel:=context.WithCancel(w.operationContext())
 w.commandCancel,w.commandActive,w.commandStatus,w.commandOutput=cancel,true,"Connecting · no automatic replay",""
 output,err:=execsession.NewOutput(execsession.DefaultOutputLimit,nil)
 if err!=nil {cancel();w.commandActive=false;w.commandStatus=err.Error();return}
 w.run(func(context.Context){
  defer cancel()
  type completed struct{ result execsession.Result; err error }
  done:=make(chan completed,1)
  go func(){r,e:=backend.RunPodCommand(ctx,target,cmd,output);done<-completed{r,e}}()
  ticker:=time.NewTicker(100*time.Millisecond);defer ticker.Stop()
  var pending atomic.Bool
  var last uint64
  for { select {
   case ended:=<-done:
    snapshot:=output.Snapshot()
    w.emit(func(){
     if epoch!=w.detailEpoch || revision!=w.commandEpoch{return}
     w.commandActive,w.commandCancel=false,nil
     w.commandOutput=snapshot.Display()
     w.commandStatus=ended.result.String()
     if ended.err!=nil && ended.result.State==execsession.Rejected{w.commandStatus+=" · "+ended.err.Error()}
     // Never retain argv or output in operation history.
     w.recordHistory(fmt.Sprintf("[%s] command %s/%s (%s) · %s",contextName,target.Namespace,target.Name,cmd.Container,ended.result.State))
    })
    return
   case <-ticker.C:
    if ctx.Err()!=nil || !pending.CompareAndSwap(false,true){continue}
    snapshot:=output.Snapshot()
    if snapshot.Revision==last{pending.Store(false);continue};last=snapshot.Revision
    w.emit(func(){defer pending.Store(false);if epoch!=w.detailEpoch || revision!=w.commandEpoch || ctx.Err()!=nil{return};w.commandOutput=snapshot.Display();w.commandStatus="Receiving output · remote exit pending"})
  } }
 })
}
func(w *Workbench) commandView(c *ui.Context){
 ui.Text(c,"Non-interactive command · no PTY, stdin or implicit shell").FontSize(11)
 ui.Text(c,"Two-minute deadline · 128 KiB output · stopping may leave the remote process running").FontSize(11)
 if w.commandContainer=="" && len(w.containers)>0{w.commandContainer=w.containers[0]}
 if ui.Select(c,&w.commandContainer,w.containers).Label("Command container").Disabled(w.commandActive).Changed(){w.commandConfirmation=""}
 if ui.TextArea(c,&w.commandArgv).Label("Command argv JSON").Height(65).ReadOnly(w.commandActive).Changed(){w.commandConfirmation=""}
 ui.TextInput(c,&w.commandConfirmation).Label("Confirm Pod for command").Placeholder("Type the exact Pod name").Disabled(w.commandActive)
 ui.Row(c).Gap(8).Children(func(){
  if ui.PrimaryButton(c,"Run command").Disabled(w.commandActive||w.detail==nil||w.commandConfirmation!=w.detail.GetName()||w.commandContainer=="").Clicked(){w.startCommand()}
  if ui.Button(c,"Stop command").Disabled(!w.commandActive).Clicked(){w.stopCommand()}
 })
 ui.Text(c,w.commandStatus).FontSize(11)
 ui.TextArea(c,&w.commandOutput).Label("Command output").ReadOnly(true).Grow(1)
}
