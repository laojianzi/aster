package schemaassist

import (
	"context"
	"errors"
	"strconv"
	"testing"
)

func TestSchemaKeywordAndRepeatedRequirementBudgets(t *testing.T) {
	d := wrappedDocument(t, nil)
	node := d.definitions["PodSpec"].(map[string]any)
	for i := 0; i <= MaxSchemaKeys; i++ {
		node["x-extra-"+strconv.Itoa(i)] = true
	}
	if _, err := d.Help(context.Background(), "/spec"); !errors.Is(err, ErrLimit) {
		t.Fatal("unbounded schema-node overlay", err)
	}
	d = wrappedDocument(t, nil)
	node = d.definitions["PodSpec"].(map[string]any)
	node["required"] = make([]any, MaxWork+1)
	if _, err := d.Help(context.Background(), "/spec"); !errors.Is(err, ErrLimit) {
		t.Fatal("unbounded field-help requirements", err)
	}
	required := make([]any, MaxWork/2)
	for i := range required {
		required[i] = "containers"
	}
	node["required"] = required
	container := d.definitions["Container"].(map[string]any)
	container["required"] = required
	// The repeated key is present, so only a work budget (not a diagnostic
	// count) can stop this amplification across multiple object nodes.
	child := map[string]any{"name": "app", "image": "fixture", "containers": []any{}}
	r, err := d.Check(context.Background(), object(map[string]any{"containers": []any{child, child, child}}))
	if err != nil || !r.Partial || r.Checked > MaxWork+1 {
		t.Fatalf("required entries escaped work budget: %+v %v", r, err)
	}
}
