package manifest

import "testing"

func TestDiffIgnoresVolatileMetadata(t *testing.T) {
	before := map[string]any{
		"apiVersion": "apps/v1",
		"kind": "Deployment",
		"metadata": map[string]any{"name": "api", "resourceVersion": "10", "uid": "a"},
		"spec": map[string]any{"replicas": float64(2)},
	}
	after := map[string]any{
		"apiVersion": "apps/v1",
		"kind": "Deployment",
		"metadata": map[string]any{"name": "api", "resourceVersion": "11", "uid": "a"},
		"spec": map[string]any{"replicas": float64(3)},
	}
	changes, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %#v", changes)
	}
	if changes[0].Path != "/spec/replicas" || changes[0].Kind != Modified {
		t.Fatalf("unexpected change: %#v", changes[0])
	}
}

func TestDiffRedactsSecretValues(t *testing.T) {
	before := map[string]any{"kind": "Secret", "data": map[string]any{"token": "old"}}
	after := map[string]any{"kind": "Secret", "data": map[string]any{"token": "new"}}
	changes, err := Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Before != "***" || changes[0].After != "***" {
		t.Fatalf("secret leaked in diff: %#v", changes)
	}
}

func TestDiffEscapesJSONPointerPaths(t *testing.T) {
	changes, err := Diff(
		map[string]any{"metadata": map[string]any{"labels": map[string]any{"app/name": "a"}}},
		map[string]any{"metadata": map[string]any{"labels": map[string]any{"app/name": "b"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "/metadata/labels/app~1name" {
		t.Fatalf("unexpected path: %#v", changes)
	}
}
