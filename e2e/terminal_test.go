//go:build e2e

package e2e

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/execsession"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	"github.com/laojianzi/aster/internal/ttysession"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func ttyOutput(t *testing.T, s *ttysession.Session) (*execsession.Output, <-chan struct{}) {
	t.Helper()
	out, err := execsession.NewOutput(64<<10, func(error) { s.Close() })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.Copy(out.Stdout(), s) }()
	t.Cleanup(func() {
		s.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("terminal reader failed to join")
		}
	})
	return out, done
}
func waitTTY(t *testing.T, ctx context.Context, predicate func() bool) {
	t.Helper()
	for !predicate() {
		select {
		case <-ctx.Done():
			t.Fatal("terminal condition timed out")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func TestRealTTYInteractiveInputResizeAndExit(t *testing.T) {
	f := testcluster.NewHTTPPod(t)
	b, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	script := `test -t 0 || exit 91; printf 'ASTER-TTY-READY\n'; while IFS= read -r cmd; do case "$cmd" in size) stty size;; unicode) printf '终端输入已处理\n';; quit) exit 7;; esac; done`
	s, err := b.OpenPodTerminal(ctx, f.Target("tty-interactive"), "http", []string{"/bin/sh", "-c", script}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, drained := ttyOutput(t, s)
	waitTTY(t, ctx, func() bool { return strings.Contains(out.Snapshot().Stdout, "ASTER-TTY-READY") })
	if err := s.Resize(100, 40); err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(out.Snapshot().Stdout, "40 100") {
		if _, err := s.Write([]byte("size\n")); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("resize did not reach remote TTY")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if _, err := s.Write([]byte("unicode\n")); err != nil {
		t.Fatal(err)
	}
	waitTTY(t, ctx, func() bool { return strings.Contains(out.Snapshot().Stdout, "终端输入已处理") })
	if _, err := s.Write([]byte("quit\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-ctx.Done():
		t.Fatal("remote terminal did not exit")
	}
	<-drained
	if r := s.Outcome(); !r.Result.ExitKnown || r.Result.ExitCode != 7 || r.Result.State != execsession.Failed || r.Err == nil || out.Snapshot().Limited {
		t.Fatal(r)
	}
}
func TestRealTTYCancelAndLeaseDoNotInventExit(t *testing.T) {
	f := testcluster.NewHTTPPod(t)
	b, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		lease time.Duration
	}{{"cancel", 30 * time.Second}, {"lease", 3 * time.Second}} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			s, err := b.OpenPodTerminal(ctx, f.Target("tty-cancel"), "http", []string{"/bin/sh", "-c", "printf 'ASTER-TTY-ESTABLISHED\\n'; sleep 30"}, test.lease)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			out, _ := ttyOutput(t, s)
			waitTTY(t, ctx, func() bool { return strings.Contains(out.Snapshot().Stdout, "ASTER-TTY-ESTABLISHED") })
			if test.name == "cancel" {
				s.Close()
			}
			select {
			case <-s.Done():
			case <-time.After(8 * time.Second):
				t.Fatal("terminal did not disconnect")
			}
			if o := s.Outcome(); o.Result.ExitKnown || o.Result.State != execsession.Interrupted || o.Err == nil {
				t.Fatal(o)
			}
			if ctx.Err() != nil {
				t.Fatal("parent deadline, not terminal lease, ended the test")
			}
		})
	}
}
func TestRealTTYReadOnlyRBACPreventsExecution(t *testing.T) {
	f := testcluster.NewHTTPPod(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	const user = "aster-tty-reader"
	_, err := f.Client.RbacV1().Roles(f.Pod.Namespace).Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: user}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Client.RbacV1().RoleBindings(f.Pod.Namespace).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: user}, Subjects: []rbacv1.Subject{{Kind: "User", Name: user}}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: user}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := rest.CopyConfig(f.Config)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	waitTTY(t, ctx, func() bool {
		_, err := client.CoreV1().Pods(f.Pod.Namespace).Get(ctx, f.Pod.Name, metav1.GetOptions{})
		return err == nil
	})
	reader, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := reader.OpenPodTerminal(ctx, f.Target("denied-tty"), "http", []string{"/bin/sh", "-c", "touch /work/denied-tty-ran"}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, drained := ttyOutput(t, s)
	select {
	case <-s.Done():
	case <-ctx.Done():
		t.Fatal("denial did not complete")
	}
	<-drained
	if o := s.Outcome(); o.Result.State != execsession.Rejected || o.Result.ExitKnown || o.Err == nil || out.Snapshot().Stdout != "" {
		t.Fatal(o)
	}
	admin, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	check, _ := execsession.NewOutput(4096, nil)
	result, err := admin.RunPodCommand(ctx, f.Target("check-tty-denial"), execsession.Command{Container: "http", Argv: []string{"test", "!", "-e", "/work/denied-tty-ran"}}, check)
	if err != nil || !result.ExitKnown || result.ExitCode != 0 {
		t.Fatal("denied TTY command executed", result, err)
	}
}
