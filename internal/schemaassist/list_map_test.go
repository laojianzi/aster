package schemaassist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func listMapTestSchema(keys ...string) map[string]any {
	names := make([]any, len(keys))
	required := make([]any, len(keys))
	properties := make(map[string]any, len(keys))
	for i, key := range keys {
		names[i], required[i] = key, key
		properties[key] = map[string]any{"type": "string"}
	}
	return map[string]any{
		"type": "array", "x-kubernetes-list-type": "map", "x-kubernetes-list-map-keys": names,
		"items": map[string]any{"type": "object", "required": required, "properties": properties, "additionalProperties": true},
	}
}

func listMapTestItems(keys []string, tuples ...[]string) []any {
	items := make([]any, len(tuples))
	for i, tuple := range tuples {
		item := map[string]any{"payload": i}
		// Opposite map insertion orders must not change the identity projection.
		for j := len(keys) - 1; j >= 0; j-- {
			item[keys[j]] = tuple[j]
		}
		items[i] = item
	}
	return items
}

// Host-only values exercise defensive checks that ordinary JSON cannot encode,
// such as typed nil, invalid UTF-8 and cyclic references. Valid public inputs
// are also checked through Decode in the equality and preservation tests.
func listMapTestDocument(t testing.TB, property map[string]any) *Document {
	t.Helper()
	d, _ := constraintDocument(t, `{}`)
	d.root["properties"].(map[string]any)["value"] = property
	return d
}

func listMapTestCheck(t testing.TB, property map[string]any, value any) Report {
	t.Helper()
	r, err := listMapTestDocument(t, property).Check(context.Background(), constraintObject(value))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func listMapTestWant(t testing.TB, r Report, partial, duplicate bool, otherCodes ...string) {
	t.Helper()
	if duplicate {
		otherCodes = append(otherCodes, "list-map-keys")
	}
	constraintWant(t, r, partial, otherCodes...)
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Code == "list-map-keys" && (diagnostic.Pointer != "/value" || diagnostic.Message != "List entries repeat the advertised string-key identity.") {
			t.Fatalf("identity hint lost its generic parent location: %+v", diagnostic)
		}
	}
}

func listMapTestChecker(ctx context.Context) (*constraintChecker, *Report) {
	r := &Report{}
	c := &constraintChecker{ctx: ctx, report: r}
	c.add = func(path, code, message string) {
		r.Diagnostics = append(r.Diagnostics, Diagnostic{Pointer: path, Code: code, Message: message})
	}
	return c, r
}

func TestSchemaListMapStringKeyIdentity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		keys      []string
		tuples    [][]string
		duplicate bool
	}{
		{"empty-array", []string{"name"}, [][]string{}, false},
		{"single-item", []string{"name"}, [][]string{{"one"}}, false},
		{"different-items", []string{"name"}, [][]string{{"one"}, {"two"}}, false},
		{"same-key-different-payload", []string{"name"}, [][]string{{"one"}, {"one"}}, true},
		{"duplicate-after-other-items", []string{"name"}, [][]string{{"one"}, {"two"}, {"three"}, {"two"}}, true},
		{"many-duplicates-one-hint", []string{"name"}, [][]string{{"one"}, {"one"}, {"one"}, {"one"}}, true},
		{"empty-identity-is-a-value", []string{"name"}, [][]string{{""}, {""}}, true},
		{"empty-and-nonempty-distinct", []string{"name"}, [][]string{{""}, {"a"}}, false},
		{"case-sensitive", []string{"name"}, [][]string{{"Name"}, {"name"}}, false},
		{"unicode-exact-match", []string{"name"}, [][]string{{"中文船🚢"}, {"中文船🚢"}}, true},
		{"unicode-normalization-not-applied", []string{"name"}, [][]string{{"é"}, {"e\u0301"}}, false},
		{"linebreaks-and-nul-preserved", []string{"name"}, [][]string{{"x\n\x00y"}, {"x\n\x00y"}}, true},
		{"second-component-distinguishes", []string{"name", "zone"}, [][]string{{"same", "east"}, {"same", "west"}}, false},
		{"first-component-distinguishes", []string{"name", "zone"}, [][]string{{"east", "same"}, {"west", "same"}}, false},
		{"both-components-match", []string{"name", "zone"}, [][]string{{"same", "same"}, {"same", "same"}}, true},
		{"concatenation-is-not-identity", []string{"name", "zone"}, [][]string{{"ab", "c"}, {"a", "bc"}}, false},
		{"separator-is-not-identity", []string{"name", "zone"}, [][]string{{"a|b", "c"}, {"a", "b|c"}}, false},
		{"nul-separator-is-not-identity", []string{"name", "zone"}, [][]string{{"a\x00b", "c"}, {"a", "b\x00c"}}, false},
		{"empty-components-keep-position", []string{"name", "zone"}, [][]string{{"", "x"}, {"x", ""}}, false},
		{"escaped-property-name-parent-pointer", []string{"name/~🚢"}, [][]string{{"same"}, {"same"}}, true},
		{"metadata-order-is-consistent", []string{"zone", "name"}, [][]string{{"east", "one"}, {"east", "one"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := constraintDocument(t, constraintEncode(t, listMapTestSchema(tc.keys...)))
			r, err := d.Check(context.Background(), constraintObject(listMapTestItems(tc.keys, tc.tuples...)))
			if err != nil {
				t.Fatal(err)
			}
			listMapTestWant(t, r, false, tc.duplicate)
		})
	}
	t.Run("eighth-component-distinguishes", func(t *testing.T) {
		keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
		first := []string{"", "x", "x", "x", "x", "x", "x", "east"}
		second := []string{"", "x", "x", "x", "x", "x", "x", "west"}
		listMapTestWant(t, listMapTestCheck(t, listMapTestSchema(keys...), listMapTestItems(keys, first, second)), false, false)
		second[7] = first[7]
		listMapTestWant(t, listMapTestCheck(t, listMapTestSchema(keys...), listMapTestItems(keys, first, second)), false, true)
	})
	t.Run("nullable-parent-null-has-no-identity", func(t *testing.T) {
		property := listMapTestSchema("name")
		property["nullable"] = true
		listMapTestWant(t, listMapTestCheck(t, property, nil), false, false)
	})
	t.Run("nonnullable-parent-null-keeps-type-hint", func(t *testing.T) {
		listMapTestWant(t, listMapTestCheck(t, listMapTestSchema("name"), nil), false, false, "type")
	})
	t.Run("wrong-type-keeps-only-type-hint", func(t *testing.T) {
		listMapTestWant(t, listMapTestCheck(t, listMapTestSchema("name"), "private-wrong-type"), false, false, "type")
	})
	t.Run("other-constraints-remain-independent", func(t *testing.T) {
		property := listMapTestSchema("name")
		property["items"].(map[string]any)["properties"].(map[string]any)["payload"] = map[string]any{"type": "string", "minLength": 3}
		items := []any{map[string]any{"name": "same", "payload": "x"}, map[string]any{"name": "same", "payload": "valid"}}
		listMapTestWant(t, listMapTestCheck(t, property, items), false, true, "minLength")
	})
}

// The oracle deliberately uses pairwise component comparisons, with neither a
// fixed tuple nor a map/hash/serialization key. It does not call production
// projection or any production equality helper to obtain expected results.
func listMapIndependentDuplicate(tuples [][]string) bool {
	for i := range tuples {
		for j := 0; j < i; j++ {
			if len(tuples[i]) != len(tuples[j]) {
				continue
			}
			equal := true
			for k, value := range tuples[i] {
				if value != tuples[j][k] {
					equal = false
					break
				}
			}
			if equal {
				return true
			}
		}
	}
	return false
}

func TestSchemaListMapMatchesIndependentTupleOracle(t *testing.T) {
	corpus := [][]string{
		{"", ""}, {"", "a"}, {"a", ""}, {"a", "a"}, {"a", "b"}, {"b", "a"},
		{"ab", "c"}, {"a", "bc"}, {"a|b", "c"}, {"a", "b|c"}, {"a\x00b", "c"}, {"a", "b\x00c"},
		{"A", "a"}, {"a", "A"}, {"é", "x"}, {"e\u0301", "x"}, {"船", "🚢"}, {"🚢", "船"},
		{"\n", "x"}, {"x", "\n"}, {"a\tb", "c"}, {"a", "b\tc"}, {"alpha", "β"}, {"β", "alpha"},
	}
	keys := []string{"first", "second"}
	d, _ := constraintDocument(t, constraintEncode(t, listMapTestSchema(keys...)))
	comparisons := 0
	for i, first := range corpus {
		for j, second := range corpus {
			tuples := [][]string{first, second}
			r, err := d.Check(context.Background(), constraintObject(listMapTestItems(keys, tuples...)))
			if err != nil {
				t.Fatalf("oracle pair %d/%d: %v", i, j, err)
			}
			listMapTestWant(t, r, false, listMapIndependentDuplicate(tuples))
			comparisons++
		}
	}
	if comparisons != 576 {
		t.Fatal("independent tuple corpus was reduced", comparisons)
	}
	t.Logf("independent tuple oracle comparisons: %d", comparisons)
}

func TestSchemaListMapRejectsMalformedAndUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		codes  []string
	}{
		{"missing-list-type", func(n map[string]any) { delete(n, "x-kubernetes-list-type") }, nil},
		{"missing-keys", func(n map[string]any) { delete(n, "x-kubernetes-list-map-keys") }, nil},
		{"set-is-unsupported", func(n map[string]any) { n["x-kubernetes-list-type"] = "set" }, nil},
		{"atomic-is-unsupported", func(n map[string]any) { n["x-kubernetes-list-type"] = "atomic" }, nil},
		{"unknown-list-type", func(n map[string]any) { n["x-kubernetes-list-type"] = "PRIVATE_UNSUPPORTED" }, nil},
		{"non-string-list-type", func(n map[string]any) { n["x-kubernetes-list-type"] = []any{"map"} }, nil},
		{"missing-parent-type", func(n map[string]any) { delete(n, "type") }, nil},
		{"non-array-parent", func(n map[string]any) { n["type"] = "object" }, []string{"type"}},
		{"malformed-parent-type", func(n map[string]any) { n["type"] = []any{"array"} }, nil},
		{"parent-int-or-string-override", func(n map[string]any) { n["x-kubernetes-int-or-string"] = true }, []string{"type"}},
		{"composite-parent", func(n map[string]any) { n["anyOf"] = []any{map[string]any{}} }, nil},
		{"keys-not-array", func(n map[string]any) { n["x-kubernetes-list-map-keys"] = "name" }, nil},
		{"keys-null", func(n map[string]any) { n["x-kubernetes-list-map-keys"] = nil }, nil},
		{"keys-typed-nil", func(n map[string]any) { n["x-kubernetes-list-map-keys"] = []any(nil) }, nil},
		{"keys-empty", func(n map[string]any) { n["x-kubernetes-list-map-keys"] = []any{} }, nil},
		{"key-name-empty", func(n map[string]any) { n["x-kubernetes-list-map-keys"] = []any{""} }, nil},
		{"key-name-non-string", func(n map[string]any) { n["x-kubernetes-list-map-keys"] = []any{int64(1)} }, nil},
		{"key-name-invalid-utf8", func(n map[string]any) { n["x-kubernetes-list-map-keys"] = []any{"bad\xff"} }, nil},
		{"key-name-duplicate", func(n map[string]any) { n["x-kubernetes-list-map-keys"] = []any{"name", "name"} }, nil},
		{"items-absent", func(n map[string]any) { delete(n, "items") }, nil},
		{"items-null", func(n map[string]any) { n["items"] = nil }, nil},
		{"items-typed-nil", func(n map[string]any) { n["items"] = map[string]any(nil) }, nil},
		{"items-not-schema", func(n map[string]any) { n["items"] = []any{} }, nil},
		{"items-non-object", func(n map[string]any) { n["items"].(map[string]any)["type"] = "string" }, nil},
		{"items-inferred-object-not-explicit", func(n map[string]any) { delete(n["items"].(map[string]any), "type") }, nil},
		{"items-nullable", func(n map[string]any) { n["items"].(map[string]any)["nullable"] = true }, nil},
		{"items-malformed-nullable", func(n map[string]any) { n["items"].(map[string]any)["nullable"] = []any{} }, nil},
		{"items-composite", func(n map[string]any) { n["items"].(map[string]any)["oneOf"] = []any{map[string]any{}} }, nil},
		{"required-absent", func(n map[string]any) { delete(n["items"].(map[string]any), "required") }, nil},
		{"required-not-array", func(n map[string]any) { n["items"].(map[string]any)["required"] = "name" }, nil},
		{"required-null", func(n map[string]any) { n["items"].(map[string]any)["required"] = nil }, nil},
		{"identity-not-required", func(n map[string]any) { n["items"].(map[string]any)["required"] = []any{"payload"} }, nil},
		{"late-required-non-string", func(n map[string]any) { n["items"].(map[string]any)["required"] = []any{"name", int64(1)} }, nil},
		{"late-required-invalid-utf8", func(n map[string]any) { n["items"].(map[string]any)["required"] = []any{"name", "bad\xff"} }, nil},
		{"properties-absent", func(n map[string]any) { delete(n["items"].(map[string]any), "properties") }, nil},
		{"properties-not-object", func(n map[string]any) { n["items"].(map[string]any)["properties"] = []any{} }, nil},
		{"properties-typed-nil", func(n map[string]any) { n["items"].(map[string]any)["properties"] = map[string]any(nil) }, nil},
		{"key-schema-absent", func(n map[string]any) { delete(n["items"].(map[string]any)["properties"].(map[string]any), "name") }, nil},
		{"key-schema-null", func(n map[string]any) { n["items"].(map[string]any)["properties"].(map[string]any)["name"] = nil }, nil},
		{"key-schema-not-object", func(n map[string]any) { n["items"].(map[string]any)["properties"].(map[string]any)["name"] = "string" }, nil},
		{"key-schema-typed-nil", func(n map[string]any) {
			n["items"].(map[string]any)["properties"].(map[string]any)["name"] = map[string]any(nil)
		}, nil},
		{"key-type-absent", func(n map[string]any) {
			delete(n["items"].(map[string]any)["properties"].(map[string]any)["name"].(map[string]any), "type")
		}, nil},
		{"key-type-number", func(n map[string]any) {
			n["items"].(map[string]any)["properties"].(map[string]any)["name"].(map[string]any)["type"] = "number"
		}, nil},
		{"key-type-malformed", func(n map[string]any) {
			n["items"].(map[string]any)["properties"].(map[string]any)["name"].(map[string]any)["type"] = []any{"string"}
		}, nil},
		{"key-int-or-string", func(n map[string]any) {
			n["items"].(map[string]any)["properties"].(map[string]any)["name"].(map[string]any)["x-kubernetes-int-or-string"] = true
		}, nil},
		{"key-nullable", func(n map[string]any) {
			n["items"].(map[string]any)["properties"].(map[string]any)["name"].(map[string]any)["nullable"] = true
		}, nil},
		{"key-nullable-malformed", func(n map[string]any) {
			n["items"].(map[string]any)["properties"].(map[string]any)["name"].(map[string]any)["nullable"] = map[string]any{}
		}, nil},
		{"key-composite", func(n map[string]any) {
			n["items"].(map[string]any)["properties"].(map[string]any)["name"].(map[string]any)["allOf"] = []any{map[string]any{"type": "string"}}
		}, nil},
		{"default-without-required-is-unsupported", func(n map[string]any) {
			n["items"].(map[string]any)["properties"].(map[string]any)["name"].(map[string]any)["default"] = "PRIVATE_DEFAULT"
			n["items"].(map[string]any)["required"] = []any{}
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			property := listMapTestSchema("name")
			tc.change(property)
			// An empty draft must not suppress inspection of advertised metadata.
			listMapTestWant(t, listMapTestCheck(t, property, []any{}), true, false, tc.codes...)
		})
	}
	for _, tc := range []struct {
		name  string
		value any
		codes []string
	}{
		{"late-missing-key", map[string]any{}, []string{"required"}},
		{"late-null-key", map[string]any{"name": nil}, []string{"type"}},
		{"late-number-key", map[string]any{"name": json.Number("1")}, []string{"type"}},
		{"late-boolean-key", map[string]any{"name": true}, []string{"type"}},
		{"late-object-key", map[string]any{"name": map[string]any{}}, []string{"type"}},
		{"late-array-key", map[string]any{"name": []any{}}, []string{"type"}},
		{"late-invalid-utf8-value", map[string]any{"name": "bad\xff"}, nil},
		{"late-overlong-value", map[string]any{"name": strings.Repeat("x", MaxListMapValueBytes+1)}, nil},
		{"late-non-object-item", "PRIVATE_ITEM", []string{"type"}},
		{"late-null-item", nil, []string{"type"}},
		{"late-typed-nil-object", map[string]any(nil), []string{"required"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := []any{map[string]any{"name": "same"}, map[string]any{"name": "same"}, tc.value}
			listMapTestWant(t, listMapTestCheck(t, listMapTestSchema("name"), items), true, false, tc.codes...)
		})
	}
	t.Run("late-second-key-metadata-prevents-duplicate", func(t *testing.T) {
		property := listMapTestSchema("name", "zone")
		property["items"].(map[string]any)["properties"].(map[string]any)["zone"].(map[string]any)["nullable"] = true
		items := listMapTestItems([]string{"name", "zone"}, []string{"same", "east"}, []string{"same", "east"})
		listMapTestWant(t, listMapTestCheck(t, property, items), true, false)
	})
	t.Run("late-eighth-key-is-not-optional", func(t *testing.T) {
		keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
		values := []string{"same", "same", "same", "same", "same", "same", "same", "same"}
		items := listMapTestItems(keys, values, values, values)
		delete(items[2].(map[string]any), "h")
		listMapTestWant(t, listMapTestCheck(t, listMapTestSchema(keys...), items), true, false, "required")
	})
	t.Run("malformed-metadata-on-wrong-type", func(t *testing.T) {
		property := listMapTestSchema("name")
		property["items"].(map[string]any)["required"] = []any{"name", false}
		listMapTestWant(t, listMapTestCheck(t, property, "wrong"), true, false, "type")
	})
	t.Run("malformed-metadata-on-nullable-null", func(t *testing.T) {
		property := listMapTestSchema("name")
		property["nullable"] = true
		property["items"].(map[string]any)["required"] = []any{"name", false}
		listMapTestWant(t, listMapTestCheck(t, property, nil), true, false)
	})
	t.Run("no-list-annotations-preserve-old-array-behavior", func(t *testing.T) {
		property := listMapTestSchema("name")
		delete(property, "x-kubernetes-list-type")
		delete(property, "x-kubernetes-list-map-keys")
		items := listMapTestItems([]string{"name"}, []string{"same"}, []string{"same"})
		listMapTestWant(t, listMapTestCheck(t, property, items), false, false)
	})
	t.Run("local-items-and-key-refs", func(t *testing.T) {
		property := listMapTestSchema("name")
		d := listMapTestDocument(t, property)
		item := property["items"].(map[string]any)
		d.definitions["Entry"] = item
		d.definitions["Key/with~escape"] = map[string]any{"type": "string", "nullable": false}
		item["properties"].(map[string]any)["name"] = map[string]any{"$ref": "#/components/schemas/Key~1with~0escape"}
		property["items"] = map[string]any{"$ref": "#/components/schemas/Entry"}
		r, err := d.Check(context.Background(), constraintObject(listMapTestItems([]string{"name"}, []string{"same"}, []string{"same"})))
		if err != nil {
			t.Fatal(err)
		}
		listMapTestWant(t, r, false, true)
	})
	t.Run("late-key-ref-cycle-prevents-duplicate", func(t *testing.T) {
		property := listMapTestSchema("name", "zone")
		d := listMapTestDocument(t, property)
		d.definitions["Cycle"] = map[string]any{"$ref": "#/components/schemas/Cycle"}
		property["items"].(map[string]any)["properties"].(map[string]any)["zone"] = map[string]any{"$ref": "#/components/schemas/Cycle"}
		c, r := listMapTestChecker(context.Background())
		c.checkListMap(d, property, listMapTestItems([]string{"name", "zone"}, []string{"same", "same"}, []string{"same", "same"}), "/value", true)
		listMapTestWant(t, *r, true, false)
	})
	t.Run("wrapped-key-ref-annotations", func(t *testing.T) {
		property := listMapTestSchema("name")
		d := listMapTestDocument(t, property)
		d.definitions["Key"] = map[string]any{"type": "string"}
		property["items"].(map[string]any)["properties"].(map[string]any)["name"] = map[string]any{
			"allOf": []any{map[string]any{"$ref": "#/components/schemas/Key"}}, "description": "display annotation",
		}
		r, err := d.Check(context.Background(), constraintObject(listMapTestItems([]string{"name"}, []string{"same"}, []string{"same"})))
		if err != nil {
			t.Fatal(err)
		}
		listMapTestWant(t, r, false, true)
	})
	for _, tc := range []struct {
		name string
		ref  any
	}{
		{"external-item-ref", "https://example.invalid/private/schema"},
		{"unknown-local-item-ref", "#/components/schemas/Absent"},
		{"malformed-item-ref", "#/components/schemas/bad~2escape"},
		{"non-string-item-ref", []any{"#/components/schemas/Entry"}},
		{"unsupported-local-ref-root", "#/definitions/Entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			property := listMapTestSchema("name")
			property["items"] = map[string]any{"$ref": tc.ref}
			listMapTestWant(t, listMapTestCheck(t, property, []any{}), true, false)
		})
	}
	for _, tc := range []struct {
		name string
		two  bool
	}{
		{"self-cyclic-local-ref", false}, {"two-node-cyclic-local-ref", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			property := listMapTestSchema("name")
			d := listMapTestDocument(t, property)
			d.definitions["CycleA"] = map[string]any{"$ref": "#/components/schemas/CycleA"}
			if tc.two {
				d.definitions["CycleA"] = map[string]any{"$ref": "#/components/schemas/CycleB"}
				d.definitions["CycleB"] = map[string]any{"$ref": "#/components/schemas/CycleA"}
			}
			property["items"] = map[string]any{"$ref": "#/components/schemas/CycleA"}
			r, err := d.Check(context.Background(), constraintObject([]any{}))
			if err != nil {
				t.Fatal(err)
			}
			listMapTestWant(t, r, true, false)
		})
	}
	for _, tc := range []struct {
		name, sibling string
		partial       bool
	}{
		{"single-ref-wrapper-annotations", "description", false},
		{"wrapper-format-not-flattened", "format", true},
		{"wrapper-list-type-not-flattened", "x-kubernetes-list-type", true},
		{"wrapper-list-keys-not-flattened", "x-kubernetes-list-map-keys", true},
		{"wrapper-map-type-not-flattened", "x-kubernetes-map-type", true},
		{"wrapper-structural-sibling-not-flattened", "required", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			property := listMapTestSchema("name")
			d := listMapTestDocument(t, property)
			d.definitions["Entry"] = property["items"]
			property["items"] = map[string]any{"allOf": []any{map[string]any{"$ref": "#/components/schemas/Entry"}}, tc.sibling: "PRIVATE_ANNOTATION"}
			items := []any{}
			if !tc.partial {
				items = listMapTestItems([]string{"name"}, []string{"same"}, []string{"same"})
			}
			r, err := d.Check(context.Background(), constraintObject(items))
			if err != nil {
				t.Fatal(err)
			}
			listMapTestWant(t, r, tc.partial, !tc.partial)
		})
	}
}

func TestSchemaListMapBudgetsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		length  int
		partial bool
	}{
		{"key-name-at-byte-boundary", MaxListMapNameBytes, false},
		{"key-name-over-byte-boundary", MaxListMapNameBytes + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := strings.Repeat("é", MaxListMapNameBytes/2) + strings.Repeat("x", tc.length-MaxListMapNameBytes)
			listMapTestWant(t, listMapTestCheck(t, listMapTestSchema(key), listMapTestItems([]string{key}, []string{"same"}, []string{"same"})), tc.partial, !tc.partial)
		})
	}
	for _, tc := range []struct {
		name    string
		length  int
		partial bool
	}{
		{"key-value-at-byte-boundary", MaxListMapValueBytes, false},
		{"key-value-over-byte-boundary", MaxListMapValueBytes + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := strings.Repeat("🚢", MaxListMapValueBytes/4) + strings.Repeat("x", tc.length-MaxListMapValueBytes)
			listMapTestWant(t, listMapTestCheck(t, listMapTestSchema("name"), listMapTestItems([]string{"name"}, []string{value}, []string{value})), tc.partial, !tc.partial)
		})
	}
	for _, tc := range []struct {
		name    string
		count   int
		partial bool
	}{
		{"items-at-boundary", MaxListMapItems, false},
		{"items-over-boundary", MaxListMapItems + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]any, tc.count)
			for i := range items {
				items[i] = map[string]any{"name": strconv.Itoa(i)}
			}
			items[len(items)-1] = map[string]any{"name": "0"}
			listMapTestWant(t, listMapTestCheck(t, listMapTestSchema("name"), items), tc.partial, !tc.partial)
		})
	}
	t.Run("all-eight-keys-and-256-items-share-document-budget", func(t *testing.T) {
		keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
		items := make([]any, MaxListMapItems)
		for i := range items {
			item := map[string]any{}
			for _, key := range keys {
				item[key] = strconv.Itoa(i)
			}
			items[i] = item
		}
		property := listMapTestSchema(keys...)
		d := listMapTestDocument(t, property)
		c, r := listMapTestChecker(context.Background())
		c.checkListMap(d, property, items, "/value", true)
		listMapTestWant(t, *r, false, false)
		// Per-feature maxima do not promise the Cartesian product also fits the
		// complete document's shared structural and constraint work allowance.
		full, err := d.Check(context.Background(), constraintObject(items))
		if err != nil {
			t.Fatal(err)
		}
		listMapTestWant(t, full, true, false)
		if full.Checked > MaxWork+1 {
			t.Fatal("combined limits bypassed the shared document budget", full.Checked)
		}
	})
	t.Run("ninth-key-is-incomplete", func(t *testing.T) {
		listMapTestWant(t, listMapTestCheck(t, listMapTestSchema("a", "b", "c", "d", "e", "f", "g", "h", "i"), []any{}), true, false)
	})
	t.Run("typed-nil-array-is-not-empty-json-array", func(t *testing.T) {
		listMapTestWant(t, listMapTestCheck(t, listMapTestSchema("name"), []any(nil)), true, false)
	})
	t.Run("wrong-type-does-not-scan-large-draft", func(t *testing.T) {
		large := make(map[string]any, MaxWork+1)
		for i := 0; i <= MaxWork; i++ {
			large[strconv.Itoa(i)] = strings.Repeat("x", 32)
		}
		r := listMapTestCheck(t, listMapTestSchema("name"), large)
		listMapTestWant(t, r, false, false, "type")
		if r.Checked >= 256 {
			t.Fatal("wrong-type identity check traversed draft payload", r.Checked)
		}
	})
	t.Run("early-duplicate-late-work-exhaustion", func(t *testing.T) {
		items := make([]any, MaxListMapItems)
		value := strings.Repeat("x", MaxListMapValueBytes)
		for i := range items {
			items[i] = map[string]any{"name": value}
		}
		r := listMapTestCheck(t, listMapTestSchema("name"), items)
		listMapTestWant(t, r, true, false)
		if r.Checked > MaxWork+1 {
			t.Fatal("identity work escaped the shared allowance", r.Checked)
		}
	})
	t.Run("work-is-shared-between-list-fields", func(t *testing.T) {
		fields, values := map[string]any{}, map[string]any{}
		for i := 0; i < 3; i++ {
			name := "field" + strconv.Itoa(i)
			fields[name] = listMapTestSchema("name")
			items := make([]any, MaxListMapItems)
			for j := range items {
				items[j] = map[string]any{"name": strconv.Itoa(j)}
			}
			values[name] = items
		}
		r := listMapTestCheck(t, map[string]any{"type": "object", "properties": fields}, values)
		listMapTestWant(t, r, true, false)
		if r.Checked > MaxWork+1 {
			t.Fatal("independent fields received independent work allowances", r.Checked)
		}
	})
	t.Run("metadata-required-scan-is-budgeted", func(t *testing.T) {
		property := listMapTestSchema("name")
		required := make([]any, MaxWork)
		for i := range required {
			required[i] = "name"
		}
		property["items"].(map[string]any)["required"] = required
		listMapTestWant(t, listMapTestCheck(t, property, []any{}), true, false)
	})
	t.Run("late-byte-limit-cannot-emit-early-duplicate", func(t *testing.T) {
		property := listMapTestSchema("name")
		d := listMapTestDocument(t, property)
		items := listMapTestItems([]string{"name"}, []string{"same"}, []string{"same"}, []string{"later"})
		baseline, baselineReport := listMapTestChecker(context.Background())
		baseline.checkListMap(d, property, items, "/value", true)
		listMapTestWant(t, *baselineReport, false, true)
		c, r := listMapTestChecker(context.Background())
		c.bytes = MaxConstraintBytes - baseline.bytes + 1
		c.checkListMap(d, property, items, "/value", true)
		listMapTestWant(t, *r, true, false)
		if !c.exhausted || c.bytes > MaxConstraintBytes {
			t.Fatal("late identity hash exceeded byte budget")
		}
	})
	t.Run("late-work-limit-cannot-emit-early-duplicate", func(t *testing.T) {
		property := listMapTestSchema("name")
		d := listMapTestDocument(t, property)
		items := listMapTestItems([]string{"name"}, []string{"same"}, []string{"same"}, []string{"later"})
		baseline, baselineReport := listMapTestChecker(context.Background())
		baseline.checkListMap(d, property, items, "/value", true)
		listMapTestWant(t, *baselineReport, false, true)
		c, r := listMapTestChecker(context.Background())
		r.Checked = MaxWork - baselineReport.Checked + 1
		c.checkListMap(d, property, items, "/value", true)
		listMapTestWant(t, *r, true, false)
		if !c.exhausted || r.Checked > MaxWork {
			t.Fatal("late identity hash exceeded work budget")
		}
	})
	t.Run("identity-byte-budget-is-shared-with-scalar-enum", func(t *testing.T) {
		property := listMapTestSchema("name")
		d := listMapTestDocument(t, property)
		items := listMapTestItems([]string{"name"}, []string{"same"}, []string{"same"})
		baseline, baselineReport := listMapTestChecker(context.Background())
		baseline.checkListMap(d, property, items, "/value", true)
		listMapTestWant(t, *baselineReport, false, true)
		c, r := listMapTestChecker(context.Background())
		// A preceding pure-scalar metadata check legitimately uses the same
		// byte allowance without applying its value constraint to this draft.
		prior := strings.Repeat("x", MaxConstraintBytes-baseline.bytes+1)
		c.checkEnum(map[string]any{"enum": []any{prior}}, nil, "/other", false)
		constraintWant(t, *r, false)
		c.checkListMap(d, property, items, "/value", true)
		listMapTestWant(t, *r, true, false)
		if !c.exhausted || c.bytes > MaxConstraintBytes {
			t.Fatal("list identity received a fresh allowance after scalar inspection")
		}
	})
	t.Run("complete-tuple-cost-independent-of-early-match", func(t *testing.T) {
		property := listMapTestSchema("name", "zone")
		d := listMapTestDocument(t, property)
		duplicate := listMapTestItems([]string{"name", "zone"}, []string{"aa", "bb"}, []string{"aa", "bb"}, []string{"cc", "dd"})
		distinct := listMapTestItems([]string{"name", "zone"}, []string{"aa", "bb"}, []string{"aa", "cc"}, []string{"cc", "dd"})
		first, a := listMapTestChecker(context.Background())
		second, b := listMapTestChecker(context.Background())
		first.checkListMap(d, property, duplicate, "/value", true)
		second.checkListMap(d, property, distinct, "/value", true)
		listMapTestWant(t, *a, false, true)
		listMapTestWant(t, *b, false, false)
		if a.Checked != b.Checked || first.bytes != second.bytes {
			t.Fatal("early duplicate changed complete identity accounting")
		}
	})
	for _, tc := range []struct {
		name    string
		links   int
		partial bool
	}{
		{"reference-chain-at-resolver-limit", 31, false},
		{"reference-chain-over-resolver-limit", 32, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			property := listMapTestSchema("name")
			d := listMapTestDocument(t, property)
			raw := property["items"]
			for i := tc.links - 1; i >= 0; i-- {
				name := "Chain" + strconv.Itoa(i)
				d.definitions[name] = raw
				raw = map[string]any{"$ref": "#/components/schemas/" + name}
			}
			property["items"] = raw
			r, err := d.Check(context.Background(), constraintObject([]any{}))
			if err != nil {
				t.Fatal(err)
			}
			listMapTestWant(t, r, tc.partial, false)
		})
	}
	t.Run("meter-rejects-before-first-reference-traversal", func(t *testing.T) {
		d := listMapTestDocument(t, listMapTestSchema("name"))
		calls := 0
		resolved, err := d.resolveWithBudget(context.Background(), map[string]any{"$ref": "#/components/schemas/PRIVATE_UNKNOWN"}, func(work, size int) bool { calls++; return false })
		if !errors.Is(err, ErrLimit) || resolved != nil || calls != 1 {
			t.Fatal("resolver traversed after its first budget rejection", err, calls)
		}
	})
	t.Run("multilevel-reference-cost-shares-the-remaining-budget", func(t *testing.T) {
		d := listMapTestDocument(t, listMapTestSchema("name"))
		d.definitions["A"] = map[string]any{"$ref": "#/components/schemas/B"}
		d.definitions["B"] = map[string]any{"$ref": "#/components/schemas/C"}
		d.definitions["C"] = map[string]any{"type": "string"}
		start := map[string]any{"$ref": "#/components/schemas/A"}
		baseline, baselineReport := listMapTestChecker(context.Background())
		resolved, err := d.resolveWithBudget(context.Background(), start, baseline.chargeWork)
		if err != nil || resolved["type"] != "string" {
			t.Fatal("bounded local chain failed", err)
		}
		c, r := listMapTestChecker(context.Background())
		c.bytes = MaxConstraintBytes - baseline.bytes + 1
		resolved, err = d.resolveWithBudget(context.Background(), start, c.chargeWork)
		if !errors.Is(err, ErrLimit) || resolved != nil || !r.Partial || !c.exhausted || r.Checked >= baselineReport.Checked {
			t.Fatal("late reference budget failure returned a resolved schema", err)
		}
	})
	t.Run("long-schema-key-is-charged-before-wrapper-scan", func(t *testing.T) {
		property := listMapTestSchema("name")
		property["items"].(map[string]any)[strings.Repeat("x", MaxConstraintBytes/2+1)] = true
		c, r := listMapTestChecker(context.Background())
		c.checkListMap(listMapTestDocument(t, property), property, []any{}, "/value", true)
		listMapTestWant(t, *r, true, false)
		if !c.exhausted {
			t.Fatal("schema keyword scan escaped byte accounting")
		}
	})
	t.Run("overwide-item-schema-is-incomplete", func(t *testing.T) {
		property := listMapTestSchema("name")
		item := property["items"].(map[string]any)
		for len(item) <= MaxSchemaKeys {
			item["x-unknown-"+strconv.Itoa(len(item))] = true
		}
		listMapTestWant(t, listMapTestCheck(t, property, []any{}), true, false)
	})
	t.Run("cancel-before-inspection", func(t *testing.T) {
		d := listMapTestDocument(t, listMapTestSchema("name"))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r, err := d.Check(ctx, constraintObject([]any{}))
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(r, Report{}) {
			t.Fatal("canceled check retained an identity report", err)
		}
	})
	t.Run("checker-discards-duplicate-when-canceled-during-hashing", func(t *testing.T) {
		property := listMapTestSchema("name")
		items := make([]any, MaxListMapItems)
		for i := range items {
			items[i] = map[string]any{"name": "same"}
		}
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &constraintCancelAfter{Context: base, cancel: cancel, after: 700}
		c, r := listMapTestChecker(ctx)
		c.checkListMap(listMapTestDocument(t, property), property, items, "/value", true)
		listMapTestWant(t, *r, true, false)
		if !errors.Is(base.Err(), context.Canceled) || !c.exhausted {
			t.Fatal("in-flight identity hashing ignored cancellation")
		}
	})
	for _, tc := range []struct {
		name  string
		after int
	}{
		{"cancel-during-metadata", 10},
		{"cancel-during-projection", 80},
		{"cancel-after-apparent-duplicate", 700},
	} {
		t.Run(tc.name, func(t *testing.T) {
			property := listMapTestSchema("name")
			d := listMapTestDocument(t, property)
			items := make([]any, MaxListMapItems)
			for i := range items {
				items[i] = map[string]any{"name": "same"}
			}
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &constraintCancelAfter{Context: base, cancel: cancel, after: tc.after}
			r, err := d.Check(ctx, constraintObject(items))
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(r, Report{}) {
				t.Fatal("in-flight cancellation retained an identity report", ctx.calls, err)
			}
		})
	}
}

func TestSchemaListMapInspectionPreservesInputsAndPrivacy(t *testing.T) {
	t.Run("input-schema-help-and-shared-checks-unchanged", func(t *testing.T) {
		key := "PRIVATE_IDENTITY_KEY"
		property := listMapTestSchema(key)
		property["default"] = []any{map[string]any{key: "PRIVATE_DEFAULT"}}
		property["example"] = []any{map[string]any{key: "PRIVATE_EXAMPLE"}}
		d, raw := constraintDocument(t, constraintEncode(t, property))
		input := constraintObject([]any{
			map[string]any{key: "PRIVATE_IDENTITY_VALUE", "payload": map[string]any{"PRIVATE_PAYLOAD": json.Number("9007199254740993")}},
			map[string]any{key: "PRIVATE_IDENTITY_VALUE", "payload": []any{"PRIVATE_OTHER_PAYLOAD", nil}},
		})
		beforeInput, beforeDefinitions, beforeRaw := constraintEncode(t, input), constraintEncode(t, d.definitions), bytes.Clone(raw)
		helpBefore, err := d.Help(context.Background(), "/value")
		if err != nil {
			t.Fatal(err)
		}
		want, err := d.Check(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		listMapTestWant(t, want, false, true)
		if strings.Contains(want.Text()+constraintEncode(t, want), "PRIVATE_") {
			t.Fatal("identity hint disclosed key names, values or payload")
		}
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 8; j++ {
					r, err := d.Check(context.Background(), input)
					if err != nil || !reflect.DeepEqual(r, want) {
						t.Error("shared read-only identity check changed its report", err)
					}
				}
			}()
		}
		wg.Wait()
		helpAfter, err := d.Help(context.Background(), "/value")
		if err != nil || !reflect.DeepEqual(helpBefore, helpAfter) {
			t.Fatal("identity inspection changed field help", err)
		}
		if beforeInput != constraintEncode(t, input) || beforeDefinitions != constraintEncode(t, d.definitions) || !bytes.Equal(beforeRaw, raw) {
			t.Fatal("identity inspection changed source, values, schema or defaults")
		}
	})
	t.Run("non-key-payload-is-not-traversed-or-counted", func(t *testing.T) {
		property := listMapTestSchema("name")
		d := listMapTestDocument(t, property)
		plain := listMapTestItems([]string{"name"}, []string{"same"}, []string{"same"})
		cycle := map[string]any{}
		cycle["self"] = cycle
		large := strings.Repeat("x", MaxConstraintBytes+1)
		opaque := []any{
			map[string]any{"name": "same", "payload": cycle},
			map[string]any{"name": "same", "payload": []any{large, make(chan int), "bad\xff"}},
		}
		first, a := listMapTestChecker(context.Background())
		second, b := listMapTestChecker(context.Background())
		first.checkListMap(d, property, plain, "/value", true)
		second.checkListMap(d, property, opaque, "/value", true)
		listMapTestWant(t, *a, false, true)
		listMapTestWant(t, *b, false, true)
		if a.Checked != b.Checked || first.bytes != second.bytes {
			t.Fatal("non-key payload entered identity accounting")
		}
		if opaque[0].(map[string]any)["payload"].(map[string]any)["self"] == nil || opaque[1].(map[string]any)["payload"].([]any)[0] != large {
			t.Fatal("identity projection changed uninspected payload")
		}
	})
	t.Run("nil-and-metered-resolver-preserve-overlay-semantics", func(t *testing.T) {
		property := listMapTestSchema("name")
		d := listMapTestDocument(t, property)
		d.definitions["Leaf"] = map[string]any{"type": "string", "description": "leaf", "title": "leaf-title"}
		d.definitions["Middle"] = map[string]any{"$ref": "#/components/schemas/Leaf", "description": "middle"}
		wrapper := map[string]any{"allOf": []any{map[string]any{"$ref": "#/components/schemas/Middle"}}, "description": "outer", "title": "outer-title"}
		before, wrapperBefore := constraintEncode(t, d.definitions), constraintEncode(t, wrapper)
		legacy, err := d.resolve(context.Background(), wrapper)
		if err != nil {
			t.Fatal(err)
		}
		c, r := listMapTestChecker(context.Background())
		metered, err := d.resolveWithBudget(context.Background(), wrapper, c.chargeWork)
		if err != nil || !reflect.DeepEqual(legacy, metered) || r.Partial {
			t.Fatal("optional budget changed successful reference semantics", err)
		}
		if metered["description"] != "outer" || metered["title"] != "outer-title" || metered["type"] != "string" || c.bytes == 0 || r.Checked == 0 {
			t.Fatal("reference annotations or metering were lost")
		}
		if before != constraintEncode(t, d.definitions) || wrapperBefore != constraintEncode(t, wrapper) {
			t.Fatal("resolver copied annotations into shared definitions")
		}
		metered["description"] = "modified-result-only"
		if before != constraintEncode(t, d.definitions) || wrapperBefore != constraintEncode(t, wrapper) {
			t.Fatal("annotation overlay aliased the original schema")
		}
	})
	t.Run("nil-and-metered-resolver-preserve-uninspected-wrapper", func(t *testing.T) {
		d := listMapTestDocument(t, listMapTestSchema("name"))
		d.definitions["Leaf"] = map[string]any{"type": "string"}
		wrapper := map[string]any{"allOf": []any{map[string]any{"$ref": "#/components/schemas/Leaf"}}, "format": "uuid"}
		legacy, err := d.resolve(context.Background(), wrapper)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := listMapTestChecker(context.Background())
		metered, err := d.resolveWithBudget(context.Background(), wrapper, c.chargeWork)
		if err != nil || !reflect.DeepEqual(legacy, metered) || metered["x-aster-uninspected-reference"] != true {
			t.Fatal("optional resolver flattened uninspected wrapper semantics", err)
		}
		if _, present := d.definitions["Leaf"].(map[string]any)["x-aster-uninspected-reference"]; present {
			t.Fatal("wrapper marker polluted a shared definition")
		}
	})
}
