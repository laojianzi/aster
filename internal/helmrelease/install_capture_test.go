package helmrelease

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	jsonpatch "gopkg.in/evanphx/json-patch.v4"
)

// Synthetic sensitive content, never production fixtures or live credentials.
func installTestRelease() map[string]any {
	return map[string]any{
		"name": "example", "namespace": "team", "version": 1, "apply_method": "ssa",
		"info": map[string]any{"status": "pending-install", "description": "Dry run complete",
			"first_deployed": "2026-10-23T00:00:00Z", "last_deployed": "2026-10-23T00:00:00Z"},
		"chart": map[string]any{"metadata": map[string]any{"apiVersion": "v2", "name": "sample", "version": "0.1.0"},
			"values": map[string]any{"secret": "PRIVATE_INSTALL_VALUE"}},
		"config":   map[string]any{"secret": "PRIVATE_INSTALL_VALUE"},
		"manifest": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\n  namespace: team\ndata:\n  message: PRIVATE_INSTALL_VALUE\n",
	}
}
func installTestNow() time.Time { return time.Date(2026, 10, 23, 0, 0, 1, 0, time.UTC) }
func installTestCandidate(t *testing.T) *InstallCandidate {
	t.Helper()
	raw, _ := json.Marshal(installTestRelease())
	c, err := DecodeInstallCapture(context.Background(), raw, "team", "example", installTestNow())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestHelmInstallCaptureBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "empty-namespace", "foreign-namespace", "wrong-name", "revision2", "deployed", "no-description",
		"future-time", "different-times", "deleted", "hooks", "dependency", "unknown-root", "unknown-info", "alias", "duplicate",
		"wrong-chart-api", "chart-name-alias", "nil-chart-name", "config-array", "ownership", "hook-annotation", "unknown-cm-field",
		"owner-ref", "empty-manifest", "duplicate-object", "32-targets", "33-targets", "invalid-utf8", "trailing-json", "cancelled", "nil-context"} {
		t.Run(mode, func(t *testing.T) {
			v := installTestRelease()
			info := v["info"].(map[string]any)
			chart := v["chart"].(map[string]any)
			switch mode {
			case "empty-namespace":
				v["namespace"] = ""
			case "foreign-namespace":
				v["manifest"] = strings.Replace(v["manifest"].(string), "namespace: team", "namespace: other", 1)
			case "wrong-name":
				v["name"] = "other"
			case "revision2":
				v["version"] = 2
			case "deployed":
				info["status"] = "deployed"
			case "no-description":
				delete(info, "description")
			case "future-time":
				info["first_deployed"] = "2030-01-01T00:00:00Z"
				info["last_deployed"] = info["first_deployed"]
			case "different-times":
				info["last_deployed"] = "2026-10-23T00:00:01Z"
			case "deleted":
				info["deleted"] = "2026-10-23T00:00:00Z"
			case "hooks":
				v["hooks"] = []any{map[string]any{"name": "hidden"}}
			case "dependency":
				chart["metadata"].(map[string]any)["dependencies"] = []any{map[string]any{"name": "hidden"}}
			case "unknown-root":
				v["future_lifecycle"] = "hidden"
			case "unknown-info":
				info["resources"] = []any{}
			case "alias":
				v["Name"] = "example"
				delete(v, "name")
			case "wrong-chart-api":
				chart["metadata"].(map[string]any)["apiVersion"] = "v1"
			case "chart-name-alias":
				m := chart["metadata"].(map[string]any)
				m["Name"] = m["name"]
				delete(m, "name")
			case "nil-chart-name":
				chart["metadata"].(map[string]any)["name"] = nil
			case "config-array":
				v["config"] = []any{}
			case "ownership":
				v["manifest"] = strings.Replace(v["manifest"].(string), "  name: example", "  name: example\n  annotations:\n    meta.helm.sh/release-name: other", 1)
			case "hook-annotation":
				v["manifest"] = strings.Replace(v["manifest"].(string), "  name: example", "  name: example\n  annotations:\n    helm.sh/hook: pre-install", 1)
			case "unknown-cm-field":
				v["manifest"] = v["manifest"].(string) + "unexpected: true\n"
			case "owner-ref":
				v["manifest"] = strings.Replace(v["manifest"].(string), "  name: example", "  name: example\n  ownerReferences: []", 1)
			case "empty-manifest":
				v["manifest"] = ""
			case "duplicate-object":
				v["manifest"] = v["manifest"].(string) + "---\n" + v["manifest"].(string)
			case "32-targets", "33-targets":
				n := 32
				if mode == "33-targets" {
					n++
				}
				var b strings.Builder
				for i := 0; i < n; i++ {
					fmt.Fprintf(&b, "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: item-%d\n", i)
				}
				v["manifest"] = b.String()
			}
			raw, _ := json.Marshal(v)
			switch mode {
			case "duplicate":
				raw = append([]byte(`{"name":"example",`), raw[1:]...)
			case "invalid-utf8":
				raw = append(raw, 0xff)
			case "trailing-json":
				raw = append(raw, []byte(`{}`)...)
			}
			ctx := context.Background()
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if mode == "nil-context" {
				ctx = nil
			}
			c, err := DecodeInstallCapture(ctx, raw, "team", "example", installTestNow())
			good := mode == "valid" || mode == "32-targets"
			if good && (err != nil || c == nil) {
				t.Fatal("valid capture rejected", err)
			}
			if !good && (err == nil || c != nil) {
				t.Fatal("unsafe complete capture accepted")
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("diagnostic leaked content")
			}
		})
	}
}
func TestHelmInstallCapturePrivacyLifetimeAndCopies(t *testing.T) {
	c := installTestCandidate(t)
	for _, v := range []string{fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(v, "PRIVATE") || strings.Contains(v, "pending-install") {
			t.Fatal("format leaked payload")
		}
	}
	j, _ := json.Marshal(c)
	if bytes.Contains(j, []byte("PRIVATE")) {
		t.Fatal("JSON leaked")
	}
	if !c.Valid(installTestNow()) || c.Valid(c.ExpiresAt()) || c.Valid(installTestNow().Add(-time.Nanosecond)) {
		t.Fatal("invalid lifetime")
	}
	items := c.Targets()
	items[0].Name = "changed"
	if c.Targets()[0].Name != "example" {
		t.Fatal("mutable target alias")
	}
	var nilCandidate *InstallCandidate
	if nilCandidate.Valid(time.Now()) || nilCandidate.ID() != "" || nilCandidate.StorageName() != "" {
		t.Fatal("nil candidate")
	}
}
func installTestWire(t *testing.T, c *InstallCandidate) ([]byte, Revision) {
	t.Helper()
	raw, err := BuildInstallSecret(context.Background(), c, installTestNow())
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if json.Unmarshal(raw, &wire) != nil {
		t.Fatal("invalid wire")
	}
	meta := wire["metadata"].(map[string]any)
	meta["uid"] = "installed-uid"
	meta["resourceVersion"] = "opaque-rv"
	raw, _ = json.Marshal(wire)
	return raw, Revision{Release: "example", Number: 1, StorageName: c.StorageName(), Status: "pending-install", UID: "installed-uid", ResourceVersion: "opaque-rv"}
}
func TestHelmInstallStorageCodecPreservesOriginalAndConditions(t *testing.T) {
	c := installTestCandidate(t)
	ctx := context.Background()
	wire, row := installTestWire(t, c)
	before := bytes.Clone(wire)
	patch, err := BuildInstallDeployedPatch(ctx, c, wire, row, installTestNow().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, wire) {
		t.Fatal("mutated caller wire")
	}
	p, err := jsonpatch.DecodePatch(patch)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Apply(wire)
	if err != nil {
		t.Fatal(err)
	}
	row.Status = "deployed"
	_, err = inspectSecretPayload(ctx, out, "team", row, func(_ Inspection, _ string, payload []byte) error {
		var old, new map[string]json.RawMessage
		_ = json.Unmarshal([]byte(c.payload), &old)
		_ = json.Unmarshal(payload, &new)
		for _, key := range []string{"chart", "manifest", "config", "name", "namespace", "version", "apply_method"} {
			if !bytes.Equal(old[key], new[key]) {
				t.Errorf("changed retained member %s", key)
			}
		}
		var i struct{ Status, Description string }
		_ = json.Unmarshal(new["info"], &i)
		if i.Status != "deployed" || i.Description != "Install complete" {
			t.Error("wrong completion fields")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var ops []statusPatchOperation
	_ = json.Unmarshal(patch, &ops)
	required := map[string]bool{"/metadata/name": false, "/metadata/namespace": false, "/metadata/uid": false, "/metadata/resourceVersion": false,
		"/metadata/labels/owner": false, "/metadata/labels/name": false, "/metadata/labels/version": false, "/metadata/labels/status": false, "/data/release": false}
	for _, op := range ops {
		if op.Op == "test" {
			if _, ok := required[op.Path]; ok {
				required[op.Path] = true
			}
		}
	}
	for key, found := range required {
		if !found {
			t.Errorf("missing atomic condition %s", key)
		}
	}
	for _, key := range []string{"uid", "resourceVersion"} {
		var changed map[string]any
		_ = json.Unmarshal(wire, &changed)
		changed["metadata"].(map[string]any)[key] = "changed"
		raw, _ := json.Marshal(changed)
		if _, err := p.Apply(raw); err == nil {
			t.Errorf("atomic %s condition not enforced", key)
		}
	}
}
func TestHelmInstallStorageRejectsChangedPendingPayload(t *testing.T) {
	c := installTestCandidate(t)
	wire, row := installTestWire(t, c)
	for _, mode := range []string{"wrong-uid", "wrong-rv", "wrong-release", "wrong-version", "wrong-status", "owner", "finalizer", "immutable", "changed-bytes", "expired", "cancelled", "nil-context"} {
		t.Run(mode, func(t *testing.T) {
			r := row
			raw := bytes.Clone(wire)
			now := installTestNow()
			ctx := context.Background()
			var obj map[string]any
			_ = json.Unmarshal(raw, &obj)
			meta := obj["metadata"].(map[string]any)
			switch mode {
			case "wrong-uid":
				r.UID = "changed"
			case "wrong-rv":
				r.ResourceVersion = "changed"
			case "wrong-release":
				r.Release = "other"
			case "wrong-version":
				r.Number = 2
			case "wrong-status":
				r.Status = "deployed"
			case "owner":
				meta["ownerReferences"] = []any{map[string]any{"apiVersion": "v1", "kind": "Secret", "name": "x", "uid": "x"}}
			case "finalizer":
				meta["finalizers"] = []string{"example.invalid/keep"}
			case "immutable":
				obj["immutable"] = true
			case "changed-bytes":
				payload, _ := installPendingPayload(ctx, c)
				payload = append(payload, ' ')
				encoded, _ := encodeStatusPayload(ctx, payload)
				obj["data"] = map[string][]byte{"release": encoded}
			case "expired":
				now = c.ExpiresAt()
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil-context":
				ctx = nil
			}
			raw, _ = json.Marshal(obj)
			got, err := BuildInstallDeployedPatch(ctx, c, raw, r, now)
			if err == nil || got != nil {
				t.Fatal("unsafe patch produced")
			}
		})
	}
	a, _ := BuildInstallSecret(context.Background(), c, installTestNow())
	b, _ := BuildInstallSecret(context.Background(), c, installTestNow())
	if !reflect.DeepEqual(a, b) {
		t.Fatal("nondeterministic encoding")
	}
}
