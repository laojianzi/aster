package kube

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/laojianzi/aster/internal/execsession"
	"k8s.io/client-go/rest"
)

func TestExecUnrecognizedProtocolNeverReplaysCommand(t *testing.T) {
	for _, selected := range []string{"", "v3.channel.k8s.io"} {
		t.Run("protocol="+selected, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/namespaces/team/pods/pod" {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(runningPod())
					return
				}
				calls.Add(1)
				if r.Method != http.MethodGet {
					t.Error("command was retried via a different transport")
				}
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				// SHA-1 here is the RFC 6455 handshake, not a security digest.
				hash := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
				_, _ = fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: %s\r\n\r\n", base64.StdEncoding.EncodeToString(hash[:]), selected)
			}))
			defer server.Close()
			backend, err := New(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			output, _ := execsession.NewOutput(100, nil)
			result, err := backend.RunPodCommand(context.Background(), execTarget(), execsession.Command{Container: "http", Argv: []string{"id"}}, output)
			if err == nil || result.ExitKnown || result.State != execsession.Unknown || calls.Load() != 1 {
				t.Fatalf("unrecognized protocol was treated as safe to replay or successful: %+v %v calls=%d", result, err, calls.Load())
			}
		})
	}
}
