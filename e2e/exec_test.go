//go:build e2e

package e2e

import (
 "context"
 "errors"
 "strings"
 "testing"
 "time"
 "github.com/laojianzi/aster/internal/execsession"
 "github.com/laojianzi/aster/internal/kube"
 "github.com/laojianzi/aster/internal/testcluster"
)
func TestRealPodCommandExitOutputAndNoImplicitShell(t *testing.T){
 f:=testcluster.NewHTTPPod(t)
 b,err:=kube.New(f.Config);if err!=nil{t.Fatal(err)}
 ctx,cancel:=context.WithTimeout(context.Background(),90*time.Second);defer cancel()
 run:=func(argv []string,limit int)(execsession.Result,execsession.Snapshot,error){t.Helper();out,_:=execsession.NewOutput(limit,nil);r,e:=b.RunPodCommand(ctx,f.Target("exec"),execsession.Command{Container:"http",Argv:argv},out);return r,out.Snapshot(),e}
 result,snap,err:=run([]string{"/bin/sh","-c","printf 'aster-exec-out'; printf 'aster-exec-err' >&2; exit 7"},4096)
 if err==nil || result.State!=execsession.Failed || !result.ExitKnown || result.ExitCode!=7 || snap.Stdout!="aster-exec-out" || snap.Stderr!="aster-exec-err" {t.Fatalf("%+v %+v %v",result,snap,err)}
 literal:="; touch /work/should-not-exist"
 result,snap,err=run([]string{"printf","%s",literal},4096)
 if err!=nil || !result.ExitKnown || result.ExitCode!=0 || snap.Stdout!=literal {t.Fatal(result,snap,err)}
 result,_,err=run([]string{"test","!","-e","/work/should-not-exist"},4096)
 if err!=nil || !result.ExitKnown || result.ExitCode!=0 {t.Fatal("arguments were reinterpreted by a shell",result,err)}
 result,snap,err=run([]string{"/bin/sh","-c","head -c 32768 /dev/zero"},1024)
 if !errors.Is(err,execsession.ErrOutputLimit) || result.State!=execsession.OutputLimited || result.ExitKnown || !snap.Limited || len(snap.Stdout)+len(snap.Stderr)>1024 {t.Fatal("output bound failed",result,len(snap.Stdout),err)}
}
func TestRealPodCommandCancellationDoesNotInventAnExitCode(t *testing.T){
 f:=testcluster.NewHTTPPod(t);b,err:=kube.New(f.Config);if err!=nil{t.Fatal(err)}
 ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
 out,_:=execsession.NewOutput(4096,nil)
 type completed struct{result execsession.Result;err error};done:=make(chan completed,1)
 go func(){r,e:=b.RunPodCommand(ctx,f.Target("cancel"),execsession.Command{Container:"http",Argv:[]string{"/bin/sh","-c","echo command-started; sleep 30"}},out);done<-completed{r,e}}()
 for !strings.Contains(out.Snapshot().Stdout,"command-started") {
  select{case end:=<-done:t.Fatal("command ended before test cancellation",end);case <-ctx.Done():t.Fatal("no initial output");case<-time.After(20*time.Millisecond):}
 }
 cancel()
 select{case end:=<-done:if end.err==nil || end.result.State!=execsession.Interrupted || end.result.ExitKnown{t.Fatal(end)};case<-time.After(10*time.Second):t.Fatal("local exec did not disconnect on cancellation")}
 // Deliberately do not assert that the remote sleep was killed by disconnect.
}
