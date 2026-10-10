package credentialvault

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/laojianzi/aster/internal/credentialexec"
	strictjson "sigs.k8s.io/json"
)

const HelperFlag = "--aster-vault-helper"

var admission = make(chan struct{}, 2)

type request struct {
	Operation string `json:"operation"`
	Key       string `json:"key"`
	Value     []byte `json:"value,omitempty"`
}
type response struct {
	Code  string `json:"code,omitempty"`
	Value []byte `json:"value,omitempty"`
}
type Client struct{}

func (Client) Put(ctx context.Context, k string, v []byte) error {
	_, e := invoke(ctx, request{"put", k, v})
	return e
}
func (Client) Get(ctx context.Context, k string) ([]byte, error) {
	return invoke(ctx, request{Operation: "get", Key: k})
}
func (Client) Delete(ctx context.Context, k string) error {
	_, e := invoke(ctx, request{Operation: "delete", Key: k})
	return e
}
func helperEnvironment() []string {
	names := []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR", "LANG", "LC_ALL", "DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "DISPLAY", "WAYLAND_DISPLAY"}
	env := []string{"GOTRACEBACK=none"}
	for _, n := range names {
		if v, ok := os.LookupEnv(n); ok && len(v) < 4096 && !strings.ContainsRune(v, 0) {
			env = append(env, n+"="+v)
		}
	}
	return env
}
func validRequest(r request) bool {
	return validKey(r.Key) && ((r.Operation == "put" && len(r.Value) > 0 && len(r.Value) <= MaxRecordBytes) || ((r.Operation == "get" || r.Operation == "delete") && len(r.Value) == 0))
}
func invoke(ctx context.Context, r request) ([]byte, error) {
	if !validRequest(r) {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case admission <- struct{}{}:
		defer func() { <-admission }()
	default:
		return nil, ErrBusy
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, ErrUnavailable
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > 4096 {
		return nil, ErrInvalid
	}
	defer clear(data)
	raw, err := credentialexec.RunHelper(ctx, exe, []string{HelperFlag}, helperEnvironment(), data, 20*time.Second)
	if err != nil {
		if r.Operation != "get" {
			return nil, ErrUncertain
		}
		return nil, ErrUnavailable
	}
	defer clear(raw)
	var result response
	warnings, e := strictjson.UnmarshalStrict(raw, &result)
	defer clear(result.Value)
	if e != nil || len(warnings) > 0 || len(result.Value) > MaxRecordBytes {
		if r.Operation != "get" {
			return nil, ErrUncertain
		}
		return nil, ErrFailed
	}
	switch result.Code {
	case "":
		if r.Operation != "get" && len(result.Value) > 0 {
			return nil, ErrFailed
		}
		return append([]byte(nil), result.Value...), nil
	case "missing":
		return nil, ErrNotFound
	case "unavailable":
		return nil, ErrUnavailable
	default:
		if r.Operation != "get" {
			return nil, ErrUncertain
		}
		return nil, ErrFailed
	}
}

// Serve is entered before any window or kubeconfig access. It accepts exactly
// one bounded request over stdin, and never emits OS error strings or secrets
// except the explicitly requested record over the private stdout pipe.
func Serve(in io.Reader, out io.Writer) int { return serve(in, out, native) }
func serve(in io.Reader, out io.Writer, execute func(request) ([]byte, error)) (exit int) {
	defer func() {
		if recover() != nil {
			exit = 1
		}
	}()
	data, err := io.ReadAll(io.LimitReader(in, 4097))
	if err != nil || len(data) > 4096 {
		return 1
	}
	defer clear(data)
	var r request
	warnings, e := strictjson.UnmarshalStrict(data, &r)
	if e != nil || len(warnings) > 0 || !validRequest(r) {
		return 1
	}
	defer clear(r.Value)
	value, err := execute(r)
	defer clear(value)
	result := response{Value: value}
	switch err {
	case nil:
	case ErrNotFound:
		result = response{Code: "missing"}
	case ErrUnavailable:
		result = response{Code: "unavailable"}
	default:
		result = response{Code: "failed"}
	}
	if json.NewEncoder(out).Encode(result) != nil {
		return 1
	}
	return 0
}
