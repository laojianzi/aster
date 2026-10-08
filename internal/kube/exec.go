package kube

import (
 "context"
 "errors"
 "fmt"
 "io"
 "net/http"
 "sync/atomic"
 "time"

 "github.com/laojianzi/aster/internal/execsession"
 "github.com/laojianzi/aster/internal/resource"
 corev1 "k8s.io/api/core/v1"
 apierrors "k8s.io/apimachinery/pkg/api/errors"
 metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
 protocol "k8s.io/apimachinery/pkg/util/remotecommand"
 "k8s.io/client-go/kubernetes/scheme"
 "k8s.io/client-go/tools/remotecommand"
 "k8s.io/client-go/transport/spdy"
)

// RunPodCommand runs a non-interactive argv in an explicitly selected running
// container. There is no implicit shell, stdin, PTY, reconnect or write retry.
// The caller owns output's cancel callback and must keep it non-blocking.
// Kubernetes exec is addressed by Pod name; the preflight UID check cannot be
// made atomic with the API server's upgrade or pin a container restart.
func (b *Backend) RunPodCommand(ctx context.Context, target resource.Identity, cmd execsession.Command, output *execsession.Output) (execsession.Result, error) {
 result := execsession.Result{State:execsession.Rejected,ExitCode:-1}
 if err := validatePodTarget(target); err != nil { return result,err }
 if err := cmd.Validate(); err != nil { return result,err }
 if output == nil { return result,errors.New("command requires bounded output") }
 defer output.Close()
 cmd.Argv = append([]string(nil),cmd.Argv...)
 if err := ctx.Err(); err != nil { return result,err }
 duration := cmd.Timeout; if duration==0 { duration=execsession.DefaultTimeout }
 ctx,cancel := context.WithTimeout(ctx,duration); defer cancel()
 checkCtx, checkCancel := context.WithTimeout(ctx,15*time.Second)
 pod,err := b.typed.CoreV1().Pods(target.Namespace).Get(checkCtx,target.Name,metav1.GetOptions{})
 checkCancel()
 if err != nil { return result,err }
 if pod.UID != target.UID { return result,errors.New("pod was replaced; refresh before executing a command") }
 if pod.DeletionTimestamp!=nil || pod.Status.Phase!=corev1.PodRunning { return result,errors.New("command requires a running, non-terminating pod") }
 running:=false
 for _, statuses:=range [][]corev1.ContainerStatus{pod.Status.ContainerStatuses,pod.Status.InitContainerStatuses,pod.Status.EphemeralContainerStatuses} {
  for _,status:=range statuses { if status.Name==cmd.Container && status.State.Running!=nil { running=true } }
 }
 if !running { return result,errors.New("selected container is not running; refresh the resource") }
 cfg:=copyConnectionConfig(b.config)
 // The explicit operation deadline and handshake budget own these lifetimes.
 cfg.Timeout=0
 var upgraded atomic.Bool
 cfg.Wrap(func(base http.RoundTripper) http.RoundTripper { return execHandshake{sessionHandshake{parent:ctx,base:base},&upgraded} })
 url:=b.typed.CoreV1().RESTClient().Post().Resource("pods").Namespace(target.Namespace).Name(target.Name).SubResource("exec").VersionedParams(&corev1.PodExecOptions{Container:cmd.Container,Command:cmd.Argv,Stdout:true,Stderr:true},scheme.ParameterCodec).URL()
 primary,err:=remotecommand.NewWebSocketExecutorForProtocols(cfg,http.MethodGet,url.String(),protocol.StreamProtocolV5Name)
 if err!=nil { return result,err }
 transport,upgrader,err:=spdy.RoundTripperFor(cfg); if err!=nil { return result,err }
 // v1-v3 cannot reliably report a remote exit status. Do not negotiate them.
 secondary,err:=remotecommand.NewSPDYExecutorForProtocols(transport,upgrader,http.MethodPost,url,protocol.StreamProtocolV5Name,protocol.StreamProtocolV4Name)
 if err!=nil { return result,err }
 executor,err:=remotecommand.NewFallbackExecutor(primary,secondary,func(err error)bool{return !upgraded.Load() && canFallbackStreaming(err)})
 if err!=nil { return result,err }
 err=executor.StreamWithContext(ctx,remotecommand.StreamOptions{Stdout:commandWriter{output.Stdout(),cancel},Stderr:commandWriter{output.Stderr(),cancel}})
 // No output writer can append after this snapshot, including a late copier.
 output.Close()
 return commandResult(ctx,output.Snapshot().Limited,upgraded.Load(),err)
}

type execHandshake struct { base sessionHandshake; upgraded *atomic.Bool }
func (t execHandshake) RoundTrip(req *http.Request)(*http.Response,error){
 response,err:=t.base.RoundTrip(req)
 if response!=nil {
  if response.StatusCode==http.StatusSwitchingProtocols {t.upgraded.Store(true)}
  if response.StatusCode>=300 && response.StatusCode<400 {if response.Body!=nil{response.Body.Close()};return nil,errors.New("exec redirects are not permitted")}
 }
 return response,err
}

type remoteExit interface{ ExitStatus() int }
func commandResult(ctx context.Context,limited,upgraded bool,err error)(execsession.Result,error){
 result:=execsession.Result{State:execsession.Unknown,ExitCode:-1}
 if limited {result.State=execsession.OutputLimited;return result,execsession.ErrOutputLimit}
 if err==nil {result.State=execsession.Succeeded;result.ExitKnown=true;result.ExitCode=0;return result,nil}
 var exit remoteExit
 if errors.As(err,&exit){result.State=execsession.Failed;result.ExitKnown=true;result.ExitCode=exit.ExitStatus();return result,fmt.Errorf("remote command exited with code %d",result.ExitCode)}
 if ctx.Err()!=nil || errors.Is(err,context.Canceled) || errors.Is(err,context.DeadlineExceeded) {result.State=execsession.Interrupted;if ctx.Err()!=nil{return result,ctx.Err()};return result,err}
 cause:=streamingCause(err)
 if !upgraded && (apierrors.IsForbidden(cause)||apierrors.IsUnauthorized(cause)||apierrors.IsNotFound(cause)||apierrors.IsBadRequest(cause)){result.State=execsession.Rejected}
 // Avoid including an exec URL (arguments may contain credentials) or an
 // untrusted server response body in UI errors, logs or history.
 return result,errors.New(result.String())
}

// Cancel even if an upstream stream copier does not propagate its write error.
type commandWriter struct { destination io.Writer; cancel context.CancelFunc }
func (w commandWriter) Write(p []byte)(int,error){
 n,err:=w.destination.Write(p);if err!=nil {w.cancel()};return n,err
}
