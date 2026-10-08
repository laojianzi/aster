// Package execsession defines the bounded, non-interactive command contract.
// It never starts a local process or interprets a shell command string.
package execsession

import (
 "encoding/json"
 "errors"
 "fmt"
 "strings"
 "time"
 "unicode/utf8"
)

const (
 MaxArgumentBytes = 16 << 10
 MaxArguments = 64
 DefaultTimeout = 2 * time.Minute
 MaxTimeout = 5 * time.Minute
 DefaultOutputLimit = 128 << 10
 MaxOutputLimit = 2 << 20
)

type Command struct {
 Container string
 Argv []string
 Timeout time.Duration
}

// ParseArgv deliberately accepts JSON, not shell syntax. An explicit
// ["/bin/sh", "-c", "..."] is required for pipelines or shell expansion.
func ParseArgv(text string) ([]string, error) {
 if len(text) > MaxArgumentBytes || !utf8.ValidString(text) { return nil, errors.New("command must be valid UTF-8 JSON within 16 KiB") }
 var raw []json.RawMessage
 if err := json.Unmarshal([]byte(text), &raw); err != nil { return nil, errors.New("command must be a JSON array of strings") }
 argv := make([]string,len(raw))
 for i, value := range raw {
  if len(value)==0 || value[0]!='"' { return nil,errors.New("every argument must be a JSON string") }
  if err:=json.Unmarshal(value,&argv[i]);err!=nil{return nil,errors.New("invalid string argument")}
 }
 if err := ValidateArgv(argv); err != nil { return nil, err }
 return argv, nil
}
func ValidateArgv(argv []string) error {
 if len(argv) == 0 || len(argv) > MaxArguments || strings.TrimSpace(argv[0]) == "" { return errors.New("command requires an executable and at most 64 arguments") }
 size := 0
 for _, arg := range argv {
  size += len(arg)
  if size > MaxArgumentBytes || strings.ContainsRune(arg, 0) || !utf8.ValidString(arg) { return errors.New("arguments must be valid UTF-8, contain no NUL and fit within 16 KiB") }
 }
 return nil
}
func (c Command) Validate() error {
 if c.Container == "" || len(c.Container) > 253 || strings.ContainsAny(c.Container, "\x00/\n\r") { return errors.New("select an explicit container") }
 if c.Timeout < 0 || c.Timeout > MaxTimeout { return fmt.Errorf("command timeout must be within %s", MaxTimeout) }
 return ValidateArgv(c.Argv)
}

type State string
const (
 Rejected State = "Rejected"
 Succeeded State = "Succeeded"
 Failed State = "Exited with failure"
 Interrupted State = "Disconnected"
 OutputLimited State = "Output limit reached"
 Unknown State = "Outcome unknown"
)
// ExitKnown is true only after a remote status/exit result was received.
// In particular, cancel, timeout and transport loss do not mean exit code zero.
type Result struct {
 State State
 ExitKnown bool
 ExitCode int
}
func (r Result) String() string {
 if r.ExitKnown { return fmt.Sprintf("%s · remote exit code %d", r.State, r.ExitCode) }
 if r.State == Rejected { return "Rejected before a successful command session was established" }
 return string(r.State) + " · remote exit not confirmed; the process may still be running"
}
