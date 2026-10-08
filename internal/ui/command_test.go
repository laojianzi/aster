package uiworkbench

import (
 "context"
 "strings"
 "testing"
 "github.com/egoist/mygo/ui"
)
func TestNativeCommandRequiresConfirmationAndDoesNotInterpretShell(t *testing.T){
 w:=New();w.detail=modelObject("one","pod");w.detailKind=catalog()[0];w.detailMode="Command";w.containers=[]string{"http"};w.commandArgv=`["printf","%s",";touch /x"]`
 tt:=ui.NewTester(w.View,1100,700)
 for _,label:=range []string{"Command","Command container","Command argv JSON","Confirm Pod for command","Run command","Stop command","Command output"}{
  r,ok:=tt.Find(label);if !ok || r.X<0 || r.Y<0 || r.X+r.W>1088 || r.Y+r.H>700 || r.W<=0||r.H<=0{t.Fatalf("missing/outside control %s %+v",label,r)}
 }
 if err:=tt.Click("Run command");err==nil && w.commandActive{t.Fatal("unconfirmed command started")}
 if err:=tt.Click("Confirm Pod for command");err!=nil{t.Fatal(err)};tt.Type("pod")
 if w.commandConfirmation!="pod"{t.Fatal("confirmation input missing")}
 if err:=tt.Click("Command argv JSON");err!=nil{t.Fatal(err)};tt.Key(ui.Cmd,ui.KeyA);tt.Type(`["id"]`)
 if w.commandConfirmation!=""{t.Fatal("edited argv retained prior confirmation")}
 saveNativeScreenshot(t,tt)
}
func TestClosingDetailCancelsCommandAndErasesCapturedData(t *testing.T){
 w:=New();ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 w.commandCancel=cancel;w.commandActive=true;w.commandArgv="synthetic-sensitive-argv";w.commandOutput="synthetic-sensitive-output";w.commandConfirmation="pod"
 before:=w.commandEpoch
 w.clearDetail()
 if ctx.Err()==nil || w.commandActive || w.commandEpoch<=before || w.commandArgv!="" || w.commandOutput!="" || w.commandConfirmation!=""{t.Fatal("command session or data survived detail close")}
 w.commandActive=true;w.stopCommand()
 if !strings.Contains(w.commandStatus,"not confirmed"){t.Fatal("stop falsely claimed remote termination")}
}
