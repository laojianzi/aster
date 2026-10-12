package schemaassist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/laojianzi/aster/internal/manifest"
)

func enumProperty(values []any) map[string]any {
	// These unconstrained item schemas keep the separate structural inspector
	// complete for the small nested arrays used by the equality cases.
	return map[string]any{
		"enum":  values,
		"items": map[string]any{"items": map[string]any{"items": map[string]any{}}},
	}
}

func enumCheck(t testing.TB, values []any, value any) Report {
	t.Helper()
	return constraintCheck(t, constraintEncode(t, enumProperty(values)), value)
}

// enumHostDocument deliberately inserts values which cannot arrive through
// valid JSON, so the defensive in-memory boundary can be tested without a
// marshaler repairing invalid UTF-8 or refusing a cycle first.
func enumHostDocument(t testing.TB, property map[string]any) *Document {
	t.Helper()
	d, _ := constraintDocument(t, `{}`)
	d.root["properties"].(map[string]any)["value"] = property
	return d
}

func enumHostCheck(t testing.TB, property map[string]any, value any) Report {
	t.Helper()
	d := enumHostDocument(t, property)
	r, err := d.Check(context.Background(), constraintObject(value))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSchemaEnumStructuredEquality(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []any
		value  any
		match  bool
	}{
		{"object-key-order", []any{map[string]any{"a": int64(1), "b": "two"}}, map[string]any{"b": "two", "a": int64(1)}, true},
		{"nested-object-key-order", []any{map[string]any{"outer": map[string]any{"a": true, "b": nil}}}, map[string]any{"outer": map[string]any{"b": nil, "a": true}}, true},
		{"additional-key", []any{map[string]any{"a": int64(1)}}, map[string]any{"a": int64(1), "b": nil}, false},
		{"missing-key-is-not-null", []any{map[string]any{"a": nil}}, map[string]any{}, false},
		{"null-is-not-missing-key", []any{map[string]any{}}, map[string]any{"a": nil}, false},
		{"empty-object", []any{map[string]any{}}, map[string]any{}, true},
		{"empty-array", []any{[]any{}}, []any{}, true},
		{"empty-container-kinds-differ", []any{map[string]any{}}, []any{}, false},
		{"null-is-not-empty-object", []any{map[string]any{}}, nil, false},
		{"null-is-not-empty-array", []any{[]any{}}, nil, false},
		{"array-order", []any{[]any{"a", "b"}}, []any{"a", "b"}, true},
		{"reordered-array", []any{[]any{"a", "b"}}, []any{"b", "a"}, false},
		{"array-length", []any{[]any{"a", "b"}}, []any{"a"}, false},
		{"duplicate-elements-count", []any{[]any{"a", "a"}}, []any{"a"}, false},
		{"heterogeneous-array", []any{[]any{true, "1", nil, json.Number("1.0")}}, []any{true, "1", nil, int64(1)}, true},
		{"nested-arrays", []any{[]any{[]any{int64(1)}, []any{int64(2)}}}, []any{[]any{int64(1)}, []any{int64(2)}}, true},
		{"nested-array-order", []any{map[string]any{"items": []any{int64(1), int64(2)}}}, map[string]any{"items": []any{int64(2), int64(1)}}, false},
		{"objects-in-array", []any{[]any{map[string]any{"a": int64(1), "b": nil}}}, []any{map[string]any{"b": nil, "a": json.Number("1e0")}}, true},
		{"unicode-keys-and-values", []any{map[string]any{"船🚢": "中文\n🚢"}}, map[string]any{"船🚢": "中文\n🚢"}, true},
		{"escaped-key-is-exact", []any{map[string]any{"a/b~c": "ok"}}, map[string]any{"a/b~c": "ok"}, true},
		{"unicode-normalization-is-not-applied", []any{map[string]any{"text": "é"}}, map[string]any{"text": "e\u0301"}, false},
		{"numeric-string-is-not-number", []any{map[string]any{"n": int64(1)}}, map[string]any{"n": "1"}, false},
		{"boolean-is-not-number", []any{map[string]any{"n": int64(1)}}, map[string]any{"n": true}, false},
		{"nested-decimal-spellings", []any{map[string]any{"n": json.Number("1.2300")}}, map[string]any{"n": json.Number("123e-2")}, true},
		{"nested-shortest-float-decimal", []any{map[string]any{"n": json.Number("0.1")}}, map[string]any{"n": float64(0.1)}, true},
		{"nested-float32-decimal", []any{map[string]any{"n": json.Number("0.1")}}, map[string]any{"n": float32(0.1)}, true},
		{"nested-close-decimal-is-distinct", []any{map[string]any{"n": json.Number("0.100000000000000001")}}, map[string]any{"n": float64(0.1)}, false},
		{"nested-large-integer", []any{map[string]any{"n": json.Number("9007199254740993")}}, map[string]any{"n": int64(9007199254740993)}, true},
		{"nested-adjacent-large-integer", []any{map[string]any{"n": json.Number("9007199254740992")}}, map[string]any{"n": int64(9007199254740993)}, false},
		{"nested-max-int64", []any{[]any{json.Number("9223372036854775807")}}, []any{int64(math.MaxInt64)}, true},
		{"nested-min-int64", []any{[]any{json.Number("-9223372036854775808")}}, []any{int64(math.MinInt64)}, true},
		{"nested-max-uint64", []any{[]any{json.Number("18446744073709551615")}}, []any{uint64(math.MaxUint64)}, true},
		{"nested-negative-zero", []any{map[string]any{"n": json.Number("-0e-2147483648")}}, map[string]any{"n": math.Copysign(0, -1)}, true},
		{"nested-huge-positive-exponent", []any{map[string]any{"n": json.Number("1e2147483647")}}, map[string]any{"n": json.Number("10e2147483646")}, true},
		{"nested-huge-negative-exponent", []any{map[string]any{"n": json.Number("1e-2147483648")}}, map[string]any{"n": json.Number("1e-2147483647")}, false},
		{"mixed-alternatives-scalar-match", []any{map[string]any{"a": true}, "yes", []any{}}, "yes", true},
		{"mixed-alternatives-null-match", []any{map[string]any{}, nil, []any{}}, nil, true},
		{"later-structured-match", []any{[]any{"no"}, map[string]any{"a": true}}, map[string]any{"a": true}, true},
		{"scalar-alternatives-structured-instance", []any{"one", int64(1), nil}, map[string]any{"a": int64(1)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codes := []string{}
			if !tc.match {
				codes = append(codes, "enum")
			}
			r := enumCheck(t, tc.values, tc.value)
			constraintWant(t, r, false, codes...)
			for _, diagnostic := range r.Diagnostics {
				if diagnostic.Pointer != "/value" || diagnostic.Message != "Value is outside the advertised choices." {
					t.Fatal("structured enum lost its containing field or fixed message")
				}
			}
		})
	}
	t.Run("actual-manifest-decoder-preserves-nested-numbers", func(t *testing.T) {
		decoded, err := manifest.Decode([]byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: enum-numbers\nvalue:\n  large: 9007199254740993\n  decimal: 0.1\n  items: [1, 2]\n"))
		if err != nil {
			t.Fatal(err)
		}
		value := decoded.Object["value"].(map[string]any)
		if value["large"] != int64(9007199254740993) || value["decimal"] != float64(0.1) {
			t.Fatal("actual decoder did not preserve the expected integer/decimal values")
		}
		candidate := map[string]any{"large": json.Number("9007199254740993"), "decimal": json.Number("0.100"), "items": []any{json.Number("1.0"), int64(2)}}
		constraintWant(t, enumCheck(t, []any{candidate}, value), false)
		candidate["large"] = json.Number("9007199254740992")
		constraintWant(t, enumCheck(t, []any{candidate}, value), false, "enum")
	})
}

// enumOracleKey is a test-only, sorted canonical expression with exact rational
// numbers. It deliberately uses a different representation from production's
// map trees and bounded decimal coefficients. Its small fixed corpus never
// contains the huge exponents used by the separate production budget cases.
func enumOracleKey(t testing.TB, value any) string {
	t.Helper()
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool:" + strconv.FormatBool(v)
	case string:
		return "string:" + strconv.Quote(v)
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("object{")
		for _, key := range keys {
			b.WriteString(strconv.Quote(key))
			b.WriteByte(':')
			b.WriteString(enumOracleKey(t, v[key]))
			b.WriteByte(';')
		}
		b.WriteByte('}')
		return b.String()
	case []any:
		var b strings.Builder
		b.WriteString("array[")
		for _, child := range v {
			b.WriteString(enumOracleKey(t, child))
			b.WriteByte(';')
		}
		b.WriteByte(']')
		return b.String()
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		rational, ok := new(big.Rat).SetString(string(encoded))
		if !ok {
			t.Fatal("invalid numeric oracle input")
		}
		return "number:" + rational.RatString()
	}
}

func TestSchemaEnumMatchesIndependentJSONOracle(t *testing.T) {
	values := []any{
		nil, false, true, "", "1", "true", "é", "e\u0301", "船🚢",
		int64(0), math.Copysign(0, -1), json.Number("-0.000e12"),
		int64(1), json.Number("1.000"), json.Number("100e-2"),
		float64(0.1), json.Number("0.100"), json.Number("0.100000000000000001"),
		int64(9007199254740993), json.Number("9007199254740993"), int64(9007199254740992),
		uint64(math.MaxUint64), json.Number("18446744073709551615"),
		map[string]any{}, []any{}, map[string]any{"a": nil},
		[]any{int64(1), "two"}, []any{"two", int64(1)}, []any{json.Number("1.0"), "two"},
		map[string]any{"b": true, "a": []any{json.Number("1.0"), nil}},
		map[string]any{"a": []any{int64(1), nil}, "b": true},
		map[string]any{"a": []any{nil, int64(1)}, "b": true},
	}
	keys := make([]string, len(values))
	for i, value := range values {
		keys[i] = enumOracleKey(t, value)
	}
	comparisons := 0
	for i, candidate := range values {
		// Wrapping each value ensures the oracle exercises the new recursive
		// path, including numeric and mixed-type leaves, on every comparison.
		d, _ := constraintDocument(t, constraintEncode(t, enumProperty([]any{map[string]any{"entry": candidate}})))
		for j, value := range values {
			r, err := d.Check(context.Background(), constraintObject(map[string]any{"entry": value}))
			match := keys[i] == keys[j]
			if err != nil || r.Partial || (len(r.Diagnostics) == 0) != match ||
				(!match && (len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "enum" || r.Diagnostics[0].Pointer != "/value")) {
				t.Fatalf("oracle disagreement at candidate %d, instance %d: %+v %v", i, j, r, err)
			}
			comparisons++
		}
	}
	t.Logf("independent JSON equality comparisons: %d", comparisons)
}

func TestSchemaEnumRejectsMalformedAndInapplicable(t *testing.T) {
	invalidText := string([]byte{0xff, 'x'})
	badNumber := map[string]any{"PRIVATE_SCHEMA_KEY": json.Number("1e2147483648")}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	huge := make([]any, MaxEnumValueNodes*4)
	for _, tc := range []struct {
		name     string
		property map[string]any
		value    any
		partial  bool
		codes    []string
	}{
		{"late-invalid-after-scalar-match", enumProperty([]any{"allowed", badNumber}), "allowed", true, nil},
		{"late-invalid-after-object-match", enumProperty([]any{map[string]any{}, badNumber}), map[string]any{}, true, nil},
		{"late-invalid-after-mismatch", enumProperty([]any{map[string]any{"a": true}, badNumber}), map[string]any{}, true, nil},
		{"nested-number-syntax", enumProperty([]any{map[string]any{"n": json.Number("01")}}), map[string]any{}, true, nil},
		{"nested-number-empty", enumProperty([]any{[]any{json.Number("")}}), []any{}, true, nil},
		{"nested-number-whitespace", enumProperty([]any{map[string]any{"n": json.Number(" 1")}}), map[string]any{}, true, nil},
		{"nested-number-exponent-underflow", enumProperty([]any{map[string]any{"n": json.Number("1e-2147483649")}}), map[string]any{}, true, nil},
		{"nested-nan-metadata", enumProperty([]any{[]any{math.NaN()}}), []any{}, true, nil},
		{"nested-infinite-metadata", enumProperty([]any{[]any{math.Inf(1)}}), []any{}, true, nil},
		{"nested-invalid-utf8-key", enumProperty([]any{map[string]any{invalidText: true}}), map[string]any{}, true, nil},
		{"nested-invalid-utf8-string", enumProperty([]any{map[string]any{"text": invalidText}}), map[string]any{}, true, nil},
		{"late-invalid-utf8-after-match", enumProperty([]any{map[string]any{}, map[string]any{"text": invalidText}}), map[string]any{}, true, nil},
		{"non-json-metadata-map", enumProperty([]any{map[string]int{"x": 1}, map[string]any{}}), map[string]any{}, true, nil},
		{"non-json-metadata-slice", enumProperty([]any{[]string{"PRIVATE_SCHEMA_VALUE"}, []any{}}), []any{}, true, nil},
		{"non-json-metadata-pointer", enumProperty([]any{map[string]any{}, new(int)}), map[string]any{}, true, nil},
		{"typed-nil-metadata-object", enumProperty([]any{map[string]any(nil)}), map[string]any{}, true, nil},
		{"typed-nil-metadata-array", enumProperty([]any{[]any(nil)}), []any{}, true, nil},
		{"nested-typed-nil-metadata", enumProperty([]any{map[string]any{"items": []any(nil)}}), map[string]any{}, true, nil},
		{"typed-nil-instance-object", enumProperty([]any{map[string]any{}}), map[string]any(nil), true, nil},
		{"typed-nil-instance-array", enumProperty([]any{[]any{}}), []any(nil), true, nil},
		{"non-json-instance-map", enumProperty([]any{map[string]any{}}), map[string]int{"x": 1}, true, nil},
		{"nested-invalid-instance-key", enumProperty([]any{map[string]any{}}), map[string]any{invalidText: true}, true, nil},
		{"nested-invalid-instance-string", enumProperty([]any{map[string]any{}}), map[string]any{"text": invalidText}, true, nil},
		{"nested-invalid-instance-number", enumProperty([]any{map[string]any{}}), map[string]any{"n": json.Number("1e2147483648")}, true, nil},
		{"nested-infinite-instance", enumProperty([]any{map[string]any{}}), map[string]any{"n": math.Inf(-1)}, true, nil},
		{"valid-metadata-wrong-type", map[string]any{"type": "string", "enum": []any{map[string]any{}}}, int64(1), false, []string{"type"}},
		{"malformed-metadata-wrong-type", map[string]any{"type": "string", "enum": []any{"allowed", badNumber}}, int64(1), true, []string{"type"}},
		{"wrong-type-huge-instance-not-scanned", map[string]any{"type": "string", "enum": []any{map[string]any{}}}, huge, false, []string{"type"}},
		{"wrong-type-cycle-not-scanned", map[string]any{"type": "string", "enum": []any{map[string]any{}}}, cyclic, false, []string{"type"}},
		{"wrong-type-invalid-utf8-not-scanned", map[string]any{"type": "object", "enum": []any{map[string]any{}}}, invalidText, false, []string{"type"}},
		{"scalar-path-wrong-type-huge-instance-not-scanned", map[string]any{"type": "string", "enum": []any{"allowed"}}, huge, false, []string{"type"}},
		{"nullable-null-matches-mixed-enum", map[string]any{"type": "object", "nullable": true, "enum": []any{map[string]any{}, nil}}, nil, false, nil},
		{"nullable-null-excluded-by-enum", map[string]any{"type": "object", "nullable": true, "enum": []any{map[string]any{}}}, nil, false, []string{"enum"}},
		{"nullable-null-invalid-metadata", map[string]any{"type": "object", "nullable": true, "enum": []any{nil, badNumber}}, nil, true, nil},
		{"nonnullable-null-retains-type-first", map[string]any{"type": "object", "enum": []any{nil, map[string]any{}}}, nil, false, []string{"type"}},
		{"canonical-union-with-structured-alternative", map[string]any{"x-kubernetes-int-or-string": true, "anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}}, "enum": []any{map[string]any{}, int64(1)}}, int64(1), false, nil},
		{"unsupported-keyword-keeps-known-enum-hint", map[string]any{"enum": []any{map[string]any{"a": true}}, "format": "PRIVATE_SCHEMA_FORMAT"}, map[string]any{}, true, []string{"enum"}},
		{"malformed-enum-keeps-supported-size-hint", map[string]any{"enum": []any{badNumber}, "minProperties": int64(2)}, map[string]any{}, true, []string{"minProperties"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := enumHostCheck(t, tc.property, tc.value)
			constraintWant(t, r, tc.partial, tc.codes...)
			if strings.Contains(r.Text()+constraintEncode(t, r), "PRIVATE_") {
				t.Fatal("enum inspection disclosed a private metadata key or value")
			}
		})
	}
	t.Run("existing-reference-target-can-contain-structured-enum", func(t *testing.T) {
		d, _ := constraintDocument(t, `{"$ref":"#/components/schemas/Shared"}`)
		d.definitions["Shared"] = enumProperty([]any{map[string]any{"a": true}})
		r, err := d.Check(context.Background(), constraintObject(map[string]any{}))
		if err != nil {
			t.Fatal(err)
		}
		constraintWant(t, r, false, "enum")
	})
	t.Run("enum-bearing-reference-wrapper-stays-partial", func(t *testing.T) {
		d := enumHostDocument(t, map[string]any{
			"allOf": []any{map[string]any{"$ref": "#/components/schemas/Shared"}},
			"enum":  []any{map[string]any{}},
		})
		d.definitions["Shared"] = map[string]any{"type": "object", "required": []any{"required-by-uninspected-composition"}}
		r, err := d.Check(context.Background(), constraintObject(map[string]any{}))
		if err != nil {
			t.Fatal(err)
		}
		constraintWant(t, r, true)
		help, err := d.Help(context.Background(), "/value")
		if err != nil || !help.Partial {
			t.Fatal("structured enum silently expanded the allOf wrapper contract", err)
		}
	})
}

func enumDepthValue(depth int) any {
	var value any = int64(1)
	for i := 0; i < depth; i++ {
		value = map[string]any{"child": value}
	}
	return value
}

func enumWideValue(width int) map[string]any {
	value := make(map[string]any, width)
	for i := 0; i < width; i++ {
		value["key"+strconv.Itoa(i)] = int64(0)
	}
	return value
}

func enumDirectChecker(ctx context.Context) (*constraintChecker, *Report) {
	r := &Report{}
	c := &constraintChecker{ctx: ctx, report: r, add: func(path, code, message string) {
		r.Diagnostics = append(r.Diagnostics, Diagnostic{path, code, message})
	}}
	return c, r
}

func TestSchemaEnumBudgetsAndCancellation(t *testing.T) {
	textLimit := strings.Repeat("x", MaxEnumTextBytes)
	textOver := textLimit + "x"
	numberLimit := json.Number(strings.Repeat("9", MaxNumberBytes))
	numberOver := json.Number(strings.Repeat("9", MaxNumberBytes+1))
	arrayLimit := make([]any, MaxEnumValueNodes-1)
	arrayOver := make([]any, MaxEnumValueNodes)
	for _, tc := range []struct {
		name      string
		candidate any
		value     any
		partial   bool
	}{
		{"depth-limit-root-zero", enumDepthValue(MaxEnumValueDepth), enumDepthValue(MaxEnumValueDepth), false},
		{"metadata-depth-limit-plus-one", enumDepthValue(MaxEnumValueDepth + 1), map[string]any{}, true},
		{"instance-depth-limit-plus-one", map[string]any{}, enumDepthValue(MaxEnumValueDepth + 1), true},
		{"array-node-limit-includes-root", arrayLimit, arrayLimit, false},
		{"metadata-array-node-limit-plus-one", arrayOver, []any{}, true},
		{"instance-array-node-limit-plus-one", []any{}, arrayOver, true},
		{"object-node-limit-includes-root", enumWideValue(MaxEnumValueNodes - 1), enumWideValue(MaxEnumValueNodes - 1), false},
		{"metadata-object-node-limit-plus-one", enumWideValue(MaxEnumValueNodes), map[string]any{}, true},
		{"instance-object-node-limit-plus-one", map[string]any{}, enumWideValue(MaxEnumValueNodes), true},
		{"string-byte-limit", map[string]any{"s": textLimit}, map[string]any{"s": textLimit}, false},
		{"metadata-string-byte-limit-plus-one", map[string]any{"s": textOver}, map[string]any{}, true},
		{"instance-string-byte-limit-plus-one", map[string]any{}, map[string]any{"s": textOver}, true},
		{"key-byte-limit", map[string]any{textLimit: nil}, map[string]any{textLimit: nil}, false},
		{"metadata-key-byte-limit-plus-one", map[string]any{textOver: nil}, map[string]any{}, true},
		{"instance-key-byte-limit-plus-one", map[string]any{}, map[string]any{textOver: nil}, true},
		{"multibyte-string-budget-is-bytes", map[string]any{"s": strings.Repeat("船", MaxEnumTextBytes/3+1)}, map[string]any{}, true},
		{"multibyte-key-budget-is-bytes", map[string]any{strings.Repeat("船", MaxEnumTextBytes/3+1): nil}, map[string]any{}, true},
		{"nested-number-byte-limit", map[string]any{"n": numberLimit}, map[string]any{"n": numberLimit}, false},
		{"metadata-nested-number-byte-limit-plus-one", map[string]any{"n": numberOver}, map[string]any{}, true},
		{"instance-nested-number-byte-limit-plus-one", map[string]any{}, map[string]any{"n": numberOver}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraintWant(t, enumCheck(t, []any{tc.candidate}, tc.value), tc.partial)
		})
	}
	t.Run("structured-alternative-count-limit", func(t *testing.T) {
		values := make([]any, MaxEnumValues)
		for i := range values {
			values[i] = map[string]any{}
		}
		constraintWant(t, enumCheck(t, values, map[string]any{}), false)
		constraintWant(t, enumCheck(t, append(values, map[string]any{}), map[string]any{}), true)
	})
	t.Run("pure-scalar-size-and-accounting-unchanged", func(t *testing.T) {
		large := strings.Repeat("x", MaxEnumTextBytes*4)
		c, r := enumDirectChecker(context.Background())
		c.checkEnum(map[string]any{"enum": []any{large}}, large, "/value", true)
		constraintWant(t, *r, false)
		if r.Checked != 2 || c.bytes != 2*len(large) {
			t.Fatal("structured limits changed the original scalar enum allowance")
		}
		values := make([]any, MaxEnumValues)
		for i := range values {
			values[i] = int64(i)
		}
		c, r = enumDirectChecker(context.Background())
		c.checkEnum(map[string]any{"enum": values}, int64(127), "/value", true)
		constraintWant(t, *r, false)
		if r.Checked != 129 || c.bytes != 129*32 {
			t.Fatal("pure scalar enumeration no longer has its established accounting")
		}
	})
	for _, tc := range []struct {
		name string
		late any
	}{
		{"late-depth-after-match", enumDepthValue(MaxEnumValueDepth + 1)},
		{"late-node-budget-after-match", arrayOver},
		{"late-string-budget-after-match", map[string]any{"s": textOver}},
		{"late-key-budget-after-match", map[string]any{textOver: nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraintWant(t, enumCheck(t, []any{map[string]any{}, tc.late}, map[string]any{}), true)
			property := map[string]any{"type": "string", "enum": []any{"allowed", tc.late}}
			constraintWant(t, enumHostCheck(t, property, int64(1)), true, "type")
		})
	}
	t.Run("late-shared-work-exhaustion-after-match", func(t *testing.T) {
		values := []any{map[string]any{}}
		for i := 1; i < MaxEnumValues; i++ {
			values = append(values, enumWideValue(200))
		}
		r := enumCheck(t, values, map[string]any{})
		constraintWant(t, r, true)
		if r.Checked > MaxWork+1 {
			t.Fatal("enum alternatives escaped the common work allowance")
		}
	})
	t.Run("work-is-shared-across-independent-fields", func(t *testing.T) {
		properties, input := map[string]any{}, map[string]any{}
		for i := 0; i < 40; i++ {
			key := "field" + strconv.Itoa(i)
			value := enumWideValue(64)
			properties[key], input[key] = enumProperty([]any{value}), value
		}
		property := map[string]any{"type": "object", "properties": properties}
		r := constraintCheck(t, constraintEncode(t, property), input)
		constraintWant(t, r, true)
		if r.Checked > MaxWork+1 {
			t.Fatal("independent enums did not share the inspection allowance")
		}
	})
	t.Run("bytes-are-shared-across-independent-fields", func(t *testing.T) {
		properties, input := map[string]any{}, map[string]any{}
		for i := 0; i < 260; i++ {
			key := "field" + strconv.Itoa(i)
			value := map[string]any{"s": strings.Repeat("x", MaxEnumTextBytes/2)}
			properties[key], input[key] = enumProperty([]any{value}), value
		}
		property := map[string]any{"type": "object", "properties": properties}
		r := constraintCheck(t, constraintEncode(t, property), input)
		constraintWant(t, r, true)
		if r.Checked > MaxWork/2 {
			t.Fatal("byte test consumed its work allowance before testing shared bytes")
		}
	})
	for _, prior := range []string{"work", "bytes", "comparison-bytes", "other-constraint"} {
		t.Run("prior-"+prior+"-exhaustion-does-not-invent-hint", func(t *testing.T) {
			c, r := enumDirectChecker(context.Background())
			switch prior {
			case "work":
				r.Checked = MaxWork
			case "bytes":
				c.bytes = MaxConstraintBytes
			case "comparison-bytes":
				// Both tiny trees fit in the remaining normalization bytes;
				// the complete comparison must also be paid before lookup.
				c.bytes = MaxConstraintBytes - 7
			case "other-constraint":
				if !c.chargeWork(MaxWork-4, 0) {
					t.Fatal("could not reserve prior constraint work")
				}
			}
			c.checkEnum(enumProperty([]any{map[string]any{"x": "a"}}), map[string]any{"x": "b"}, "/value", true)
			constraintWant(t, *r, true)
		})
	}
	t.Run("late-shared-byte-exhaustion-after-match", func(t *testing.T) {
		c, r := enumDirectChecker(context.Background())
		c.bytes = MaxConstraintBytes - 32
		c.checkEnum(enumProperty([]any{"allowed", map[string]any{"s": strings.Repeat("x", 64)}}), "allowed", "/value", true)
		constraintWant(t, *r, true)
	})
	t.Run("huge-width-rejected-before-traversal", func(t *testing.T) {
		value := make([]any, 100000)
		r := enumHostCheck(t, enumProperty([]any{[]any{}}), value)
		constraintWant(t, r, true)
		if r.Checked > 100 {
			t.Fatal("an oversized container was traversed before its width guard")
		}
	})
	cyclicObject := map[string]any{}
	cyclicObject["self"] = cyclicObject
	cyclicArray := make([]any, 1)
	cyclicArray[0] = cyclicArray
	for _, tc := range []struct {
		name      string
		candidate any
		value     any
	}{
		{"cyclic-metadata-object", cyclicObject, map[string]any{}},
		{"cyclic-metadata-array", cyclicArray, []any{}},
		{"cyclic-instance-object", map[string]any{}, cyclicObject},
		{"cyclic-instance-array", []any{}, cyclicArray},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraintWant(t, enumHostCheck(t, enumProperty([]any{tc.candidate}), tc.value), true)
		})
	}
	t.Run("shared-subtrees-count-on-each-occurrence", func(t *testing.T) {
		shared := map[string]any{"leaf": nil}
		values := make([]any, 511) // One array node + 511 * two value nodes.
		for i := range values {
			values[i] = shared
		}
		constraintWant(t, enumCheck(t, []any{values}, values), false)
		values = append(values, shared)
		constraintWant(t, enumCheck(t, []any{values}, values), true)
	})
	t.Run("cancel-before-inspection", func(t *testing.T) {
		d, _ := constraintDocument(t, constraintEncode(t, enumProperty([]any{map[string]any{}})))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r, err := d.Check(ctx, constraintObject(map[string]any{}))
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(r, Report{}) {
			t.Fatal("canceled enum check retained a report", err)
		}
	})
	for _, tc := range []struct {
		name      string
		candidate any
		value     any
		after     int
	}{
		{"cancel-during-metadata", enumWideValue(200), map[string]any{}, 20},
		{"cancel-during-instance", map[string]any{}, enumWideValue(200), 20},
		{"cancel-during-nested-comparison", enumDepthValue(32), enumDepthValue(32), 150},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := constraintDocument(t, constraintEncode(t, enumProperty([]any{tc.candidate})))
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &constraintCancelAfter{Context: base, cancel: cancel, after: tc.after}
			r, err := d.Check(ctx, constraintObject(tc.value))
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(r, Report{}) {
				t.Fatal("in-flight enum cancellation retained a report", err)
			}
		})
	}
}

func TestSchemaEnumInspectionPreservesInputsAndPrivacy(t *testing.T) {
	property := map[string]any{"type": "object", "properties": map[string]any{
		"object": map[string]any{
			"type": "object", "enum": []any{map[string]any{"PRIVATE_KEY": "PRIVATE_SCHEMA_VALUE", "number": json.Number("9007199254740993")}},
			"default": map[string]any{"PRIVATE_DEFAULT": true}, "example": "PRIVATE_EXAMPLE",
		},
		"array":    map[string]any{"type": "array", "items": map[string]any{}, "enum": []any{[]any{"PRIVATE_SCHEMA_ARRAY", nil}}},
		"nullable": map[string]any{"type": "object", "nullable": true, "enum": []any{nil, map[string]any{}}},
		"omitted":  map[string]any{"type": "object", "enum": []any{map[string]any{}}, "default": map[string]any{"PRIVATE_INSERTION": true}},
	}}
	d, raw := constraintDocument(t, constraintEncode(t, property))
	input := constraintObject(map[string]any{
		"object":   map[string]any{"PRIVATE_KEY": "PRIVATE_DRAFT_VALUE", "number": int64(9007199254740993)},
		"array":    []any{"PRIVATE_DRAFT_ARRAY", nil},
		"nullable": nil,
	})
	beforeInput, beforeDocument := constraintEncode(t, input), constraintEncode(t, d.root)
	beforeRaw := bytes.Clone(raw)
	helpBefore, err := d.Help(context.Background(), "/value/object")
	if err != nil {
		t.Fatal(err)
	}
	want, err := d.Check(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	constraintWant(t, want, false, "enum", "enum")
	for _, diagnostic := range want.Diagnostics {
		if diagnostic.Pointer != "/value/array" && diagnostic.Pointer != "/value/object" {
			t.Fatal("enum exposed a member pointer instead of its containing draft field")
		}
	}
	if strings.Contains(want.Text()+constraintEncode(t, want)+helpBefore.Text(), "PRIVATE_") {
		t.Fatal("enum or field-help output disclosed private source or metadata")
	}
	good := constraintObject(map[string]any{
		"object":   map[string]any{"PRIVATE_KEY": "PRIVATE_SCHEMA_VALUE", "number": int64(9007199254740993)},
		"array":    []any{"PRIVATE_SCHEMA_ARRAY", nil},
		"nullable": map[string]any{},
	})
	accepted, err := d.Check(context.Background(), good)
	if err != nil {
		t.Fatal(err)
	}
	constraintWant(t, accepted, false)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				r, e := d.Check(context.Background(), input)
				if e != nil || !reflect.DeepEqual(r, want) {
					t.Error("read-only enum inspection changed its report", e)
				}
			}
		}()
	}
	wg.Wait()
	helpAfter, err := d.Help(context.Background(), "/value/object")
	if err != nil || !reflect.DeepEqual(helpBefore, helpAfter) {
		t.Fatal("enum inspection changed shared field-help state", err)
	}
	if beforeInput != constraintEncode(t, input) || beforeDocument != constraintEncode(t, d.root) || !bytes.Equal(beforeRaw, raw) {
		t.Fatal("enum inspection changed source, draft, shared schema, or defaults")
	}
	t.Run("unordered-mismatch-has-fixed-comparison-cost", func(t *testing.T) {
		candidate, matching := enumWideValue(32), enumWideValue(32)
		candidate["key17"] = int64(1)
		property := enumProperty([]any{candidate, matching})
		baseline, baselineReport := enumDirectChecker(context.Background())
		baseline.checkEnum(property, matching, "/value", true)
		constraintWant(t, *baselineReport, false)
		// Repeat at the exact remaining allowance. A random early mismatch
		// must not alter later budget decisions or resurrect a stale result.
		for i := 0; i < 64; i++ {
			c, r := enumDirectChecker(context.Background())
			r.Checked = MaxWork - baselineReport.Checked
			c.checkEnum(property, enumWideValue(32), "/value", true)
			constraintWant(t, *r, false)
			if r.Checked != MaxWork || c.bytes != baseline.bytes {
				t.Fatal("object iteration order changed comparison cost")
			}
		}
	})
}
