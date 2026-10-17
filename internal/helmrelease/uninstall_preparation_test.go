package helmrelease

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const profileManifest = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: sample\ndata:\n  password: PRIVATE_DATA\n"

func profilePayload(text string) map[string]any {
	return map[string]any{"name": "example", "namespace": "team", "version": 2,
		"info": map[string]any{"status": "deployed", "description": "PRIVATE_NOTES"},
		"chart": map[string]any{"metadata": map[string]any{"name": "example-chart", "version": "1.2.3", "apiVersion": "v2", "type": "application"},
			"values":    map[string]any{"password": "PRIVATE_VALUE"},
			"files":     []any{map[string]any{"name": "README.md", "data": base64.StdEncoding.EncodeToString([]byte("PRIVATE_README"))}},
			"templates": []any{map[string]any{"name": "templates/target.yaml", "data": base64.StdEncoding.EncodeToString([]byte(text))}}},
		"manifest": text, "config": map[string]any{"password": "PRIVATE_CONFIG"}, "hooks": []any{}}
}
func profileWire(t *testing.T, doc map[string]any, modify func(*corev1.Secret)) []byte {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	s := inspectionSecret()
	s.Data["release"] = helmfixture.Stored(b)
	if modify != nil {
		modify(s)
	}
	return inspectionWire(s)
}
func checkedProfile(t *testing.T, text string, now time.Time) *UninstallProfile {
	t.Helper()
	p, err := InspectUninstallProfile(context.Background(), profileWire(t, profilePayload(text), nil), "team", inspectionSelection(), "session", now)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHelmUninstallProfileFullContent(t *testing.T) {
	for _, text := range []string{profileManifest, strings.ReplaceAll(profileManifest, "\n", "\r\n"),
		profileManifest + "binaryData:\n  other: YQ==\nimmutable: true\n", strings.Replace(profileManifest, "  name: sample", "  name: sample\n  namespace: team", 1)} {
		now := time.Now()
		wire := profileWire(t, profilePayload(text), nil)
		original := bytes.Clone(wire)
		p, err := InspectUninstallProfile(context.Background(), wire, "team", inspectionSelection(), "session", now)
		if err != nil || p == nil {
			t.Fatal(err)
		}
		if !bytes.Equal(wire, original) || len(p.Preview().Items()) != 1 || p.Preview().Summary().HookCount != 0 ||
			p.Preview().ExpiresAt() != now.Add(PreparationLifetime) || len(p.Preview().ID()) != 32 {
			t.Fatal("input/lifetime/projection contract")
		}
		serialized, _ := json.Marshal(p)
		if strings.Contains(string(serialized), "PRIVATE") {
			t.Fatal("private payload escaped")
		}
		p.Preview().Items()[0].Name = "tampered"
		if p.Preview().Items()[0].Name != "sample" {
			t.Fatal("aliased stored item")
		}
	}
}

func TestHelmUninstallProfileRejectsUnsupportedContent(t *testing.T) {
	cases := map[string]func(map[string]any){
		"hooks":                 func(d map[string]any) { d["hooks"] = []any{map[string]any{"name": "PRIVATE_HOOK"}} },
		"unknown-release-field": func(d map[string]any) { d["future_execution"] = true },
		"deleted":               func(d map[string]any) { d["info"].(map[string]any)["deleted"] = "2026-01-01T00:00:00Z" },
		"deleted-alias":         func(d map[string]any) { d["info"].(map[string]any)["Deleted"] = nil },
		"null-description":      func(d map[string]any) { d["info"].(map[string]any)["description"] = nil },
		"dependencies": func(d map[string]any) {
			d["chart"].(map[string]any)["metadata"].(map[string]any)["dependencies"] = []any{map[string]any{"name": "child"}}
		},
		"dependencies-alias": func(d map[string]any) {
			d["chart"].(map[string]any)["metadata"].(map[string]any)["Dependencies"] = []any{}
		},
		"lock":                func(d map[string]any) { d["chart"].(map[string]any)["lock"] = map[string]any{} },
		"unknown-chart-field": func(d map[string]any) { d["chart"].(map[string]any)["dependencies"] = []any{} },
		"library":             func(d map[string]any) { d["chart"].(map[string]any)["metadata"].(map[string]any)["type"] = "library" },
		"chart-policy": func(d map[string]any) {
			d["chart"].(map[string]any)["metadata"].(map[string]any)["annotations"] = map[string]string{"helm.sh/resource-policy": "keep"}
		},
		"files-alias":     func(d map[string]any) { d["chart"].(map[string]any)["Files"] = nil },
		"null-file":       func(d map[string]any) { d["chart"].(map[string]any)["files"] = []any{nil} },
		"malformed-files": func(d map[string]any) { d["chart"].(map[string]any)["files"] = map[string]any{} },
		"unknown-method":  func(d map[string]any) { d["apply_method"] = "PRIVATE_OTHER" },
	}
	for _, name := range []string{"crds/object.yaml", "CRDS/object.txt", "docs/../readme", "/absolute", "charts/child.tgz", "docs\\crds\\x", "docs//file", "docs/./file", "../README", "docs/", "templates/forged.yaml"} {
		name := name
		cases["file-path-"+name] = func(d map[string]any) {
			d["chart"].(map[string]any)["files"] = []any{map[string]any{"name": name, "data": "YQ=="}}
		}
	}
	cases["malformed-file-base64"] = func(d map[string]any) {
		d["chart"].(map[string]any)["files"] = []any{map[string]any{"name": "README", "data": "PRIVATE!"}}
	}
	cases["duplicate-file"] = func(d map[string]any) {
		f := map[string]any{"name": "README", "data": "YQ=="}
		d["chart"].(map[string]any)["files"] = []any{f, f}
	}
	cases["file-count"] = func(d map[string]any) {
		f := make([]any, MaxPreviewSegments+1)
		for i := range f {
			f[i] = map[string]any{"name": fmt.Sprintf("doc%d", i), "data": ""}
		}
		d["chart"].(map[string]any)["files"] = f
	}
	for name, modify := range cases {
		t.Run(name, func(t *testing.T) {
			d := profilePayload(profileManifest)
			modify(d)
			p, err := InspectUninstallProfile(context.Background(), profileWire(t, d, nil), "team", inspectionSelection(), "session", time.Now())
			if err == nil || p != nil {
				t.Fatal("unsupported complete content admitted")
			}
			if strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("private diagnostic")
			}
		})
	}
}

func TestHelmUninstallProfileRejectsManifestAndStorage(t *testing.T) {
	manifests := map[string]string{
		"empty": "# comments only\n", "secret": strings.Replace(profileManifest, "kind: ConfigMap", "kind: Secret", 1),
		"wrong-version":           strings.Replace(profileManifest, "apiVersion: v1", "apiVersion: other/v1", 1),
		"foreign":                 strings.Replace(profileManifest, "  name: sample", "  name: sample\n  namespace: other", 1),
		"duplicate":               profileManifest + "---\n" + profileManifest,
		"duplicate-default-alias": profileManifest + "---\n" + strings.Replace(profileManifest, "  name: sample", "  name: sample\n  namespace: team", 1),
		"wrong-data-type":         strings.Replace(profileManifest, "PRIVATE_DATA", "42", 1),
		"bad-binary":              profileManifest + "binaryData:\n  bytes: PRIVATE!\n",
		"overlapping-keys":        profileManifest + "binaryData:\n  password: YQ==\n",
		"unknown-root":            profileManifest + "spec: {}\n", "root-alias": profileManifest + "Data: {}\n",
		"list": "apiVersion: v1\nkind: List\nitems: []\n",
	}
	for _, line := range []string{"  generateName: prefix", "  ownerReferences: []", "  finalizers: []", "  deletionTimestamp: null", "  resourceVersion: '7'", "  uid: 'old'", "  annotations:\n    helm.sh/hook: test", "  annotations:\n    helm.sh/hook-weight: '0'", "  annotations:\n    helm.sh/resource-policy: unknown", "  Annotations: {}", "  labels:\n    'invalid label': value"} {
		manifests[line] = strings.Replace(profileManifest, "  name: sample", "  name: sample\n"+line, 1)
	}
	for name, text := range manifests {
		t.Run(name, func(t *testing.T) {
			p, err := InspectUninstallProfile(context.Background(), profileWire(t, profilePayload(text), nil), "team", inspectionSelection(), "session", time.Now())
			if err == nil || p != nil {
				t.Fatal("unsupported manifest admitted")
			}
		})
	}
	for name, modify := range map[string]func(*corev1.Secret){
		"immutable":   func(s *corev1.Secret) { v := true; s.Immutable = &v },
		"finalizers":  func(s *corev1.Secret) { s.Finalizers = []string{"example/hold"} },
		"owners":      func(s *corev1.Secret) { s.OwnerReferences = []metav1.OwnerReference{{Name: "owner"}} },
		"terminating": func(s *corev1.Secret) { v := metav1.Now(); s.DeletionTimestamp = &v },
		"grace":       func(s *corev1.Secret) { v := int64(0); s.DeletionGracePeriodSeconds = &v },
		"keep":        func(s *corev1.Secret) { s.Annotations = map[string]string{"helm.sh/resource-policy": "keep"} },
		"stale-rv":    func(s *corev1.Secret) { s.ResourceVersion = "changed" },
	} {
		t.Run(name, func(t *testing.T) {
			p, e := InspectUninstallProfile(context.Background(), profileWire(t, profilePayload(profileManifest), modify), "team", inspectionSelection(), "session", time.Now())
			if e == nil || p != nil {
				t.Fatal("storage accepted")
			}
		})
	}
}

func TestHelmUninstallProfileBoundsAndCancellation(t *testing.T) {
	for _, count := range []int{MaxPreviewObjects, MaxPreviewObjects + 1} {
		text := ""
		for i := 0; i < count; i++ {
			text += "---\n" + strings.Replace(profileManifest, "name: sample", fmt.Sprintf("name: sample-%d", i), 1)
		}
		p, e := InspectUninstallProfile(context.Background(), profileWire(t, profilePayload(text), nil), "team", inspectionSelection(), "session", time.Now())
		if count == MaxPreviewObjects {
			if e != nil || len(p.Preview().Items()) != count {
				t.Fatal(e)
			}
		} else if e == nil || p != nil {
			t.Fatal("oversized object count")
		}
	}
	wire := profileWire(t, profilePayload(profileManifest), nil)
	for _, left := range []int{1, 2, 30, 100} {
		ctx := &inspectionCancelContext{Context: context.Background(), left: left}
		p, e := InspectUninstallProfile(ctx, wire, "team", inspectionSelection(), "session", time.Now())
		if !errors.Is(e, context.Canceled) || p != nil {
			t.Fatal("cancellation", left, e)
		}
	}
	for _, now := range []time.Time{{}, time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		p, e := InspectUninstallProfile(context.Background(), wire, "team", inspectionSelection(), "session", now)
		if e == nil || p != nil {
			t.Fatal("invalid clock")
		}
	}
	for _, status := range []string{"failed", "uninstalling", "uninstalled", "pending-install"} {
		r := inspectionSelection()
		r.Status = status
		if p, e := InspectUninstallProfile(context.Background(), wire, "team", r, "session", time.Now()); e == nil || p != nil {
			t.Fatal("status", status)
		}
	}
	if p, e := InspectUninstallProfile(context.Background(), make([]byte, MaxSecretBytes+1), "team", inspectionSelection(), "session", time.Now()); !errors.Is(e, ErrInspectionBudget) || p != nil {
		t.Fatal("budget", e)
	}
}

func preparationInputs(t *testing.T) (*UninstallProfile, Snapshot, Snapshot, []LiveItem, time.Time) {
	now := time.Now()
	p := checkedProfile(t, profileManifest, now)
	a := Snapshot{Namespace: "team", ResourceVersion: "opaque-a", ObservedAt: now.Add(time.Millisecond), Revisions: []Revision{inspectionSelection()}}
	b := a
	b.ResourceVersion = "opaque-b"
	b.ObservedAt = now.Add(3 * time.Millisecond)
	b.Revisions = append([]Revision(nil), a.Revisions...)
	items := []LiveItem{{Stored: p.Preview().Items()[0], Namespace: "team", Resource: "configmaps", UID: "object-uid", ResourceVersion: "opaque-object", State: "present", Markers: "match", ObservedAt: now.Add(2 * time.Millisecond)}}
	return p, a, b, items, now.Add(4 * time.Millisecond)
}
func TestHelmUninstallPreparationImmutableExactTargets(t *testing.T) {
	profile, a, b, items, now := preparationInputs(t)
	p, e := FinishUninstallPreparation(context.Background(), profile, "session", a, b, items, now)
	if e != nil || !p.Matches("session", "team", inspectionSelection(), now) {
		t.Fatal(e)
	}
	want := p.Targets()
	items[0].UID = "changed"
	a.Revisions[0].UID = "changed"
	p.Targets()[0].UID = "changed"
	if !reflect.DeepEqual(want, p.Targets()) || p.ID() == "" || p.Namespace() != "team" || p.Summary().ChartName != "example-chart" {
		t.Fatal("defensive identity projection")
	}
	for _, when := range []time.Time{now.Add(-time.Nanosecond), p.ExpiresAt(), p.ExpiresAt().Add(time.Second)} {
		if p.Matches("session", "team", inspectionSelection(), when) {
			t.Fatal("invalid validity")
		}
	}
	r := inspectionSelection()
	r.ResourceVersion = "new"
	if p.Matches("other", "team", inspectionSelection(), now) || p.Matches("session", "other", inspectionSelection(), now) || p.Matches("session", "team", r, now) {
		t.Fatal("scope changed")
	}
	var zero *UninstallPreparation
	if zero.ID() != "" || zero.Namespace() != "" || zero.Revision() != (Revision{}) || zero.Summary() != (Inspection{}) || len(zero.Targets()) != 0 || !zero.ExpiresAt().IsZero() || zero.Matches("session", "team", r, now) {
		t.Fatal("nil handle")
	}
}
func TestHelmUninstallPreparationRejectsChangedBatch(t *testing.T) {
	for _, mode := range []string{"older-history", "newer-history", "source-rv", "source-uid", "missing-source", "missing-object", "marker-missing", "marker-mismatch", "owners", "finalizers", "terminating", "keep", "hook", "wrong-resource", "cluster-scope", "wrong-object", "wrong-uid", "object-before-snapshot", "object-after-snapshot", "expired", "session", "canceled", "unrelated-release"} {
		t.Run(mode, func(t *testing.T) {
			p, a, b, items, now := preparationInputs(t)
			session := "session"
			ctx := context.Background()
			switch mode {
			case "older-history", "newer-history":
				r := inspectionSelection()
				r.UID = "history-uid"
				r.Number = 1
				r.StorageName = "sh.helm.release.v1.example.v1"
				if mode == "newer-history" {
					r.Number = 3
					r.StorageName = "sh.helm.release.v1.example.v3"
				}
				b.Revisions = append(b.Revisions, r)
			case "source-rv":
				b.Revisions[0].ResourceVersion = "changed"
			case "source-uid":
				b.Revisions[0].UID = "changed"
			case "missing-source":
				b.Revisions = nil
			case "missing-object":
				items[0].State = "absent"
				items[0].UID = ""
				items[0].ResourceVersion = ""
				items[0].Markers = ""
			case "marker-missing":
				items[0].Markers = "missing"
			case "marker-mismatch":
				items[0].Markers = "mismatch"
			case "owners":
				items[0].Owners = true
			case "finalizers":
				items[0].Finalizers = true
			case "terminating":
				items[0].Terminating = true
			case "keep":
				items[0].Keep = true
			case "hook":
				items[0].Hook = true
			case "wrong-resource":
				items[0].Resource = "secrets"
			case "cluster-scope":
				items[0].Namespace = ""
			case "wrong-object":
				items[0].Stored.Name = "other"
			case "wrong-uid":
				items[0].UID = "private\n"
			case "object-before-snapshot":
				items[0].ObservedAt = a.ObservedAt.Add(-time.Nanosecond)
			case "object-after-snapshot":
				items[0].ObservedAt = b.ObservedAt.Add(time.Nanosecond)
			case "expired":
				now = p.Preview().ExpiresAt()
			case "session":
				session = "other"
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "unrelated-release":
				r := inspectionSelection()
				r.Release = "other"
				r.StorageName = "sh.helm.release.v1.other.v2"
				r.UID = "unrelated"
				b.Revisions = append(b.Revisions, r)
			}
			out, e := FinishUninstallPreparation(ctx, p, session, a, b, items, now)
			if mode == "unrelated-release" {
				if e != nil || out == nil {
					t.Fatal(e)
				}
			} else if e == nil || out != nil {
				t.Fatal("changed batch accepted")
			}
		})
	}
}
