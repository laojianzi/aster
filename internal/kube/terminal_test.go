package kube

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/laojianzi/aster/internal/execsession"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestTTYProtocolFailuresNeverReplayOrLeakArguments(t *testing.T) {
	for _, mode := range []string{"forbidden", "redirect", "bad-protocol", "missing-protocol"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/namespaces/team/pods/pod" {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(runningPod())
					return
				}
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Query().Get("tty") != "true" || r.URL.Query().Get("stdin") != "true" || r.URL.Query().Get("stderr") == "true" {
					t.Error("invalid terminal protocol request")
				}
				switch mode {
				case "forbidden":
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(403)
					_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Code: 403, Reason: metav1.StatusReasonForbidden, Message: "synthetic-secret"})
				case "redirect":
					w.Header().Set("Location", "/synthetic-secret")
					w.WriteHeader(307)
				default:
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					h := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
					protocol := "v3.channel.k8s.io"
					if mode == "missing-protocol" {
						protocol = ""
					}
					_, _ = fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: %s\r\n\r\n", base64.StdEncoding.EncodeToString(h[:]), protocol)
				}
			}))
			defer server.Close()
			b, err := New(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			session, err := b.OpenPodTerminal(ctx, execTarget(), "http", []string{"echo", "synthetic-secret"}, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			_, _ = io.Copy(io.Discard, session)
			result := session.Outcome()
			if result.Err == nil || result.Result.ExitKnown || calls.Load() != 1 {
				t.Fatal(result, calls.Load())
			}
			if result.Err.Error() != result.Result.String() {
				t.Fatal("remote argument/response detail exposed", result.Err)
			}
			if mode == "forbidden" && result.Result.State != execsession.Rejected {
				t.Fatal(result)
			}
		})
	}
}
func TestTTYPreflightRejectsReplacedOrStoppedPod(t *testing.T) {
	for _, mode := range []string{"replaced", "stopped", "invalid-argv"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				pod := runningPod()
				if mode == "replaced" {
					pod.UID = "other"
				} else {
					pod.Status.ContainerStatuses = nil
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(pod)
			}))
			defer server.Close()
			b, err := New(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			argv := []string{"/bin/sh"}
			want := int32(1)
			if mode == "invalid-argv" {
				argv = nil
				want = 0
			}
			s, err := b.OpenPodTerminal(context.Background(), execTarget(), "http", argv, time.Second)
			if err == nil || s != nil || calls.Load() != want {
				t.Fatal("unsafe terminal opened", err, calls.Load())
			}
		})
	}
}
