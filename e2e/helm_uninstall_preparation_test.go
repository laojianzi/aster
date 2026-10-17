//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// All mutations are isolated fixture setup/teardown, never preparation effects.
func TestRealHelmUninstallPreparationReadOnlyProfile(t *testing.T) {
	f := testcluster.NewHelmMetadata(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	text := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: prepared-config\ndata:\n  token: PRIVATE_STORED\n"
	payload, err := json.Marshal(map[string]any{
		"name": "example", "namespace": f.Namespace, "version": 1, "info": map[string]any{"status": "deployed"},
		"chart":    map[string]any{"metadata": map[string]any{"name": "example-chart", "version": "1.2.3", "apiVersion": "v2"}},
		"manifest": text, "config": map[string]any{"token": "PRIVATE_VALUE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := f.Secrets[0].DeepCopy()
	s.Data = map[string][]byte{"release": helmfixture.Stored(payload)}
	s, err = f.Client.CoreV1().Secrets(f.Namespace).Update(ctx, s, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := f.Client.CoreV1().ConfigMaps(f.Namespace)
	cm, err := client.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "prepared-config", Labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"},
		Annotations: map[string]string{"meta.helm.sh/release-name": "example", "meta.helm.sh/release-namespace": f.Namespace}}, Data: map[string]string{"canary": "PRIVATE_LIVE"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := client.Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "preparation-owner"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := kube.New(f.Config)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := b.HelmReleaseMetadata(ctx, f.Namespace)
	if err != nil || len(snap.Revisions) != 1 {
		t.Fatal("fixture metadata", err)
	}
	row := snap.Revisions[0]
	p, err := b.PrepareHelmUninstall(ctx, f.Namespace, row, "preparation-session", true)
	if err != nil || p == nil || !p.Matches(b, "preparation-session", f.Namespace, row, time.Now()) {
		t.Fatal("valid profile", err)
	}
	if len(p.Targets()) != 1 || p.Targets()[0].UID != string(cm.UID) || p.Targets()[0].ResourceVersion != cm.ResourceVersion {
		t.Fatal("wrong actual identity")
	}
	checkUnchanged := func(t *testing.T, want *corev1.ConfigMap) {
		t.Helper()
		live, e := client.Get(ctx, want.Name, metav1.GetOptions{})
		storage, se := f.Client.CoreV1().Secrets(f.Namespace).Get(ctx, s.Name, metav1.GetOptions{})
		if e != nil || se != nil || !reflect.DeepEqual(want, live) || !reflect.DeepEqual(s, storage) {
			t.Fatal("preparation mutated fixture", e, se)
		}
	}
	checkUnchanged(t, cm)
	for _, mode := range []string{"keep", "hook", "missing-marker", "wrong-marker", "finalizer", "owner"} {
		t.Run(mode, func(t *testing.T) {
			candidate := cm.DeepCopy()
			switch mode {
			case "keep":
				candidate.Annotations["helm.sh/resource-policy"] = "keep"
			case "hook":
				candidate.Annotations["helm.sh/hook"] = "pre-delete"
			case "missing-marker":
				delete(candidate.Labels, "app.kubernetes.io/managed-by")
			case "wrong-marker":
				candidate.Annotations["meta.helm.sh/release-name"] = "other"
			case "finalizer":
				candidate.Finalizers = []string{"aster.test/hold"}
			case "owner":
				candidate.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: owner.Name, UID: owner.UID}}
			}
			updated, e := client.Update(ctx, candidate, metav1.UpdateOptions{})
			if e != nil {
				t.Fatal(e)
			}
			// Ensure even a failed assertion cannot leave fixture finalizers behind.
			defer func() {
				cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
				defer done()
				current, e := client.Get(cleanup, cm.Name, metav1.GetOptions{})
				if e != nil {
					t.Error("fixture restore GET", e)
					return
				}
				restore := cm.DeepCopy()
				restore.ResourceVersion = current.ResourceVersion
				cm, e = client.Update(cleanup, restore, metav1.UpdateOptions{})
				if e != nil {
					t.Error("fixture restore", e)
				}
			}()
			out, e := b.PrepareHelmUninstall(ctx, f.Namespace, row, "preparation-session", true)
			if e == nil || out != nil {
				t.Fatal("unsafe live profile admitted")
			}
			checkUnchanged(t, updated)
		})
	}
	t.Run("denied", func(t *testing.T) {
		cfg := rest.CopyConfig(f.Config)
		cfg.Impersonate = rest.ImpersonationConfig{UserName: "aster-preparation-no-reads"}
		denied, e := kube.New(cfg)
		if e != nil {
			t.Fatal(e)
		}
		out, e := denied.PrepareHelmUninstall(ctx, f.Namespace, row, "preparation-session", true)
		if !errors.Is(e, helmrelease.ErrForbidden) || out != nil {
			t.Fatal("read authorization not enforced", e)
		}
		checkUnchanged(t, cm)
	})
	t.Run("multiple-revisions", func(t *testing.T) {
		second := s.DeepCopy()
		second.Name = "sh.helm.release.v1.example.v2"
		second.UID = ""
		second.ResourceVersion = ""
		second.ManagedFields = nil
		second.CreationTimestamp = metav1.Time{}
		second.Labels["version"] = "2"
		if _, e := f.Client.CoreV1().Secrets(f.Namespace).Create(ctx, second, metav1.CreateOptions{}); e != nil {
			t.Fatal(e)
		}
		newSnapshot, e := b.HelmReleaseMetadata(ctx, f.Namespace)
		if e != nil || len(newSnapshot.Revisions) != 2 {
			t.Fatal(e)
		}
		for _, selected := range newSnapshot.Revisions {
			out, e := b.PrepareHelmUninstall(ctx, f.Namespace, selected, "preparation-session", true)
			if e == nil || out != nil {
				t.Fatal("multiple-revision release admitted")
			}
		}
		checkUnchanged(t, cm)
	})
}
