//go:build e2e

package e2e

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func statusWire(t *testing.T, s *corev1.Secret) ([]byte, helmrelease.Revision) {
	t.Helper()
	copy := s.DeepCopy()
	copy.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}
	wire, err := json.Marshal(copy)
	if err != nil {
		t.Fatal(err)
	}
	return wire, helmrelease.Revision{Release: "example", Number: 1, Status: s.Labels["status"], StorageName: s.Name, UID: string(s.UID), ResourceVersion: s.ResourceVersion}
}
func statusRetainedPayload(t *testing.T, s *corev1.Secret) map[string]json.RawMessage {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(string(s.Data["release"]))
	if err != nil {
		t.Fatal("storage encoding", err)
	}
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err = io.ReadAll(io.LimitReader(r, helmrelease.MaxReleaseBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(b)
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil {
		t.Fatal("release JSON")
	}
	delete(fields, "info")
	return fields
}

// Mutations below are ONLY fixture operations in the existing explicitly
// opted-in disposable kind namespace. There is no product transport call site.
func TestRealHelmStatusTransitionConditionalStorage(t *testing.T) {
	f := testcluster.NewHelmUninstallPreview(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	secrets := f.Client.CoreV1().Secrets(f.Namespace)
	before, err := secrets.Get(ctx, f.Secrets[0].Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	canary, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, "preview-config", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wire, row := statusWire(t, before)
	defer clear(wire)
	patch, err := helmrelease.BuildUninstallStatusPatch(ctx, wire, f.Namespace, row, helmrelease.BeginUninstallStatus, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer clear(patch)
	dry, err := secrets.Patch(ctx, row.StorageName, types.JSONPatchType, patch, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldValidation: "Strict"})
	if err != nil || dry.Labels["status"] != "uninstalling" || dry.UID != before.UID {
		t.Fatal("server dry-run", err)
	}
	after, err := secrets.Get(ctx, row.StorageName, metav1.GetOptions{})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("dry-run persisted", err)
	}
	for _, index := range []int{0, 1, 2, 3, 4} {
		t.Run([]string{"name", "namespace", "uid", "rv", "status"}[index], func(t *testing.T) {
			var ops []map[string]any
			if json.Unmarshal(patch, &ops) != nil {
				t.Fatal("patch")
			}
			ops[index]["value"] = "wrong"
			bad, _ := json.Marshal(ops)
			defer clear(bad)
			_, err := secrets.Patch(ctx, row.StorageName, types.JSONPatchType, bad, metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}})
			if !apierrors.IsInvalid(err) && !apierrors.IsConflict(err) {
				t.Fatal("server accepted stale precondition or unrelated failure", err)
			}
		})
	}
	t.Run("denied", func(t *testing.T) {
		cfg := rest.CopyConfig(f.Config)
		cfg.Impersonate = rest.ImpersonationConfig{UserName: "aster-status-no-patch"}
		denied, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, dryRun := range [][]string{{metav1.DryRunAll}, nil} {
			_, err := denied.CoreV1().Secrets(f.Namespace).Patch(ctx, row.StorageName, types.JSONPatchType, patch, metav1.PatchOptions{DryRun: dryRun})
			if !apierrors.IsForbidden(err) {
				t.Fatal("missing patch permission was not denied", err)
			}
		}
	})
	started, err := secrets.Patch(ctx, row.StorageName, types.JSONPatchType, patch, metav1.PatchOptions{FieldValidation: "Strict"})
	if err != nil || started.UID != before.UID || started.ResourceVersion == before.ResourceVersion || started.Labels["status"] != "uninstalling" {
		t.Fatal("conditional fixture start", err)
	}
	if !reflect.DeepEqual(statusRetainedPayload(t, before), statusRetainedPayload(t, started)) {
		t.Fatal("unrelated raw release fields lost")
	}
	if _, err := secrets.Patch(ctx, row.StorageName, types.JSONPatchType, patch, metav1.PatchOptions{}); !apierrors.IsInvalid(err) && !apierrors.IsConflict(err) {
		t.Fatal("old patch replay admitted", err)
	}
	wire2, row2 := statusWire(t, started)
	defer clear(wire2)
	finish, err := helmrelease.BuildUninstallStatusPatch(ctx, wire2, f.Namespace, row2, helmrelease.FinishUninstallStatus, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer clear(finish)
	// A concurrent unrelated metadata update changes RV. It must invalidate the
	// already encoded finish request instead of losing the new metadata.
	concurrent := started.DeepCopy()
	concurrent.Annotations = map[string]string{"example/retained": "concurrent"}
	concurrent, err = secrets.Update(ctx, concurrent, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Patch(ctx, row.StorageName, types.JSONPatchType, finish, metav1.PatchOptions{}); !apierrors.IsInvalid(err) && !apierrors.IsConflict(err) {
		t.Fatal("stale finish admitted", err)
	}
	fresh, freshRow := statusWire(t, concurrent)
	defer clear(fresh)
	finish, err = helmrelease.BuildUninstallStatusPatch(ctx, fresh, f.Namespace, freshRow, helmrelease.FinishUninstallStatus, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer clear(finish)
	finished, err := secrets.Patch(ctx, row.StorageName, types.JSONPatchType, finish, metav1.PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if finished.Labels["status"] != "uninstalled" || finished.UID != before.UID || finished.Annotations["example/retained"] != "concurrent" || !reflect.DeepEqual(statusRetainedPayload(t, before), statusRetainedPayload(t, finished)) {
		t.Fatal("retained history/payload/identity lost")
	}
	finalWire, finalRow := statusWire(t, finished)
	defer clear(finalWire)
	if _, err := helmrelease.InspectSecret(ctx, finalWire, f.Namespace, finalRow); err != nil {
		t.Fatal("final storage cannot be inspected", err)
	}
	live, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, "preview-config", metav1.GetOptions{})
	if err != nil || !reflect.DeepEqual(canary, live) {
		t.Fatal("codec fixture changed a resource", err)
	}
}
