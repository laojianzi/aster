package execsession

import (
 "bytes"
 "errors"
 "io"
 "strings"
 "sync"
 "sync/atomic"
 "testing"
 "time"
)

func TestArgumentsAreExplicitAndNeverShellExpanded(t *testing.T) {
 argv, err := ParseArgv(`["printf","%s","; touch /should-not-exist", "", "中文"]`)
 if err != nil || len(argv) != 5 || argv[2] != "; touch /should-not-exist" || argv[3] != "" { t.Fatalf("%q %v", argv, err) }
 for _, value := range []string{`null`, `[]`, `[""]`, `["x",null]`, `"sh"`, `["x",1]`, `["x","\u0000"]`, `["x"] []`, strings.Repeat(" ", MaxArgumentBytes+1)} {
  if _, err := ParseArgv(value); err == nil { t.Errorf("accepted malformed command %q", value[:min(80,len(value))]) }
 }
 if err := (Command{Container:"http", Argv:[]string{"id"}, Timeout:MaxTimeout+time.Second}).Validate(); err == nil { t.Fatal("unbounded timeout") }
 if err := ValidateArgv(make([]string, MaxArguments+1)); err == nil { t.Fatal("unbounded argument count") }
}
func TestOutputSharedBudgetStopsAndDoesNotAlias(t *testing.T) {
 var stopped atomic.Int32
 out, err := NewOutput(5, func(e error) { if !errors.Is(e, ErrOutputLimit) { t.Error(e) }; stopped.Add(1) })
 if err != nil { t.Fatal(err) }
 _, _ = out.Stdout().Write([]byte("abc"))
 first := out.Snapshot()
 n, err := out.Stderr().Write([]byte("12345"))
 last := out.Snapshot()
 if n != 2 || !errors.Is(err,ErrOutputLimit) || last.Stdout != "abc" || last.Stderr != "12" || !last.Limited || stopped.Load()!=1 || first.Stderr!="" { t.Fatalf("%d %v %+v",n,err,last) }
 if _,err = out.Stdout().Write([]byte("x")); !errors.Is(err,ErrOutputLimit) { t.Fatal(err) }
 if stopped.Load()!=1 { t.Fatal("repeated overflow callback") }
 out.Close()
 if _,err = out.Stdout().Write([]byte("x")); !errors.Is(err,io.ErrClosedPipe) { t.Fatal(err) }
}
func TestConcurrentOutputIsBounded(t *testing.T) {
 out,_ := NewOutput(4096,nil)
 var group sync.WaitGroup
 for range 16 { group.Add(1); go func(){defer group.Done(); for range 100 { _,_=out.Stdout().Write(bytes.Repeat([]byte("x"),64)); _=out.Snapshot() }}() }
 group.Wait()
 if got:=out.Snapshot();len(got.Stdout)+len(got.Stderr)!=4096 || !got.Limited { t.Fatal("output budget was not enforced") }
}
func TestRemoteOutputDoesNotCarryTerminalEffects(t *testing.T) {
 raw := "hello\x1b]52;c;c2VjcmV0\x07\u202E.txt\r\n中文\t" + string([]byte{255})
 display := DisplayText(raw)
 if strings.ContainsAny(display,"\x1b\x07\r\u202E") || !strings.Contains(display,"中文\t") { t.Fatalf("unsafe display %q",display) }
 for _, state := range []State{Interrupted,OutputLimited,Unknown} {
  result:=Result{State:state,ExitCode:-1}
  if strings.Contains(result.String(),"exit code 0") || !strings.Contains(result.String(),"may still be running") {t.Fatal(result)}
 }
}
