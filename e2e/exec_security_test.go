//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/execsession"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestRealPodCommandRejectsReadOnlyIdentity(t *testing.T) {
	f := testcluster.NewHTTPPod(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	const user = "aster-command-reader"
	_, err := f.Client.RbacV1().Roles(f.Pod.Namespace).Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: user},
		Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}}},
	}, metav1.CreateOptions{})
	if err != nil { t.Fatal(err) }
	_, err = f.Client.RbacV1().RoleBindings(f.Pod.Namespace).Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: user},
		Subjects: []rbacv1.Subject{{Kind: "User", Name: user}},
		RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: user},
	}, metav1.CreateOptions{})
	if err != nil { t.Fatal(err) }
	cfg := rest.CopyConfig(f.Config)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil { t.Fatal(err) }
	for {
		_, err = client.CoreV1().Pods(f.Pod.Namespace).Get(ctx, f.Pod.Name, metav1.GetOptions{})
		if err == nil { break }
		select { case <-ctx.Done(): t.Fatalf("Pod read permission did not propagate: %v", err); case <-time.After(100*time.Millisecond): }
	}
	backend, err := kube.New(cfg)
	if err != nil { t.Fatal(err) }
	output, _ := execsession.NewOutput(4096, nil)
	result, err := backend.RunPodCommand(ctx, f.Target("restricted-exec"), execsession.Command{
		Container: "http", Argv: []string{"/bin/sh", "-c", "echo denied-command-ran; touch /work/denied-command-ran"},
	}, output)
	if err == nil || result.State != execsession.Rejected || result.ExitKnown {
		t.Fatalf("read-only identity was not rejected: %+v %v", result, err)
	}
	if s := output.Snapshot(); s.Stdout != "" || s.Stderr != "" { t.Fatal("rejected command produced output") }
	// An independent authorized session checks that the forbidden operation did not run.
	admin, err := kube.New(f.Config)
	if err != nil { t.Fatal(err) }
	check, _ := execsession.NewOutput(4096, nil)
	result, err = admin.RunPodCommand(ctx, f.Target("verify-denial"), execsession.Command{
		Container: "http", Argv: []string{"test", "!", "-e", "/work/denied-command-ran"},
	}, check)
	if err != nil || !result.ExitKnown || result.ExitCode != 0 { t.Fatal("forbidden command modified the Pod", result, err) }
}

func TestRealPodCommandDeadlineDoesNotClaimTermination(t *testing.T) {
	f := testcluster.NewHTTPPod(t)
	backend, err := kube.New(f.Config)
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, _ := execsession.NewOutput(4096, nil)
	result, err := backend.RunPodCommand(ctx, f.Target("exec-deadline"), execsession.Command{
		Container: "http", Argv: []string{"/bin/sh", "-c", "echo deadline-command-started; sleep 30"}, Timeout: 3*time.Second,
	}, output)
	if !strings.Contains(output.Snapshot().Stdout, "deadline-command-started") { t.Fatal("deadline test did not reach an established command") }
	if err == nil || result.State != execsession.Interrupted || result.ExitKnown || ctx.Err() != nil {
		t.Fatalf("command deadline did not disconnect with unknown remote exit: %+v %v", result, err)
	}
}
