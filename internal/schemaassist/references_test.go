package schemaassist

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/laojianzi/aster/internal/testschema"
)

func wrappedDocument(t *testing.T, extra map[string]any) *Document {
	t.Helper()
	var top map[string]any
	if err := json.Unmarshal([]byte(testschema.Document), &top); err != nil {
		t.Fatal(err)
	}
	defs := top["components"].(map[string]any)["schemas"].(map[string]any)
	props := defs["Pod"].(map[string]any)["properties"].(map[string]any)
	defs["PodSpec"] = props["spec"]
	wrapper := map[string]any{
		"allOf":       []any{map[string]any{"$ref": "#/components/schemas/PodSpec"}},
		"description": "Description on the Pod spec field.", "default": map[string]any{"do-not-apply": true},
	}
	for key, value := range extra {
		wrapper[key] = value
	}
	props["spec"] = wrapper
	data, err := json.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	return doc(t, string(data))
}

func TestSchemaKubernetesSingleRefAllOfWrappers(t *testing.T) {
	d := wrappedDocument(t, nil)
	help, err := d.Help(context.Background(), "/spec/containers/0/image")
	if err != nil || help.Type != "string" || help.Partial {
		t.Fatalf("nested native reference: %+v %v", help, err)
	}
	spec, err := d.Help(context.Background(), "/spec")
	if err != nil || spec.Type != "object" || spec.Partial || spec.Description != "Description on the Pod spec field." {
		t.Fatalf("field annotations lost: %+v %v", spec, err)
	}
	input := object(map[string]any{"containers": []any{map[string]any{"name": "app"}}})
	before, _ := json.Marshal(input)
	definitions, _ := json.Marshal(d.definitions)
	r, err := d.Check(context.Background(), input)
	if err != nil || r.Partial || len(r.Diagnostics) != 1 || r.Diagnostics[0].Pointer != "/spec/containers/0/image" {
		t.Fatalf("wrapped required field: %+v %v", r, err)
	}
	after, _ := json.Marshal(input)
	definitionsAfter, _ := json.Marshal(d.definitions)
	if string(before) != string(after) || string(definitions) != string(definitionsAfter) {
		t.Fatal("wrapper resolution applied defaults or changed shared definitions")
	}
	// Wrapper cycles use the same bounded cycle guard as direct references.
	d.definitions["PodSpec"] = map[string]any{"allOf": []any{map[string]any{"$ref": "#/components/schemas/PodSpec"}}}
	if _, err := d.Help(context.Background(), "/spec"); !errors.Is(err, ErrUnsupported) {
		t.Fatal("wrapper cycle accepted", err)
	}
}

func TestSchemaAllOfConstraintsAreNeverDiscarded(t *testing.T) {
	for _, extra := range []map[string]any{
		{"type": "array"}, {"nullable": true}, {"required": []any{"another"}},
		{"properties": map[string]any{"another": map[string]any{"type": "integer"}}},
		{"allOf": []any{map[string]any{"$ref": "#/components/schemas/PodSpec"}, map[string]any{"type": "object"}}},
		{"allOf": []any{map[string]any{"$ref": "#/components/schemas/PodSpec", "required": []any{"another"}}}},
	} {
		d := wrappedDocument(t, extra)
		h, err := d.Help(context.Background(), "/spec")
		if err != nil || !h.Partial {
			t.Fatalf("constraint discarded: %+v %+v %v", extra, h, err)
		}
	}
	for _, ref := range []string{"https://example.invalid/private", "#/components/schemas/Missing"} {
		d := wrappedDocument(t, map[string]any{"allOf": []any{map[string]any{"$ref": ref}}})
		if _, err := d.Help(context.Background(), "/spec"); !errors.Is(err, ErrUnsupported) {
			t.Fatal("unsafe wrapper reference", err)
		}
	}
}
