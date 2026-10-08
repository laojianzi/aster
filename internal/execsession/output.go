package execsession

import (
 "errors"
 "io"
 "strings"
 "sync"
 "unicode"
)

var ErrOutputLimit = errors.New("combined command output exceeded its byte budget")

// Output has one byte budget shared by stdout and stderr. Overflow disconnects
// the command instead of silently dropping bytes. The stop callback must be
// non-blocking; context.CancelCauseFunc is suitable. Snapshot never aliases data.
type Output struct {
 mu sync.Mutex
 stdout, stderr []byte
 limit int
 exceeded bool
 closed bool
 revision uint64
 stop func(error)
}
type Snapshot struct {
 Stdout, Stderr string
 Limited bool
 Revision uint64
}
func NewOutput(limit int, stop func(error)) (*Output, error) {
 if limit < 1 || limit > MaxOutputLimit { return nil, errors.New("output budget must be between 1 byte and 2 MiB") }
 return &Output{limit: limit, stop: stop}, nil
}
func (o *Output) Stdout() io.Writer { return outputWriter{o: o} }
func (o *Output) Stderr() io.Writer { return outputWriter{o: o, stderr: true} }
func (o *Output) Snapshot() Snapshot {
 o.mu.Lock(); defer o.mu.Unlock()
 return Snapshot{string(o.stdout), string(o.stderr), o.exceeded, o.revision}
}
// Close prevents late protocol copier goroutines from changing final output.
func (o *Output) Close() { o.mu.Lock(); o.closed = true; o.mu.Unlock() }
func (o *Output) Limit() int { return o.limit }

type outputWriter struct { o *Output; stderr bool }
func (w outputWriter) Write(p []byte) (int, error) {
 o := w.o
 o.mu.Lock()
 if o.closed { o.mu.Unlock(); return 0, io.ErrClosedPipe }
 if o.exceeded { o.mu.Unlock(); return 0, ErrOutputLimit }
 available := o.limit - len(o.stdout) - len(o.stderr)
 n := min(len(p), available)
 if n > 0 {
  if w.stderr { o.stderr = append(o.stderr, p[:n]...) } else { o.stdout = append(o.stdout, p[:n]...) }
  o.revision++
 }
 over := n < len(p)
 if over { o.exceeded = true; o.revision++ }
 stop := o.stop
 o.mu.Unlock()
 if over {
  if stop != nil { stop(ErrOutputLimit) }
  return n, ErrOutputLimit
 }
 return n, nil
}

// DisplayText does not interpret ANSI, OSC, terminal title, hyperlink or
// clipboard instructions. Control/format characters become visible markers.
// The original byte streams remain available in Snapshot for backend tests.
func DisplayText(s string) string {
 var out strings.Builder
 for _, r := range strings.ToValidUTF8(s, "\uFFFD") {
  if r == '\n' || r == '\t' || (!unicode.IsControl(r) && !unicode.Is(unicode.Cf, r)) {
   out.WriteRune(r)
  } else {
   out.WriteString("�")
  }
 }
 return out.String()
}
func (s Snapshot) Display() string {
 text := "STDOUT\n" + DisplayText(s.Stdout) + "\n\nSTDERR\n" + DisplayText(s.Stderr)
 if s.Limited { text += "\n\n[Output limit reached; the connection was closed, remote termination is not confirmed.]" }
 return text
}
