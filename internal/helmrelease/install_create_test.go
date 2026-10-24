package helmrelease

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func installCreateResponse(t *testing.T, p *InstallCreatePlan, index int) map[string]any {
	t.Helper()
	var v map[string]any
	b := p.SensitiveBody(index)
	defer clear(b)
	if json.Unmarshal(b, &v) != nil {
		t.Fatal("fixture")
	}
	m := v["metadata"].(map[string]any)
	m["uid"] = "server-generated-uid"
	m["creationTimestamp"] = "2026-10-23T00:00:01Z"
	return v
}
func TestHelmInstallCreateFrozenBodies(t *testing.T) {
	c := installTestCandidate(t)
	ctx := context.Background()
	p, err := BuildInstallCreatePlan(ctx, c, installTestNow())
	if err != nil {
		t.Fatal(err)
	}
	if p.Len() != 2 || p.CandidateID() != c.ID() {
		t.Fatal("plan identity")
	}
	original := p.SensitiveBody(0)
	defer clear(original)
	changed := p.SensitiveBody(0)
	changed[0] = '!'
	clear(changed)
	if !bytes.Equal(original, p.SensitiveBody(0)) {
		t.Fatal("mutable plan bytes")
	}
	var cm map[string]any
	if json.Unmarshal(p.SensitiveBody(1), &cm) != nil {
		t.Fatal("wire")
	}
	m := cm["metadata"].(map[string]any)
	if m["namespace"] != "team" || m["labels"].(map[string]any)["app.kubernetes.io/managed-by"] != "Helm" ||
		m["annotations"].(map[string]any)["meta.helm.sh/release-name"] != "example" {
		t.Fatal("missing deterministic ownership")
	}
	id := p.CandidateID()
	replacement := installTestCandidate(t)
	*c = *replacement
	if p.CandidateID() != id || !bytes.Equal(original, p.SensitiveBody(0)) {
		t.Fatal("candidate wrapper rebound plan")
	}
	for _, v := range []any{p, *p} {
		j, _ := json.Marshal(v)
		if strings.Contains(fmt.Sprintf("%#v", v), "PRIVATE") || bytes.Contains(j, []byte("PRIVATE")) {
			t.Fatal("sensitive plan serialization")
		}
	}
	if p.SensitiveBody(-1) != nil || p.SensitiveBody(p.Len()) != nil {
		t.Fatal("invalid index")
	}
	var nilPlan *InstallCreatePlan
	if nilPlan.Len() != 0 || nilPlan.CandidateID() != "" || nilPlan.SensitiveBody(0) != nil {
		t.Fatal("nil plan")
	}
}
func TestHelmInstallCreatePlanLimits(t *testing.T) {
	for _, mode := range []string{"nil-context", "cancelled", "expired", "zero-candidate", "32-targets", "ordered-binary"} {
		t.Run(mode, func(t *testing.T) {
			c := installTestCandidate(t)
			ctx := context.Background()
			now := installTestNow()
			switch mode {
			case "nil-context":
				ctx = nil
			case "cancelled":
				var stop context.CancelFunc
				ctx, stop = context.WithCancel(ctx)
				stop()
			case "expired":
				now = c.ExpiresAt()
			case "zero-candidate":
				c = &InstallCandidate{}
			case "32-targets", "ordered-binary":
				v := installTestRelease()
				n := 32
				if mode == "ordered-binary" {
					n = 2
				}
				var text strings.Builder
				for i := 0; i < n; i++ {
					fmt.Fprintf(&text, "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: target-%02d\nbinaryData:\n  bytes: AQID\n", i)
				}
				v["manifest"] = text.String()
				raw, _ := json.Marshal(v)
				var e error
				c, e = DecodeInstallCapture(ctx, raw, "team", "example", now)
				if e != nil {
					t.Fatal(e)
				}
			}
			p, err := BuildInstallCreatePlan(ctx, c, now)
			if mode == "32-targets" || mode == "ordered-binary" {
				if err != nil {
					t.Fatal(err)
				}
				if mode == "32-targets" && p.Len() != 33 {
					t.Fatal("count")
				}
				for i := 1; i < p.Len(); i++ {
					resource, name := p.Identity(i)
					if resource != "configmaps" || name != fmt.Sprintf("target-%02d", i-1) {
						t.Fatal("order")
					}
				}
			} else if err == nil || p != nil {
				t.Fatal("invalid codec admitted")
			}
		})
	}
}
func TestHelmInstallCreateStrictResponses(t *testing.T) {
	modes := []string{"valid", "generated-metadata", "changed-data", "wrong-name", "wrong-namespace", "wrong-kind", "wrong-api", "changed-label",
		"changed-annotation", "extra-annotation", "extra-root", "finalizer", "owner", "deleting", "generation", "generate-name", "immutable",
		"missing-uid", "bad-uid", "missing-time", "bad-time", "unknown-metadata", "bad-managed-fields", "wrong-manager", "empty-managed-fields",
		"duplicate-root", "duplicate-meta", "trailing", "invalid-utf8", "null-root", "too-large", "cancelled", "nil-context"}
	for _, kind := range []string{"Secret", "ConfigMap"} {
		t.Run(kind, func(t *testing.T) {
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					p, err := BuildInstallCreatePlan(context.Background(), installTestCandidate(t), installTestNow())
					if err != nil {
						t.Fatal(err)
					}
					index := 0
					if kind == "ConfigMap" {
						index = 1
					}
					v := installCreateResponse(t, p, index)
					m := v["metadata"].(map[string]any)
					switch mode {
					case "generated-metadata":
						m["resourceVersion"] = "opaque-rv"
						m["managedFields"] = []any{map[string]any{"manager": InstallFieldManager, "operation": "Update", "apiVersion": "v1", "time": "2026-10-23T00:00:01Z", "fieldsType": "FieldsV1", "fieldsV1": map[string]any{"f:data": map[string]any{}}}}
					case "changed-data":
						v["data"] = map[string]any{"other": "AAAA"}
					case "wrong-name":
						m["name"] = "other"
					case "wrong-namespace":
						m["namespace"] = "other"
					case "wrong-kind":
						v["kind"] = "Status"
					case "wrong-api":
						v["apiVersion"] = "other/v1"
					case "changed-label":
						m["labels"].(map[string]any)["owner"] = "foreign"
					case "changed-annotation", "extra-annotation":
						m["annotations"] = map[string]any{"unexpected": "PRIVATE_INJECTED"}
					case "extra-root":
						v["future"] = "PRIVATE_INJECTED"
					case "finalizer":
						m["finalizers"] = []string{"example.invalid/keep"}
					case "owner":
						m["ownerReferences"] = []any{map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "name": "owner", "uid": "owner"}}
					case "deleting":
						m["deletionTimestamp"] = "2026-10-23T00:00:01Z"
					case "generation":
						m["generation"] = 1
					case "generate-name":
						m["generateName"] = "prefix-"
					case "immutable":
						v["immutable"] = true
					case "missing-uid":
						delete(m, "uid")
					case "bad-uid":
						m["uid"] = "line\nbreak"
					case "missing-time":
						delete(m, "creationTimestamp")
					case "bad-time":
						m["creationTimestamp"] = "invalid"
					case "unknown-metadata":
						m["future"] = true
					case "bad-managed-fields":
						m["managedFields"] = "invalid"
					case "wrong-manager":
						m["managedFields"] = []any{map[string]any{"manager": "unexpected", "operation": "Update", "apiVersion": "v1", "fieldsType": "FieldsV1", "fieldsV1": map[string]any{}}}
					case "empty-managed-fields":
						m["managedFields"] = []any{map[string]any{"manager": InstallFieldManager, "operation": "Update", "apiVersion": "v1", "fieldsType": "FieldsV1", "fieldsV1": nil}}
					}
					raw, _ := json.Marshal(v)
					switch mode {
					case "duplicate-root":
						raw = append([]byte(`{"kind":"`+kind+`",`), raw[1:]...)
					case "duplicate-meta":
						raw = []byte(strings.Replace(string(raw), `"uid":"server-generated-uid"`, `"uid":"server-generated-uid","uid":"server-generated-uid"`, 1))
					case "trailing":
						raw = append(raw, []byte(`{}`)...)
					case "invalid-utf8":
						raw = append(raw, 0xff)
					case "null-root":
						raw = []byte("null")
					case "too-large":
						raw = make([]byte, MaxSecretBytes+1)
					}
					ctx := context.Background()
					if mode == "nil-context" {
						ctx = nil
					}
					if mode == "cancelled" {
						var stop context.CancelFunc
						ctx, stop = context.WithCancel(ctx)
						stop()
					}
					err = ValidateInstallCreateResponse(ctx, p, index, raw)
					good := mode == "valid" || mode == "generated-metadata"
					if good && err != nil {
						t.Fatal("valid response", err)
					}
					if !good && err == nil {
						t.Fatal("unsafe response accepted")
					}
					if err != nil && strings.Contains(err.Error(), "PRIVATE") {
						t.Fatal("diagnostic leak")
					}
				})
			}
		})
	}
}
func TestHelmInstallCreateDeterministic(t *testing.T) {
	c := installTestCandidate(t)
	ctx := context.Background()
	p, _ := BuildInstallCreatePlan(ctx, c, installTestNow())
	q, _ := BuildInstallCreatePlan(ctx, c, installTestNow())
	for i := 0; i < p.Len(); i++ {
		if !bytes.Equal(p.SensitiveBody(i), q.SensitiveBody(i)) {
			t.Fatal("same snapshot/time changed bytes")
		}
	}
	later, _ := BuildInstallCreatePlan(ctx, c, installTestNow().Add(time.Second))
	if bytes.Equal(p.SensitiveBody(0), later.SensitiveBody(0)) {
		t.Fatal("createdAt not frozen per plan")
	}
}
