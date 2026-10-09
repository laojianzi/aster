package schemaassist

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/laojianzi/aster/internal/testschema"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var podGVK = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}

func doc(t *testing.T, data string) *Document {
	t.Helper()
	d, e := Decode(context.Background(), []byte(data), podGVK)
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func object(spec map[string]any) map[string]any {
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "example"}, "spec": spec}
}
func TestSchemaFieldHelpAndStructuralHints(t *testing.T) {
	d := doc(t, testschema.Document)
	h, e := d.Help(context.Background(), "/spec/containers/0/image")
	if e != nil || h.Type != "string" || !strings.Contains(h.Text(), "Container image") {
		t.Fatalf("%+v %v", h, e)
	}
	h, e = d.Help(context.Background(), "/spec/special~1key~0")
	if e != nil || h.Type != "string" {
		t.Fatalf("escaped path %v", e)
	}
	root, e := d.Help(context.Background(), "/spec")
	if e != nil || len(root.Fields) != 7 {
		t.Fatalf("%+v %v", root, e)
	}
	spec := map[string]any{"containers": []any{map[string]any{"name": "test"}}, "count": "SECRET VALUE MUST NOT APPEAR", "unexpected": "SECRET VALUE MUST NOT APPEAR"}
	input := object(spec)
	before, _ := json.Marshal(input)
	r, e := d.Check(context.Background(), input)
	if e != nil || r.Partial || len(r.Diagnostics) != 3 {
		t.Fatalf("%+v %v", r, e)
	}
	for _, want := range []string{"/spec/containers/0/image · required", "/spec/count · type", "/spec/unexpected · unknown"} {
		if !strings.Contains(r.Text(), want) {
			t.Errorf("missing %s in %s", want, r.Text())
		}
	}
	if strings.Contains(r.Text(), "SECRET VALUE") {
		t.Fatal("diagnostic disclosed a value")
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("check changed draft")
	}
}
func TestSchemaMapNullableAndPreservation(t *testing.T) {
	d := doc(t, testschema.Document)
	spec := map[string]any{"containers": []any{}, "maybe": nil, "port": "http", "labels": map[string]any{"any/key": "value"}, "opaque": map[string]any{"unknown": map[string]any{"nested": "keep"}, "known": int64(3)}}
	r, e := d.Check(context.Background(), object(spec))
	if e != nil || len(r.Diagnostics) != 0 || r.Partial {
		t.Fatalf("%+v %v", r, e)
	}
	spec["opaque"].(map[string]any)["known"] = "bad"
	r, e = d.Check(context.Background(), object(spec))
	if e != nil || len(r.Diagnostics) != 1 || r.Diagnostics[0].Pointer != "/spec/opaque/known" {
		t.Fatalf("known child not checked: %+v %v", r, e)
	}
}
func TestSchemaStrictParsingAndBudgets(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		limit      int
		want       error
	}{
		{"duplicate", `{"x":1,"x":2}`, 100, ErrInvalid},
		{"trailing", `{} {}`, 100, ErrInvalid},
		{"syntax", `{"x":}`, 100, ErrInvalid},
		{"size", `{"x":1}`, 2, ErrLimit},
		{"depth", strings.Repeat("[", MaxDepth+2) + "0" + strings.Repeat("]", MaxDepth+2), 1000, ErrLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e := ParseJSON(context.Background(), []byte(tc.data), tc.limit)
			if !errors.Is(e, tc.want) {
				t.Fatalf("%v", e)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := ParseJSON(ctx, []byte(`{}`), 10); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	d := doc(t, testschema.Document)
	entries := make([]any, MaxWork+1)
	r, e := d.Check(context.Background(), object(map[string]any{"containers": entries}))
	if e != nil || !r.Partial || r.Checked > MaxWork+1 {
		t.Fatalf("unbounded check %+v %v", r, e)
	}
	spec := map[string]any{"containers": []any{}}
	for i := 0; i < MaxDiagnostics+1; i++ {
		spec[strings.Repeat("x", i+1)] = "value"
	}
	r, e = d.Check(context.Background(), object(spec))
	if e != nil || !r.Partial || len(r.Diagnostics) != MaxDiagnostics {
		t.Fatalf("unbounded output %+v %v", r, e)
	}
}
func TestSchemaReferencesCannotEscapeOrLoop(t *testing.T) {
	for _, ref := range []string{"https://example.invalid/private", "#/components/schemas/Missing", "#/components/schemas/Container"} {
		t.Run(ref, func(t *testing.T) {
			var top map[string]any
			_ = json.Unmarshal([]byte(testschema.Document), &top)
			defs := top["components"].(map[string]any)["schemas"].(map[string]any)
			defs["Container"] = map[string]any{"$ref": ref}
			data, _ := json.Marshal(top)
			d := doc(t, string(data))
			_, e := d.Help(context.Background(), "/spec/containers/0/image")
			if !errors.Is(e, ErrUnsupported) {
				t.Fatal(e)
			}
			r, e := d.Check(context.Background(), object(map[string]any{"containers": []any{map[string]any{"name": "a"}}}))
			if e != nil || !r.Partial || strings.Contains(r.Text(), ref) {
				t.Fatalf("%+v %v", r, e)
			}
		})
	}
}
func TestSchemaPointerAndVersionRejections(t *testing.T) {
	d := doc(t, testschema.Document)
	for _, p := range []string{"spec", "/~2", "/~", "/spec/containers/-/image", "/spec/containers/x/image"} {
		if _, e := d.Help(context.Background(), p); e == nil {
			t.Fatal("accepted", p)
		}
	}
	for _, body := range []string{`{"openapi":"3.1.0"}`, strings.ReplaceAll(testschema.Document, `"kind":"Pod"`, `"kind":"Other"`)} {
		if _, e := Decode(context.Background(), []byte(body), podGVK); e == nil {
			t.Fatal("wrong GVK/version")
		}
	}
	if _, e := d.Check(context.Background(), map[string]any{"apiVersion": "v1", "kind": "Secret"}); e == nil {
		t.Fatal("wrong draft GVK")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := d.Help(ctx, "/spec"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestSchemaHelpIsBoundedPlainText(t *testing.T) {
	var top map[string]any
	_ = json.Unmarshal([]byte(testschema.Document), &top)
	root := top["components"].(map[string]any)["schemas"].(map[string]any)["Pod"].(map[string]any)
	root["description"] = strings.Repeat("x", 3000) + "\u001b\u202e"
	props := root["properties"].(map[string]any)
	for i := 0; i < MaxFields+1; i++ {
		props[strings.Repeat("a", i+1)] = map[string]any{"type": "string"}
	}
	data, _ := json.Marshal(top)
	d := doc(t, string(data))
	h, e := d.Help(context.Background(), "")
	if e != nil || !h.Partial || len(h.Fields) != MaxFields || strings.ContainsAny(h.Text(), "\u001b\u202e") || len(h.Text()) > 64<<10 {
		t.Fatalf("bad help %v", e)
	}
}
func TestSchemaCompositionRemainsAdvisory(t *testing.T) {
	d := doc(t, strings.Replace(testschema.Document, `"count":{"type":"integer"}`, `"count":{"allOf":[{"type":"object","properties":{"x":{"type":"string"}}}]}`, 1))
	r, e := d.Check(context.Background(), object(map[string]any{"containers": []any{}, "count": map[string]any{"x": 3}}))
	if e != nil || !r.Partial || len(r.Diagnostics) != 0 {
		t.Fatalf("unsupported constraints treated as validation %+v %v", r, e)
	}
}
func TestIntegerClassificationAvoidsExponentAllocation(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{{"1", true}, {"1.0", true}, {"1.2", false}, {"1.00000000000000001", false}, {"1.2e1", true}, {"1e-1", false}, {"0e-1000000", true}, {"1e1000000", true}, {"1e999999999999999999", false}} {
		if got := integer(json.Number(tc.value)); got != tc.want {
			t.Errorf("%s: %v", tc.value, got)
		}
	}
}
