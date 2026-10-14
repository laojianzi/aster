package schemaassist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func listSetTestSchema() map[string]any {
	return map[string]any{"type": "array", "x-kubernetes-list-type": "set", "items": map[string]any{"type": "string"}}
}

func listSetTestDocument(t testing.TB, property map[string]any) *Document {
	t.Helper()
	d, _ := constraintDocument(t, `{}`)
	d.root["properties"].(map[string]any)["value"] = property
	return d
}

func listSetTestCheck(t testing.TB, property map[string]any, value any) Report {
	t.Helper()
	r, err := listSetTestDocument(t, property).Check(context.Background(), constraintObject(value))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func listSetTestWant(t testing.TB, r Report, partial, duplicate bool, codes ...string) {
	t.Helper()
	if duplicate {
		codes = append(codes, "list-set")
	}
	constraintWant(t, r, partial, codes...)
	for _, diagnostic := range r.Diagnostics {
		if diagnostic.Code == "list-set" && (diagnostic.Pointer != "/value" || diagnostic.Message != "List entries repeat the advertised string value.") {
			t.Fatalf("set hint lost its generic parent location: %+v", diagnostic)
		}
	}
}

func TestSchemaListSetStringEquality(t *testing.T) {
	for _, tc := range []struct {
		name      string
		values    []any
		duplicate bool
	}{
		{"empty-array", []any{}, false},
		{"single-empty-string", []any{""}, false},
		{"distinct-values", []any{"api", "worker"}, false},
		{"exact-duplicate", []any{"api", "api"}, true},
		{"empty-is-a-value", []any{"", ""}, true},
		{"late-duplicate", []any{"api", "worker", "batch", "api"}, true},
		{"multiple-classes-one-parent-hint", []any{"api", "worker", "api", "api", "worker"}, true},
		{"case-not-folded", []any{"API", "api"}, false},
		{"spaces-not-trimmed", []any{"api", " api", "api "}, false},
		{"unicode-and-astral-exact", []any{"中文船🚢", "中文船🚢"}, true},
		{"unicode-normalization-not-applied", []any{"é", "e\u0301"}, false},
		{"line-endings-not-normalized", []any{"a\r\nb", "a\nb"}, false},
		{"embedded-nul-exact", []any{"a\x00b", "a\x00b"}, true},
		{"embedded-nul-not-terminator", []any{"a", "a\x00", "a\x00b"}, false},
		{"newline-and-tab-exact", []any{"x\ny\tz", "x\ny\tz"}, true},
		{"numeric-looking-strings-retain-spelling", []any{"1", "1.0", "01", "true", "null"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := constraintDocument(t, constraintEncode(t, listSetTestSchema()))
			r, err := d.Check(context.Background(), constraintObject(tc.values))
			if err != nil {
				t.Fatal(err)
			}
			listSetTestWant(t, r, false, tc.duplicate)
		})
	}
	t.Run("decoded-json-escapes-compare-as-values", func(t *testing.T) {
		var values []any
		if err := json.Unmarshal([]byte(`["ship🚢","ship\ud83d\udea2","\u0061","a"]`), &values); err != nil {
			t.Fatal(err)
		}
		listSetTestWant(t, listSetTestCheck(t, listSetTestSchema(), values), false, true)
	})
	t.Run("nullable-parent-null-has-no-members", func(t *testing.T) {
		property := listSetTestSchema()
		property["nullable"] = true
		listSetTestWant(t, listSetTestCheck(t, property, nil), false, false)
	})
	for _, value := range []any{nil, "PRIVATE_WRONG_TYPE", map[string]any{}, int64(1)} {
		t.Run("wrong-parent-"+strconv.Itoa(len(constraintEncode(t, value))), func(t *testing.T) {
			listSetTestWant(t, listSetTestCheck(t, listSetTestSchema(), value), false, false, "type")
		})
	}
	t.Run("ordinary-array-does-not-gain-uniqueness", func(t *testing.T) {
		property := listSetTestSchema()
		delete(property, "x-kubernetes-list-type")
		listSetTestWant(t, listSetTestCheck(t, property, []any{"same", "same"}), false, false)
	})
	t.Run("item-constraints-are-independent", func(t *testing.T) {
		property := listSetTestSchema()
		property["items"].(map[string]any)["minLength"] = 3
		listSetTestWant(t, listSetTestCheck(t, property, []any{"x", "x"}), false, true, "minLength", "minLength")
	})
	t.Run("default-does-not-rewrite-explicit-strings", func(t *testing.T) {
		property := listSetTestSchema()
		property["items"].(map[string]any)["default"] = "PRIVATE_DEFAULT"
		listSetTestWant(t, listSetTestCheck(t, property, []any{"", "", "PRIVATE_DEFAULT"}), false, true)
	})
}

// The oracle sorts an owned copy and checks adjacent strings. It neither uses
// a hash table nor calls the production identity or enum comparison helpers.
func TestSchemaListSetMatchesIndependentStringOracle(t *testing.T) {
	corpus := []string{"", "a", "b", "A", "ab", "a|b", "a\x00b", "a\x00", "\x00", "\n", "\r\n", "\t", "é", "e\u0301", "船", "🚢", "船🚢", " a", "a ", "1", "1.0", "null", "true", "\u200b"}
	d, _ := constraintDocument(t, constraintEncode(t, listSetTestSchema()))
	comparisons := 0
	for _, first := range corpus {
		for _, second := range corpus {
			ordered := []string{first, "PRIVATE_MIDDLE", second}
			sorted := append([]string{}, ordered...)
			sort.Strings(sorted)
			duplicate := false
			for i := 1; i < len(sorted); i++ {
				duplicate = duplicate || sorted[i] == sorted[i-1]
			}
			values := make([]any, len(ordered))
			for i, text := range ordered {
				values[i] = text
			}
			r, err := d.Check(context.Background(), constraintObject(values))
			if err != nil {
				t.Fatal(err)
			}
			listSetTestWant(t, r, false, duplicate)
			comparisons++
		}
	}
	if comparisons != 576 {
		t.Fatal("independent string corpus was reduced", comparisons)
	}
	t.Logf("independent string oracle comparisons: %d", comparisons)
}

func TestSchemaListSetRejectsMalformedAndUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		codes  []string
	}{
		{"unknown-list-type", func(n map[string]any) { n["x-kubernetes-list-type"] = "PRIVATE_LIST_KIND" }, nil},
		{"non-string-list-type", func(n map[string]any) { n["x-kubernetes-list-type"] = []any{"set"} }, nil},
		{"atomic-is-unsupported", func(n map[string]any) { n["x-kubernetes-list-type"] = "atomic" }, nil},
		{"missing-parent-type", func(n map[string]any) { delete(n, "type") }, nil},
		{"malformed-parent-type", func(n map[string]any) { n["type"] = []any{"array"} }, nil},
		{"non-array-parent", func(n map[string]any) { n["type"] = "object" }, []string{"type"}},
		{"parent-int-or-string", func(n map[string]any) { n["x-kubernetes-int-or-string"] = true }, []string{"type"}},
		{"composite-parent", func(n map[string]any) { n["oneOf"] = []any{map[string]any{}} }, nil},
		{"map-keys-on-set", func(n map[string]any) { n["x-kubernetes-list-map-keys"] = []any{"PRIVATE_KEY"} }, nil},
		{"even-empty-map-keys-invalid", func(n map[string]any) { n["x-kubernetes-list-map-keys"] = []any{} }, nil},
		{"null-map-keys-invalid", func(n map[string]any) { n["x-kubernetes-list-map-keys"] = nil }, nil},
		{"missing-items", func(n map[string]any) { delete(n, "items") }, nil},
		{"null-items", func(n map[string]any) { n["items"] = nil }, nil},
		{"tuple-items", func(n map[string]any) { n["items"] = []any{map[string]any{"type": "string"}} }, nil},
		{"typed-nil-items", func(n map[string]any) { n["items"] = map[string]any(nil) }, nil},
		{"unspecified-items", func(n map[string]any) { n["items"] = map[string]any{} }, nil},
		{"nullable-integer-items", func(n map[string]any) { n["items"] = map[string]any{"type": "integer", "nullable": true} }, nil},
		{"number-items", func(n map[string]any) { n["items"] = map[string]any{"type": "number"} }, nil},
		{"object-items", func(n map[string]any) {
			n["items"] = map[string]any{"type": "object", "x-kubernetes-map-type": "atomic"}
		}, nil},
		{"array-items", func(n map[string]any) {
			n["items"] = map[string]any{"type": "array", "x-kubernetes-list-type": "atomic"}
		}, nil},
		{"malformed-item-type", func(n map[string]any) { n["items"] = map[string]any{"type": []any{"string"}} }, nil},
		{"nullable-items", func(n map[string]any) { n["items"].(map[string]any)["nullable"] = true }, nil},
		{"malformed-nullable-items", func(n map[string]any) { n["items"].(map[string]any)["nullable"] = "false" }, nil},
		{"int-or-string-items", func(n map[string]any) { n["items"].(map[string]any)["x-kubernetes-int-or-string"] = true }, nil},
		{"composite-items", func(n map[string]any) { n["items"].(map[string]any)["anyOf"] = []any{map[string]any{}} }, nil},
		{"unknown-item-ref", func(n map[string]any) { n["items"] = map[string]any{"$ref": "#/components/schemas/Missing"} }, nil},
		{"external-item-ref", func(n map[string]any) { n["items"] = map[string]any{"$ref": "https://example.invalid/PRIVATE"} }, nil},
		{"malformed-item-ref", func(n map[string]any) { n["items"] = map[string]any{"$ref": "#/components/schemas/bad~2escape"} }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			property := listSetTestSchema()
			tc.change(property)
			// Empty input cannot hide malformed advertised identity metadata.
			listSetTestWant(t, listSetTestCheck(t, property, []any{}), true, false, tc.codes...)
		})
	}
	for _, tc := range []struct {
		name  string
		value any
		codes []string
	}{
		{"late-null", nil, []string{"type"}},
		{"late-number", json.Number("1"), []string{"type"}},
		{"late-boolean", true, []string{"type"}},
		{"late-object", map[string]any{}, []string{"type"}},
		{"late-array", []any{}, []string{"type"}},
		{"late-typed-nil-array", []any(nil), []string{"type"}},
		{"late-invalid-utf8", "bad\xff", nil},
		{"late-overlong-value", strings.Repeat("x", MaxListSetValueBytes+1), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listSetTestWant(t, listSetTestCheck(t, listSetTestSchema(), []any{"same", "same", tc.value}), true, false, tc.codes...)
		})
	}
	t.Run("default-does-not-fill-null-locally", func(t *testing.T) {
		property := listSetTestSchema()
		property["items"].(map[string]any)["default"] = "PRIVATE_DEFAULT"
		listSetTestWant(t, listSetTestCheck(t, property, []any{"PRIVATE_DEFAULT", "PRIVATE_DEFAULT", nil}), true, false, "type")
	})
	for _, value := range []any{"wrong-parent", nil} {
		t.Run("bad-items-even-with-null-or-wrong-parent", func(t *testing.T) {
			property := listSetTestSchema()
			property["nullable"] = true
			property["items"] = map[string]any{"type": "string", "nullable": true}
			codes := []string{}
			if value != nil {
				codes = append(codes, "type")
			}
			listSetTestWant(t, listSetTestCheck(t, property, value), true, false, codes...)
		})
	}
	t.Run("local-escaped-item-reference", func(t *testing.T) {
		property := listSetTestSchema()
		d := listSetTestDocument(t, property)
		d.definitions["Value/with~escape"] = map[string]any{"type": "string", "nullable": false}
		property["items"] = map[string]any{"$ref": "#/components/schemas/Value~1with~0escape"}
		r, err := d.Check(context.Background(), constraintObject([]any{"same", "same"}))
		if err != nil {
			t.Fatal(err)
		}
		listSetTestWant(t, r, false, true)
	})
	for _, annotation := range []string{"description", "format", "nullable", "x-kubernetes-list-type"} {
		t.Run("item-ref-wrapper-"+annotation, func(t *testing.T) {
			property := listSetTestSchema()
			d := listSetTestDocument(t, property)
			d.definitions["Value"] = map[string]any{"type": "string"}
			property["items"] = map[string]any{"allOf": []any{map[string]any{"$ref": "#/components/schemas/Value"}}, annotation: "PRIVATE_ANNOTATION"}
			values := []any{}
			if annotation == "description" {
				values = []any{"same", "same"}
			}
			r, err := d.Check(context.Background(), constraintObject(values))
			if err != nil {
				t.Fatal(err)
			}
			listSetTestWant(t, r, annotation != "description", annotation == "description")
		})
	}
	t.Run("cyclic-reference-is-not-flattened", func(t *testing.T) {
		property := listSetTestSchema()
		d := listSetTestDocument(t, property)
		d.definitions["CycleA"] = map[string]any{"$ref": "#/components/schemas/CycleB"}
		d.definitions["CycleB"] = map[string]any{"$ref": "#/components/schemas/CycleA"}
		property["items"] = map[string]any{"$ref": "#/components/schemas/CycleA"}
		r, err := d.Check(context.Background(), constraintObject([]any{}))
		if err != nil {
			t.Fatal(err)
		}
		listSetTestWant(t, r, true, false)
	})
	t.Run("uninspected-parent-ref-sibling-prevents-duplicate", func(t *testing.T) {
		d := listSetTestDocument(t, map[string]any{"allOf": []any{map[string]any{"$ref": "#/components/schemas/Values"}}, "format": "PRIVATE_FORMAT"})
		d.definitions["Values"] = listSetTestSchema()
		r, err := d.Check(context.Background(), constraintObject([]any{"same", "same"}))
		if err != nil {
			t.Fatal(err)
		}
		listSetTestWant(t, r, true, false)
	})
}

func TestSchemaListSetBudgetsAndCancellation(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run("value-byte-boundary-"+strconv.Itoa(extra), func(t *testing.T) {
			value := strings.Repeat("🚢", MaxListSetValueBytes/4) + strings.Repeat("x", extra)
			listSetTestWant(t, listSetTestCheck(t, listSetTestSchema(), []any{value, value}), extra != 0, extra == 0)
		})
		t.Run("entry-count-boundary-"+strconv.Itoa(extra), func(t *testing.T) {
			values := make([]any, MaxListSetItems+extra)
			for i := range values {
				values[i] = strconv.Itoa(i)
			}
			values[len(values)-1] = "0"
			listSetTestWant(t, listSetTestCheck(t, listSetTestSchema(), values), extra != 0, extra == 0)
		})
	}
	t.Run("typed-nil-array-is-not-empty-json-array", func(t *testing.T) {
		listSetTestWant(t, listSetTestCheck(t, listSetTestSchema(), []any(nil)), true, false)
	})
	t.Run("early-duplicate-cannot-escape-shared-work", func(t *testing.T) {
		values := make([]any, MaxListSetItems)
		for i := range values {
			values[i] = strings.Repeat("x", MaxListSetValueBytes)
		}
		r := listSetTestCheck(t, listSetTestSchema(), values)
		listSetTestWant(t, r, true, false)
		if r.Checked > MaxWork+1 {
			t.Fatal("set checking bypassed the shared work limit", r.Checked)
		}
	})
	for _, meter := range []string{"bytes", "work"} {
		t.Run("late-"+meter+"-exhaustion-discards-apparent-duplicate", func(t *testing.T) {
			property := listSetTestSchema()
			d := listSetTestDocument(t, property)
			values := []any{"same", "same", "later"}
			baseline, baseReport := listMapTestChecker(context.Background())
			baseline.checkListSet(d, property, values, "/value", true)
			listSetTestWant(t, *baseReport, false, true)
			c, r := listMapTestChecker(context.Background())
			if meter == "bytes" {
				c.bytes = MaxConstraintBytes - baseline.bytes + 1
			} else {
				r.Checked = MaxWork - baseReport.Checked + 1
			}
			c.checkListSet(d, property, values, "/value", true)
			listSetTestWant(t, *r, true, false)
			if !c.exhausted || c.bytes > MaxConstraintBytes || r.Checked > MaxWork {
				t.Fatal("late failure escaped the shared limits")
			}
		})
	}
	t.Run("byte-budget-is-shared-with-prior-enum", func(t *testing.T) {
		property := listSetTestSchema()
		d := listSetTestDocument(t, property)
		values := []any{"same", "same", "later"}
		baseline, baseReport := listMapTestChecker(context.Background())
		baseline.checkListSet(d, property, values, "/value", true)
		listSetTestWant(t, *baseReport, false, true)
		c, r := listMapTestChecker(context.Background())
		prior := strings.Repeat("x", MaxConstraintBytes-baseline.bytes+1)
		c.checkEnum(map[string]any{"enum": []any{prior}}, nil, "/other", false)
		constraintWant(t, *r, false)
		c.checkListSet(d, property, values, "/value", true)
		listSetTestWant(t, *r, true, false)
	})
	t.Run("work-is-shared-across-map-and-set-fields", func(t *testing.T) {
		fields, values := map[string]any{}, map[string]any{}
		for i := 0; i < 16; i++ {
			name := "field" + strconv.Itoa(i)
			items := make([]any, MaxListSetItems)
			if i%2 == 0 {
				fields[name] = listMapTestSchema("name")
				for j := range items {
					items[j] = map[string]any{"name": strconv.Itoa(j)}
				}
			} else {
				fields[name] = listSetTestSchema()
				for j := range items {
					items[j] = strconv.Itoa(j)
				}
			}
			values[name] = items
		}
		r := listSetTestCheck(t, map[string]any{"type": "object", "properties": fields}, values)
		listSetTestWant(t, r, true, false)
		if r.Checked > MaxWork+1 {
			t.Fatal("list fields received independent work allowances", r.Checked)
		}
	})
	t.Run("early-match-does-not-reduce-full-cost", func(t *testing.T) {
		property := listSetTestSchema()
		d := listSetTestDocument(t, property)
		first, a := listMapTestChecker(context.Background())
		second, b := listMapTestChecker(context.Background())
		first.checkListSet(d, property, []any{"aa", "aa", "cc"}, "/value", true)
		second.checkListSet(d, property, []any{"aa", "bb", "cc"}, "/value", true)
		listSetTestWant(t, *a, false, true)
		listSetTestWant(t, *b, false, false)
		if a.Checked != b.Checked || first.bytes != second.bytes {
			t.Fatal("an early duplicate skipped later accounting")
		}
	})
	for _, links := range []int{31, 32} {
		t.Run("reference-depth-"+strconv.Itoa(links), func(t *testing.T) {
			property := listSetTestSchema()
			d := listSetTestDocument(t, property)
			raw := property["items"]
			for i := links - 1; i >= 0; i-- {
				name := "Chain" + strconv.Itoa(i)
				d.definitions[name] = raw
				raw = map[string]any{"$ref": "#/components/schemas/" + name}
			}
			property["items"] = raw
			r, err := d.Check(context.Background(), constraintObject([]any{}))
			if err != nil {
				t.Fatal(err)
			}
			listSetTestWant(t, r, links == 32, false)
		})
	}
	t.Run("oversized-item-schema-is-budgeted", func(t *testing.T) {
		property := listSetTestSchema()
		property["items"].(map[string]any)[strings.Repeat("x", MaxConstraintBytes/2+1)] = true
		c, r := listMapTestChecker(context.Background())
		c.checkListSet(listSetTestDocument(t, property), property, []any{}, "/value", true)
		listSetTestWant(t, *r, true, false)
		if !c.exhausted {
			t.Fatal("item metadata scan escaped byte accounting")
		}
	})
	for _, after := range []int{0, 10, 80, 400} {
		t.Run("cancel-after-"+strconv.Itoa(after), func(t *testing.T) {
			property := listSetTestSchema()
			d := listSetTestDocument(t, property)
			values := make([]any, MaxListSetItems)
			for i := range values {
				values[i] = "same"
			}
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &constraintCancelAfter{Context: base, cancel: cancel, after: after}
			if after == 0 {
				cancel()
			}
			r, err := d.Check(ctx, constraintObject(values))
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(r, Report{}) {
				t.Fatal("cancellation retained a speculative set report", err, ctx.calls)
			}
		})
	}
}

func TestSchemaListSetInspectionPreservesInputsAndPrivacy(t *testing.T) {
	property := listSetTestSchema()
	property["description"] = "PRIVATE_SCHEMA_DESCRIPTION"
	property["items"].(map[string]any)["default"] = "PRIVATE_DEFAULT"
	d, raw := constraintDocument(t, constraintEncode(t, property))
	beforeRaw := append([]byte{}, raw...)
	obj := constraintObject([]any{"PRIVATE_DRAFT_VALUE", "PRIVATE_DRAFT_VALUE", "中文船🚢", "é", "e\u0301", ""})
	beforeObject := constraintEncode(t, obj)
	beforeSchema := constraintEncode(t, d.root)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 30 {
				r, err := d.Check(context.Background(), obj)
				if err != nil {
					t.Error(err)
					return
				}
				listSetTestWant(t, r, false, true)
				for _, private := range []string{"PRIVATE_SCHEMA_DESCRIPTION", "PRIVATE_DEFAULT", "PRIVATE_DRAFT_VALUE", "中文船🚢"} {
					if strings.Contains(r.Text(), private) {
						t.Error("set report echoed private schema or draft values")
					}
				}
			}
		})
	}
	workers.Wait()
	if !bytes.Equal(raw, beforeRaw) || constraintEncode(t, obj) != beforeObject || constraintEncode(t, d.root) != beforeSchema {
		t.Fatal("read-only set inspection rewrote input or schema")
	}
}
