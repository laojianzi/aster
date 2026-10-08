package uiworkbench

import (
 "context"
 "sync/atomic"
 "time"
 "github.com/laojianzi/aster/internal/logbuffer"
 "github.com/laojianzi/aster/internal/operation"
)
func(w *Workbench)stopLogs(){w.logEpoch++;if w.logCancel!=nil{w.logCancel();w.logCancel=nil}}
func(w *Workbench)startLogs(previous,follow bool){
 if w.detail==nil||w.backend==nil||w.detailKind.GVR.Resource!="pods"{return};w.stopLogs()
 epoch,revision:=w.detailEpoch,w.logEpoch;backend,target,container:=w.backend,w.target(),w.container
 ctx,cancel:=context.WithCancel(w.operationContext());w.logCancel=cancel;w.logRows=nil;w.logDropped=0;w.logStatus="Connecting"
 buffer:=logbuffer.New(4000,4<<20);done:=make(chan struct{})
 w.run(func(context.Context){
  defer close(done);check,cancelCheck:=context.WithTimeout(ctx,15*time.Second);obj,err:=backend.GetObject(check,target.GVR,target.Namespace,target.Name);cancelCheck()
  if err==nil&&obj.GetUID()!=target.UID{err=operation.ErrConflict}
  // Logs are name-addressed by Kubernetes: preflight rejects known replacements,
  // but cannot provide an atomic UID precondition on the log subresource.
  if err==nil{err=backend.ReadLogs(ctx,target.Namespace,target.Name,container,previous,follow,500,func(line string)error{buffer.Append(line);return nil})}
  w.emit(func(){if epoch!=w.detailEpoch||revision!=w.logEpoch||ctx.Err()!=nil{return};if err!=nil{w.logStatus="Stream ended: "+err.Error()}else{w.logStatus="End of retained logs"}})
 })
 w.run(func(context.Context){
  ticker:=time.NewTicker(100*time.Millisecond);defer ticker.Stop();finished:=false;end:=done;var last uint64;var pending atomic.Bool
  for{select{case<-ctx.Done():return;case<-end:finished=true;end=nil;case<-ticker.C:
   if !pending.CompareAndSwap(false,true){continue};lines,dropped,rev:=buffer.Snapshot();if rev==last{pending.Store(false);if finished{return};continue};last=rev
   w.emit(func(){defer pending.Store(false);if epoch!=w.detailEpoch||revision!=w.logEpoch||ctx.Err()!=nil{return};w.logRows=lines;w.logDropped=dropped;if w.logStatus=="Connecting"{w.logStatus="Streaming"}})
   if finished{return}
  }}
 })
}
