package helmrelease

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/manifest"
)

func previewWire(t *testing.T, text string) []byte {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(helmfixture.Payload("team", "example", "deployed", 2), &payload); err != nil {
		t.Fatal(err)
	}
	payload["manifest"] = text
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	secret := inspectionSecret()
	secret.Data["release"] = helmfixture.Stored(data)
	return inspectionWire(secret)
}
func previewManifest(name string) string {
	return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name + "\n  namespace: team\ndata:\n  token: PRIVATE_MANIFEST\n"
}
func TestHelmUninstallPreviewImmutableIdentityExpiryAndPrivacy(t *testing.T) {
	text := previewManifest("first") + "---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: kept\n  annotations:\n    helm.sh/resource-policy: keep\nstringData:\n  token: PRIVATE_SECRET\n" +
		"---\napiVersion: batch/v1\nkind: Job\nmetadata:\n  name: hook\n  namespace: other\n  annotations:\n    helm.sh/hook: pre-delete\n" +
		"---\napiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: examples.test.example\n"
	data := previewWire(t, text)
	original := bytes.Clone(data)
	now := time.Now()
	row := inspectionSelection()
	p, err := PreviewUninstallSecret(context.Background(), data, "team", row, "session", now)
	if err != nil {
		t.Fatal(err)
	}
	items := p.Items()
	if len(items) != 4 || items[0].Name != "first" || !items[1].Keep || items[1].Namespace != "" || !items[2].Hook || !items[2].ForeignNamespace || !items[3].CRD || p.Summary().HookCount != 1 {
		t.Fatalf("bad inventory: %+v", items)
	}
	if len(p.ID()) != 32 || !p.Matches("session", "team", row, now) || !p.Matches("session", "team", row, p.ExpiresAt().Add(-time.Nanosecond)) || p.Matches("session", "team", row, p.ExpiresAt()) || p.Matches("session", "team", row, now.Add(-time.Nanosecond)) {
		t.Fatal("lifetime not enforced")
	}
	if p.Matches("another", "team", row, now) || p.Matches("session", "other", row, now) {
		t.Fatal("cross-scope match")
	}
	for _, mutate := range []func(*Revision){func(r *Revision) { r.UID = "new" }, func(r *Revision) { r.ResourceVersion = "8" }, func(r *Revision) { r.Number = 3 }, func(r *Revision) { r.Status = "failed" }, func(r *Revision) { r.StorageName = "other" }} {
		changed := row
		mutate(&changed)
		if p.Matches("session", "team", changed, now) {
			t.Fatal("changed revision accepted")
		}
	}
	if !bytes.Equal(data, original) {
		t.Fatal("source modified")
	}
	clear(data)
	items[0].Name = "changed"
	if p.Items()[0].Name != "first" {
		t.Fatal("mutation alias")
	}
	for _, projected := range []any{p.Items(), p.Summary(), p} {
		b, _ := json.Marshal(projected)
		if strings.Contains(string(b), "PRIVATE") {
			t.Fatal("payload retained in public projection")
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				p.Items()[0].Name = "caller"
				if p.Items()[0].Name != "first" {
					t.Error("concurrent alias")
				}
			}
		}()
	}
	wg.Wait()
	again, err := PreviewUninstallSecret(context.Background(), original, "team", row, "session", now)
	if err != nil || again.ID() == p.ID() {
		t.Fatal("ID not fresh opaque identity", err)
	}
	var nilP *UninstallPreview
	if nilP.ID() != "" || nilP.Items() != nil || nilP.Namespace() != "" || nilP.Revision() != (Revision{}) || nilP.Summary() != (Inspection{}) || !nilP.ExpiresAt().IsZero() || nilP.Matches("session", "team", row, now) {
		t.Fatal("nil observation")
	}
}

func TestHelmUninstallPreviewRejectsAmbiguityAndLateFailure(t *testing.T) {
	good := previewManifest("first")
	for name, bad := range map[string]string{
		"duplicate-yaml": good + "kind: Secret\n", "duplicate-json": `{"apiVersion":"v1","kind":"Secret","kind":"ConfigMap","metadata":{"name":"x"}}`,
		"alias": good + "other: &x PRIVATE\ncopy: *x\n", "merge": good + "x: {<<: {foo: PRIVATE}}\n", "late": good + "---\nPRIVATE_BAD: [", "duplicate-resource": good + "---\n" + good,
		"namespace-alias": good + "---\n" + strings.ReplaceAll(good, "  namespace: team\n", ""),
		"version-alias":   strings.ReplaceAll(good, "apiVersion: v1", "apiVersion: apps/v1") + "---\n" + strings.ReplaceAll(good, "apiVersion: v1", "apiVersion: apps/v2"),
		"list":            strings.ReplaceAll(good, "kind: ConfigMap", "kind: List"), "typed-list": good + "items: []\n",
		"generateName": strings.ReplaceAll(good, "  name: first", "  name: first\n  generateName: prefix-"),
		"invalid-name": strings.ReplaceAll(good, "name: first", "name: ../PRIVATE"), "missing-name": strings.ReplaceAll(good, "  name: first\n", ""),
		"null-namespace": strings.ReplaceAll(good, "namespace: team", "namespace: null"), "case-policy": strings.ReplaceAll(good, "  namespace: team", "  annotations:\n    helm.sh/resource-policy: KEEP"),
		"annotation-value": strings.ReplaceAll(good, "  namespace: team", "  annotations:\n    example.test/note: 1"),
		"invalid-kind":     strings.ReplaceAll(good, "kind: ConfigMap", "kind: Bad/Kind"), "invalid-gv": strings.ReplaceAll(good, "apiVersion: v1", "apiVersion: ../v1"),
		"bom": "\ufeff" + good, "line-break": strings.ReplaceAll(good, "\n", "\u2028"), "invalid-utf8": string([]byte{255}),
	} {
		t.Run(name, func(t *testing.T) {
			items, err := previewItems(context.Background(), bad, "team")
			if err == nil || items != nil {
				t.Fatal("unsafe partial inventory accepted")
			}
			if strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("error disclosed content")
			}
		})
	}
	// Valid comments, CRLF, and indented document separators in scalars are inert.
	for _, text := range []string{"", "# empty\n", strings.ReplaceAll(good, "\n", "\r\n"), good + "literal: |\n  ---\n  kind: Secret\n", "---\n" + good + "---\n# tail\n"} {
		if _, err := previewItems(context.Background(), text, "team"); err != nil {
			t.Fatal("supported form rejected", err)
		}
	}
	data := previewWire(t, good)
	for _, body := range [][]byte{append(bytes.Clone(data), 'x'), bytes.Replace(data, []byte(`"resourceVersion":"7"`), []byte(`"resourceVersion":"8"`), 1)} {
		if p, err := PreviewUninstallSecret(context.Background(), body, "team", inspectionSelection(), "session", time.Now()); err == nil || p != nil {
			t.Fatal("envelope bypass")
		}
	}
}

func TestHelmUninstallPreviewBudgetsAndCancellation(t *testing.T) {
	var all strings.Builder
	for i := 0; i < MaxPreviewObjects; i++ {
		fmt.Fprintf(&all, "---\n%s", previewManifest(fmt.Sprintf("object-%d", i)))
	}
	if items, err := previewItems(context.Background(), all.String(), "team"); err != nil || len(items) != MaxPreviewObjects {
		t.Fatal("exact object budget", err)
	}
	for _, text := range []string{all.String() + "---\n" + previewManifest("extra"), strings.Repeat("# comment\n---\n", MaxPreviewSegments+1), strings.Repeat("#\n", MaxPreviewNewlines+1), strings.Repeat("x", MaxManifestBytes+1), previewManifest("large") + "text: " + strings.Repeat("x", manifest.MaxBytes)} {
		if items, err := previewItems(context.Background(), text, "team"); !errors.Is(err, ErrPreviewBudget) || items != nil {
			t.Fatal("budget failed", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := PreviewUninstallSecret(ctx, previewWire(t, previewManifest("one")), "team", inspectionSelection(), "session", time.Now()); !errors.Is(err, context.Canceled) || p != nil {
		t.Fatal("cancel ignored", err)
	}
	if items, err := previewItems(&inspectionCancelContext{Context: context.Background(), left: 5}, all.String(), "team"); !errors.Is(err, context.Canceled) || items != nil {
		t.Fatal("inflight cancel ignored", err)
	}
	for _, session := range []string{"", strings.Repeat("s", 257), "bad\nidentity", string([]byte{255})} {
		if p, err := PreviewUninstallSecret(context.Background(), nil, "team", inspectionSelection(), session, time.Now()); !errors.Is(err, ErrPreviewSession) || p != nil {
			t.Fatal("session admitted", err)
		}
	}
	if _, err := PreviewUninstallSecret(context.Background(), nil, "team", inspectionSelection(), "session", time.Time{}); !errors.Is(err, ErrPreviewSession) {
		t.Fatal("zero clock")
	}
}
