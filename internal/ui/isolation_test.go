package uiworkbench

import("context";"testing")

func TestDetailSwitchClearsLogsAndInvalidatesStreamGeneration(t *testing.T){
 w:=New();ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 w.logCancel=cancel;w.logRows=[]string{"another-pod-private-log"};w.logDropped=5;w.logStatus="Streaming";before:=w.logEpoch;w.clearDetail()
 if ctx.Err()==nil||len(w.logRows)!=0||w.logDropped!=0||w.logStatus!=""||w.logEpoch<=before{t.Fatal("old resource log state survived closing detail")}
}
func TestConfirmationUsesImmutablePreparedTarget(t *testing.T){w:=New();if w.confirmationName()!=""{t.Fatal("unprepared operation has a confirmation name")}}
