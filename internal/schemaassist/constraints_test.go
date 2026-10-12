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
	"testing"
)

func constraintDocument(t testing.TB, property string) (*Document, []byte) {
	t.Helper()
	raw := []byte("{\"openapi\":\"3.0.3\",\"components\":{\"schemas\":{\"Case\":{\"type\":\"object\",\"x-kubernetes-group-version-kind\":[{\"group\":\"\",\"version\":\"v1\",\"kind\":\"Pod\"}],\"properties\":{\"apiVersion\":{\"type\":\"string\"},\"kind\":{\"type\":\"string\"},\"value\":" + property + "}}}}}")
	d, err := Decode(context.Background(), raw, podGVK)
	if err != nil {
		t.Fatalf("decode constraint fixture: %v", err)
	}
	return d, raw
}

func constraintEncode(t testing.TB, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func constraintObject(value any) map[string]any {
	return map[string]any{"apiVersion": "v1", "kind": "Pod", "value": value}
}

func constraintCheck(t testing.TB, property string, value any) Report {
	t.Helper()
	d, _ := constraintDocument(t, property)
	r, err := d.Check(context.Background(), constraintObject(value))
	if err != nil {
		t.Fatalf("check constraint fixture: %v", err)
	}
	return r
}

func constraintWant(t testing.TB, r Report, partial bool, codes ...string) {
	t.Helper()
	if r.Partial != partial {
		t.Errorf("partial = %v, want %v; report %+v", r.Partial, partial, r)
	}
	got := make([]string, 0, len(r.Diagnostics))
	for _, diagnostic := range r.Diagnostics {
		got = append(got, diagnostic.Code)
	}
	sort.Strings(got)
	want := append([]string{}, codes...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("codes = %v, want %v; report %+v", got, want, r)
	}
	if len(r.Text()) > 64<<10 {
		t.Fatal("constraint report escaped the output bound")
	}
}

func TestSchemaScalarEnumExactEqualityAndNullable(t *testing.T) {
	for _, tc := range []struct {
		name, property string
		value          any
		codes          []string
	}{
		{"string-match", "{\"type\":\"string\",\"enum\":[\"a\",\"b\"]}", "b", nil},
		{"string-case-sensitive", "{\"type\":\"string\",\"enum\":[\"A\"]}", "a", []string{"enum"}},
		{"boolean-not-string", "{\"enum\":[true]}", "true", []string{"enum"}},
		{"boolean-match", "{\"type\":\"boolean\",\"enum\":[false,true]}", false, nil},
		{"integer-spelling", "{\"type\":\"number\",\"enum\":[1.0]}", json.Number("10e-1"), nil},
		{"negative-zero", "{\"type\":\"number\",\"enum\":[0]}", json.Number("-0e-2147483648"), nil},
		{"float-negative-zero", "{\"type\":\"number\",\"enum\":[0]}", math.Copysign(0, -1), nil},
		{"float-shortest-decimal", "{\"type\":\"number\",\"enum\":[0.1]}", float64(0.1), nil},
		{"large-integer-match", "{\"type\":\"integer\",\"enum\":[9007199254740993]}", int64(9007199254740993), nil},
		{"large-integer-distinct", "{\"type\":\"integer\",\"enum\":[9007199254740992]}", int64(9007199254740993), []string{"enum"}},
		{"max-int64", "{\"type\":\"integer\",\"enum\":[9223372036854775807]}", int64(math.MaxInt64), nil},
		{"max-uint64", "{\"type\":\"number\",\"enum\":[18446744073709551615]}", uint64(math.MaxUint64), nil},
		{"numeric-not-string", "{\"enum\":[1]}", "1", []string{"enum"}},
		{"nullable-enum-contains-null", "{\"type\":\"string\",\"nullable\":true,\"enum\":[null,\"yes\"]}", nil, nil},
		{"nullable-enum-excludes-null", "{\"type\":\"string\",\"nullable\":true,\"enum\":[\"yes\"]}", nil, []string{"enum"}},
		{"untyped-null-enum", "{\"enum\":[null]}", nil, nil},
		{"nonnullable-type-first", "{\"type\":\"string\",\"enum\":[null]}", nil, []string{"type"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := constraintCheck(t, tc.property, tc.value)
			constraintWant(t, r, false, tc.codes...)
			for _, d := range r.Diagnostics {
				if d.Pointer != "/value" {
					t.Errorf("unexpected diagnostic location %q", d.Pointer)
				}
			}
		})
	}
}

func TestSchemaNumericBoundsUseExactDecodedValues(t *testing.T) {
	for _, tc := range []struct {
		name, property string
		value          any
		codes          []string
	}{
		{"inclusive-minimum", "{\"type\":\"number\",\"minimum\":2}", int64(2), nil},
		{"below-minimum", "{\"type\":\"number\",\"minimum\":2}", json.Number("1.999999999999999999"), []string{"minimum"}},
		{"exclusive-minimum-equality", "{\"type\":\"number\",\"minimum\":2,\"exclusiveMinimum\":true}", int64(2), []string{"minimum"}},
		{"explicit-inclusive-minimum", "{\"type\":\"number\",\"minimum\":2,\"exclusiveMinimum\":false}", int64(2), nil},
		{"above-exclusive-minimum", "{\"type\":\"number\",\"minimum\":2,\"exclusiveMinimum\":true}", json.Number("2.000000000000000001"), nil},
		{"inclusive-maximum", "{\"type\":\"number\",\"maximum\":2}", json.Number("2e0"), nil},
		{"above-maximum", "{\"type\":\"number\",\"maximum\":2}", json.Number("2.000000000000000001"), []string{"maximum"}},
		{"exclusive-maximum-equality", "{\"type\":\"number\",\"maximum\":2,\"exclusiveMaximum\":true}", int64(2), []string{"maximum"}},
		{"explicit-inclusive-maximum", "{\"type\":\"number\",\"maximum\":2,\"exclusiveMaximum\":false}", int64(2), nil},
		{"negative-order", "{\"type\":\"number\",\"minimum\":-1.1}", json.Number("-1.100000000000000001"), []string{"minimum"}},
		{"negative-zero-equal", "{\"type\":\"number\",\"minimum\":0,\"maximum\":0}", json.Number("-0.000e+2000000000"), nil},
		{"large-integer-boundary", "{\"type\":\"integer\",\"minimum\":9007199254740993,\"maximum\":9007199254740993}", int64(9007199254740993), nil},
		{"large-integer-under-boundary", "{\"type\":\"integer\",\"minimum\":9007199254740993}", int64(9007199254740992), []string{"minimum"}},
		{"large-integer-over-boundary", "{\"type\":\"integer\",\"maximum\":9007199254740992}", int64(9007199254740993), []string{"maximum"}},
		{"max-int64-next-integer", "{\"type\":\"number\",\"minimum\":9223372036854775808}", int64(math.MaxInt64), []string{"minimum"}},
		{"min-int64-previous-integer", "{\"type\":\"number\",\"maximum\":-9223372036854775809}", int64(math.MinInt64), []string{"maximum"}},
		{"decimal-exponent-equivalence", "{\"type\":\"number\",\"minimum\":123e-2,\"maximum\":1.2300}", json.Number("0.123e1"), nil},
		{"float-uses-json-decimal", "{\"type\":\"number\",\"minimum\":0.1,\"maximum\":0.1}", float64(0.1), nil},
		{"float-does-not-become-adjacent-decimal", "{\"type\":\"number\",\"maximum\":0.100000000000000001}", float64(0.1), nil},
		{"huge-positive-exponent", "{\"type\":\"number\",\"minimum\":1e2147483647}", json.Number("9e2147483646"), []string{"minimum"}},
		{"huge-negative-exponent", "{\"type\":\"number\",\"maximum\":1e-2147483648}", json.Number("1e-2147483647"), []string{"maximum"}},
		{"huge-negative-value", "{\"type\":\"number\",\"minimum\":-1e2147483647}", json.Number("-9e2147483646"), nil},
		{"coefficient-normalization", "{\"type\":\"number\",\"minimum\":100000e-5,\"maximum\":0.1e1}", json.Number("1.00000"), nil},
		{"empty-intersection-is-two-known-violations", "{\"type\":\"number\",\"minimum\":5,\"maximum\":1}", int64(3), []string{"minimum", "maximum"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraintWant(t, constraintCheck(t, tc.property, tc.value), false, tc.codes...)
		})
	}
	t.Run("512-byte-number-boundary", func(t *testing.T) {
		value := "1" + strings.Repeat("0", 511)
		constraintWant(t, constraintCheck(t, "{\"type\":\"number\",\"minimum\":"+value+",\"maximum\":"+value+"}", json.Number(value)), false)
	})
	t.Run("bounded-decimal-matrix-matches-rational-order", func(t *testing.T) {
		// A separate exact arithmetic oracle covers coefficient/power order and
		// equivalent spellings without duplicating the checker's normalization.
		values := []string{
			"-9e90", "-1e90", "-100.1", "-10", "-1.01", "-1", "-0.1", "-1e-90",
			"-0", "0", "0.000e10", "1e-90", "0.01", "0.1", "1", "1.000", "100e-2",
			"1.01", "10", "10.01", "100.1", "9007199254740992", "9007199254740993", "1e90", "9e90",
		}
		for _, limit := range values {
			bound, ok := new(big.Rat).SetString(limit)
			if !ok {
				t.Fatalf("invalid oracle bound %q", limit)
			}
			d, _ := constraintDocument(t, "{\"type\":\"number\",\"minimum\":"+limit+",\"maximum\":"+limit+"}")
			for _, value := range values {
				instance, ok := new(big.Rat).SetString(value)
				if !ok {
					t.Fatalf("invalid oracle value %q", value)
				}
				codes := []string{}
				switch instance.Cmp(bound) {
				case -1:
					codes = append(codes, "minimum")
				case 1:
					codes = append(codes, "maximum")
				}
				r, err := d.Check(context.Background(), constraintObject(json.Number(value)))
				if err != nil {
					t.Fatal(err)
				}
				if r.Partial || len(r.Diagnostics) != len(codes) || (len(codes) == 1 && r.Diagnostics[0].Code != codes[0]) {
					t.Fatalf("value %q vs bound %q disagreed with exact rational comparison: %+v, want %v", value, limit, r, codes)
				}
			}
		}
	})
}

func TestSchemaConstraintLengthsCountUnicodeAndContainers(t *testing.T) {
	for _, tc := range []struct {
		name, property string
		value          any
		codes          []string
	}{
		{"empty-minimum", "{\"type\":\"string\",\"minLength\":1}", "", []string{"minLength"}},
		{"unicode-two-codepoints", "{\"type\":\"string\",\"minLength\":2,\"maxLength\":2}", "中🚢", nil},
		{"astral-emoji-is-one", "{\"type\":\"string\",\"maxLength\":1}", "🚢", nil},
		{"combining-mark-is-another-codepoint", "{\"type\":\"string\",\"maxLength\":1}", "e\u0301", []string{"maxLength"}},
		{"zwj-family-is-seven", "{\"type\":\"string\",\"minLength\":7,\"maxLength\":7}", "👨‍👩‍👧‍👦", nil},
		{"crlf-is-two", "{\"type\":\"string\",\"minLength\":4,\"maxLength\":4}", "a\r\nb", nil},
		{"zero-length-maximum", "{\"type\":\"string\",\"maxLength\":0}", "x", []string{"maxLength"}},
		{"empty-array", "{\"type\":\"array\",\"minItems\":1,\"items\":{\"type\":\"integer\"}}", []any{}, []string{"minItems"}},
		{"array-boundaries", "{\"type\":\"array\",\"minItems\":2,\"maxItems\":2,\"items\":{\"type\":\"integer\"}}", []any{int64(1), int64(2)}, nil},
		{"array-over-maximum", "{\"type\":\"array\",\"maxItems\":1,\"items\":{\"type\":\"integer\"}}", []any{int64(1), int64(2)}, []string{"maxItems"}},
		{"empty-object", "{\"type\":\"object\",\"minProperties\":1,\"additionalProperties\":{\"type\":\"integer\"}}", map[string]any{}, []string{"minProperties"}},
		{"object-counts-keys", "{\"type\":\"object\",\"minProperties\":2,\"maxProperties\":2,\"additionalProperties\":{\"type\":\"integer\"}}", map[string]any{"中文": int64(1), "🚢": int64(2)}, nil},
		{"object-over-maximum", "{\"type\":\"object\",\"maxProperties\":1,\"additionalProperties\":{\"type\":\"integer\"}}", map[string]any{"a": int64(1), "b": int64(2)}, []string{"maxProperties"}},
		{"integer-valued-count-spelling", "{\"type\":\"string\",\"minLength\":2.0,\"maxLength\":2e0}", "ab", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraintWant(t, constraintCheck(t, tc.property, tc.value), false, tc.codes...)
		})
	}
}

func TestSchemaConstraintsRejectMalformedAndUnsupportedWithoutGuessing(t *testing.T) {
	for _, tc := range []struct {
		name, property string
		value          any
	}{
		{"enum-not-array", "{\"type\":\"string\",\"enum\":\"private-schema-value\"}", "private-draft-value"},
		{"enum-empty", "{\"type\":\"string\",\"enum\":[]}", "anything"},
		{"enum-complex-even-after-match", "{\"type\":\"string\",\"enum\":[\"match\",{\"hidden\":\"schema-secret\"}]}", "match"},
		{"enum-complex-no-match", "{\"type\":\"string\",\"enum\":[[\"schema-secret\"]]}", "draft-secret"},
		{"minimum-string", "{\"type\":\"number\",\"minimum\":\"schema-secret\"}", int64(1)},
		{"maximum-null", "{\"type\":\"number\",\"maximum\":null}", int64(1)},
		{"exclusive-minimum-31-number", "{\"type\":\"number\",\"minimum\":2,\"exclusiveMinimum\":2}", int64(2)},
		{"exclusive-maximum-string", "{\"type\":\"number\",\"maximum\":2,\"exclusiveMaximum\":\"true\"}", int64(2)},
		{"orphan-exclusive-minimum", "{\"type\":\"number\",\"exclusiveMinimum\":true}", int64(1)},
		{"orphan-exclusive-maximum", "{\"type\":\"number\",\"exclusiveMaximum\":true}", int64(1)},
		{"negative-length", "{\"type\":\"string\",\"minLength\":-1}", "x"},
		{"fractional-length", "{\"type\":\"string\",\"maxLength\":1.5}", "x"},
		{"boolean-item-count", "{\"type\":\"array\",\"minItems\":true,\"items\":{\"type\":\"integer\"}}", []any{int64(1)}},
		{"fractional-property-count", "{\"type\":\"object\",\"maxProperties\":1.5}", map[string]any{}},
		{"numeric-exponent-out-of-range", "{\"type\":\"number\",\"minimum\":1e2147483648}", int64(1)},
		{"numeric-coefficient-too-long", "{\"type\":\"number\",\"maximum\":" + strings.Repeat("9", 513) + "}", int64(1)},
		{"enum-invalid-bounded-number-after-match", "{\"type\":\"number\",\"enum\":[1,1e2147483648]}", int64(1)},
		{"unsupported-pattern", "{\"type\":\"string\",\"pattern\":\"schema-secret-pattern\"}", "draft-secret"},
		{"unsupported-format", "{\"type\":\"string\",\"format\":\"email\"}", "not-an-email"},
		{"unsupported-multiple-of", "{\"type\":\"number\",\"multipleOf\":2}", int64(3)},
		{"unsupported-unique-items", "{\"type\":\"array\",\"uniqueItems\":true,\"items\":{\"type\":\"integer\"}}", []any{int64(1), int64(1)}},
		{"unsupported-composite", "{\"oneOf\":[{\"type\":\"integer\"},{\"type\":\"string\"}]}", int64(1)},
		{"nullable-string-on-null", "{\"type\":\"string\",\"nullable\":\"true\",\"enum\":[\"schema-secret\"]}", nil},
		{"nullable-string-on-present-value", "{\"type\":\"string\",\"nullable\":\"false\"}", "draft-secret"},
		{"nullable-number", "{\"type\":\"integer\",\"nullable\":1}", int64(1)},
		{"nullable-null", "{\"type\":\"string\",\"nullable\":null}", "draft-secret"},
		{"type-array-from-31", "{\"type\":[\"string\",\"null\"]}", "draft-secret"},
		{"type-number", "{\"type\":1}", int64(1)},
		{"type-null", "{\"type\":null}", "draft-secret"},
		{"type-empty", "{\"type\":\"\"}", "draft-secret"},
		{"unknown-type-with-null", "{\"type\":\"schema-secret-type\"}", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := constraintCheck(t, tc.property, tc.value)
			if tc.name == "unsupported-pattern" {
				// Keep the original name/input; the literal pattern is now
				// supported and must establish this mismatch without leaks.
				constraintWant(t, r, false, "pattern")
			} else if tc.name == "unsupported-multiple-of" {
				// Keep the original regression identity and input. This keyword
				// now produces a known violation instead of an incomplete scan.
				constraintWant(t, r, false, "multipleOf")
			} else {
				constraintWant(t, r, true)
			}
			if strings.Contains(r.Text(), "schema-secret") || strings.Contains(r.Text(), "draft-secret") {
				t.Fatal("incomplete report disclosed source values")
			}
		})
	}
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"empty", json.Number("")},
		{"leading-zero", json.Number("01")},
		{"trailing-dot", json.Number("1.")},
		{"nan-token", json.Number("NaN")},
		{"exponent-overflow", json.Number("1e2147483648")},
		{"exponent-underflow", json.Number("1e-2147483649")},
		{"too-many-bytes", json.Number(strings.Repeat("9", 513))},
		{"leading-space", json.Number(" 1")},
		{"trailing-space", json.Number("1 ")},
		{"surrounding-newlines", json.Number("\n1\n")},
		{"trailing-tab", json.Number("1\t")},
		{"non-json-space", json.Number("\u00a01")},
		{"nan-float", math.NaN()},
		{"positive-infinity", math.Inf(1)},
		{"negative-infinity", math.Inf(-1)},
	} {
		t.Run("invalid-draft-number-"+tc.name, func(t *testing.T) {
			constraintWant(t, constraintCheck(t, "{\"type\":\"number\",\"minimum\":0}", tc.value), true)
		})
	}
	t.Run("invalid-number-syntax-is-a-decode-error", func(t *testing.T) {
		_, raw := constraintDocument(t, "{\"type\":\"number\",\"minimum\":0}")
		for _, malformed := range []string{"01", "1.", "NaN", "Infinity"} {
			data := bytes.Replace(raw, []byte("\"minimum\":0"), []byte("\"minimum\":"+malformed), 1)
			if _, err := Decode(context.Background(), data, podGVK); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid schema JSON accepted: %v", err)
			}
		}
	})
	t.Run("malformed-constraints-survive-type-failure", func(t *testing.T) {
		for _, tc := range []struct{ name, property string }{
			{"minimum", "{\"type\":\"string\",\"minimum\":\"schema-secret-bound\"}"},
			{"complex-enum", "{\"type\":\"string\",\"enum\":[\"allowed\",{\"private\":\"schema-secret-enum\"}]}"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r := constraintCheck(t, tc.property, int64(1))
				constraintWant(t, r, true, "type")
				if strings.Contains(r.Text()+constraintEncode(t, r), "schema-secret") {
					t.Fatal("malformed schema value escaped through a known type error")
				}
			})
		}
	})
	t.Run("reference-wrapper-retains-unsupported-semantics", func(t *testing.T) {
		objectSchema := map[string]any{
			"type": "object", "required": []any{"count", "mode"},
			"properties": map[string]any{
				"count": map[string]any{"type": "integer"},
				"mode":  map[string]any{"type": "string"},
			},
		}
		badObject := map[string]any{"count": "private-draft-value"}
		for _, tc := range []struct {
			name, helpPath string
			annotations    map[string]any
			target, value  any
			locations      []string
		}{
			{"format-with-structural-type-error", "/value", map[string]any{"format": "private-schema-format"}, map[string]any{"type": "string"}, int64(1), []string{"type:/value"}},
			{"map-type", "/value", map[string]any{"x-kubernetes-map-type": "atomic"}, objectSchema, badObject, []string{"required:/value/mode", "type:/value/count"}},
			{"list-type", "/value/0", map[string]any{"x-kubernetes-list-type": "map", "x-kubernetes-list-map-keys": []any{"count"}}, map[string]any{"type": "array", "items": objectSchema}, []any{badObject}, []string{"required:/value/0/mode", "type:/value/0/count"}},
			{"list-map-keys", "/value/0", map[string]any{"x-kubernetes-list-map-keys": []any{"private-schema-key"}}, map[string]any{"type": "array", "items": objectSchema}, []any{badObject}, []string{"required:/value/0/mode", "type:/value/0/count"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				wrapper := map[string]any{
					"allOf":   []any{map[string]any{"$ref": "#/components/schemas/Shared"}},
					"default": "private-schema-default", "example": "private-schema-example",
				}
				for key, value := range tc.annotations {
					wrapper[key] = value
				}
				_, raw := constraintDocument(t, constraintEncode(t, wrapper))
				raw = bytes.Replace(raw, []byte("\"schemas\":{"), []byte("\"schemas\":{\"Shared\":"+constraintEncode(t, tc.target)+","), 1)
				d, err := Decode(context.Background(), raw, podGVK)
				if err != nil {
					t.Fatal(err)
				}
				r, err := d.Check(context.Background(), constraintObject(tc.value))
				if err != nil {
					t.Fatal(err)
				}
				codes, locations := []string{}, []string{}
				for _, diagnostic := range r.Diagnostics {
					locations = append(locations, diagnostic.Code+":"+diagnostic.Pointer)
				}
				for _, location := range tc.locations {
					codes = append(codes, strings.SplitN(location, ":", 2)[0])
				}
				constraintWant(t, r, true, codes...)
				sort.Strings(locations)
				if !reflect.DeepEqual(locations, tc.locations) {
					t.Errorf("wrapper lost supported structural locations: %v", locations)
				}
				help, err := d.Help(context.Background(), tc.helpPath)
				if err != nil || help.Type == "unspecified" {
					t.Fatalf("unsupported wrapper annotation prevented field-help resolution: %+v %v", help, err)
				}
				if strings.Contains(r.Text()+constraintEncode(t, r)+help.Text(), "private-") {
					t.Fatal("reference wrapper disclosed a draft, annotation, default or example value")
				}
			})
		}
	})
}

func TestSchemaConstraintKeywordApplicability(t *testing.T) {
	for _, tc := range []struct {
		name, property string
		value          any
		codes          []string
	}{
		{"numeric-keyword-on-string", "{\"type\":\"string\",\"minimum\":100}", "abc", nil},
		{"length-keyword-on-number", "{\"type\":\"number\",\"minLength\":100}", int64(1), nil},
		{"array-keyword-on-object", "{\"type\":\"object\",\"minItems\":5,\"additionalProperties\":{\"type\":\"integer\"}}", map[string]any{"a": int64(1)}, nil},
		{"object-keyword-on-array", "{\"type\":\"array\",\"minProperties\":5,\"items\":{\"type\":\"integer\"}}", []any{int64(1)}, nil},
		{"untyped-string-does-not-get-number-comparison", "{\"minimum\":100}", "abc", nil},
		{"type-error-is-not-length-error", "{\"type\":\"string\",\"minLength\":100}", int64(1), []string{"type"}},
		{"type-error-is-not-range-error", "{\"type\":\"number\",\"minimum\":100}", "abc", []string{"type"}},
		{"type-error-is-not-enum-error", "{\"type\":\"integer\",\"enum\":[1]}", "1", []string{"type"}},
		{"nullable-skips-inapplicable-length", "{\"type\":\"string\",\"nullable\":true,\"minLength\":100}", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraintWant(t, constraintCheck(t, tc.property, tc.value), false, tc.codes...)
		})
	}
	t.Run("unsupported-semantics-survive-type-failure", func(t *testing.T) {
		for _, property := range []string{
			"{\"type\":\"string\",\"format\":\"private-schema-format\"}",
			"{\"type\":\"string\",\"anyOf\":[{\"type\":\"string\"},{\"type\":\"boolean\"}]}",
		} {
			r := constraintCheck(t, property, int64(1))
			constraintWant(t, r, true, "type")
			if strings.Contains(r.Text(), "private-") {
				t.Fatal("type failure disclosed an unsupported schema value")
			}
		}
	})
}

func TestSchemaIntOrStringOnlyAcceptsCanonicalUnion(t *testing.T) {
	canonical := "{\"x-kubernetes-int-or-string\":true,\"anyOf\":[{\"type\":\"integer\"},{\"type\":\"string\"}],\"enum\":[1,\"one\"]}"
	for _, value := range []any{int64(1), "one", json.Number("1.0")} {
		constraintWant(t, constraintCheck(t, canonical, value), false)
	}
	constraintWant(t, constraintCheck(t, canonical, int64(2)), false, "enum")
	constraintWant(t, constraintCheck(t, canonical, json.Number("1.5")), false, "type")
	constraintWant(t, constraintCheck(t, canonical, true), false, "type")
	reversed := "{\"x-kubernetes-int-or-string\":true,\"anyOf\":[{\"type\":\"string\"},{\"type\":\"integer\"}]}"
	constraintWant(t, constraintCheck(t, reversed, "one"), false)

	for _, property := range []string{
		"{\"x-kubernetes-int-or-string\":true,\"anyOf\":[{\"type\":\"integer\"},{\"type\":\"string\"},{\"type\":\"boolean\"}]}",
		"{\"x-kubernetes-int-or-string\":true,\"anyOf\":[{\"type\":\"integer\",\"minimum\":10},{\"type\":\"string\"}]}",
		"{\"x-kubernetes-int-or-string\":true,\"anyOf\":[{\"type\":\"integer\"},{\"type\":\"integer\"}]}",
		"{\"x-kubernetes-int-or-string\":true,\"anyOf\":[{\"type\":\"integer\"}]}",
		"{\"x-kubernetes-int-or-string\":true,\"allOf\":[{\"type\":\"integer\"},{\"type\":\"string\"}]}",
		"{\"x-kubernetes-int-or-string\":true,\"oneOf\":[{\"type\":\"integer\"},{\"type\":\"string\"}]}",
		"{\"x-kubernetes-int-or-string\":true,\"anyOf\":[{\"type\":\"integer\"},{\"type\":\"string\"}],\"not\":{\"enum\":[1]}}",
	} {
		constraintWant(t, constraintCheck(t, property, int64(1)), true)
	}
}

type constraintCancelAfter struct {
	context.Context
	cancel context.CancelFunc
	calls  int
	after  int
}

func (c *constraintCancelAfter) Err() error {
	c.calls++
	if c.calls >= c.after {
		c.cancel()
	}
	return c.Context.Err()
}

func TestSchemaConstraintBudgetsCancelAndPrivacy(t *testing.T) {
	t.Run("enum-limit", func(t *testing.T) {
		values := make([]any, 128)
		for i := range values {
			values[i] = i
		}
		property := map[string]any{"type": "integer", "enum": values}
		constraintWant(t, constraintCheck(t, constraintEncode(t, property), int64(127)), false)
		property["enum"] = append(values, 128)
		constraintWant(t, constraintCheck(t, constraintEncode(t, property), int64(0)), true)
	})
	t.Run("enum-work-is-shared-between-independent-fields", func(t *testing.T) {
		fields := map[string]any{}
		value := map[string]any{}
		for i := 0; i < MaxWork/128+2; i++ {
			enum := make([]any, 128)
			for j := range enum {
				enum[j] = i*128 + j
			}
			name := "f" + strconv.Itoa(i)
			fields[name] = map[string]any{"type": "integer", "enum": enum}
			value[name] = int64(i*128 + 127)
		}
		property := constraintEncode(t, map[string]any{"type": "object", "properties": fields})
		r := constraintCheck(t, property, value)
		constraintWant(t, r, true)
		if r.Checked > MaxWork+1 {
			t.Fatalf("work amplification escaped shared budget: %d", r.Checked)
		}
	})
	t.Run("string-byte-budget-is-global", func(t *testing.T) {
		block := strings.Repeat("x", 1<<20)
		values := make([]any, 9)
		for i := range values {
			values[i] = block
		}
		r := constraintCheck(t, "{\"type\":\"array\",\"items\":{\"type\":\"string\",\"minLength\":1}}", values)
		constraintWant(t, r, true)
		if len(r.Diagnostics) != 0 {
			t.Fatal("byte exhaustion invented a string-length violation")
		}
	})
	t.Run("canceled-before-check", func(t *testing.T) {
		d, _ := constraintDocument(t, "{\"type\":\"number\",\"minimum\":0}")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := d.Check(ctx, constraintObject(int64(1))); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled check returned %v", err)
		}
	})
	t.Run("canceled-during-inspection", func(t *testing.T) {
		d, _ := constraintDocument(t, "{\"type\":\"array\",\"items\":{\"type\":\"integer\",\"enum\":[0,1,2]}}")
		values := make([]any, 100)
		for i := range values {
			values[i] = int64(2)
		}
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &constraintCancelAfter{Context: base, cancel: cancel, after: 20}
		if _, err := d.Check(ctx, constraintObject(values)); !errors.Is(err, context.Canceled) {
			t.Fatalf("inspection did not observe cancellation: calls=%d err=%v", ctx.calls, err)
		}
	})
	t.Run("constraint-messages-never-copy-values", func(t *testing.T) {
		schemaSecret := "schema-private-allowed-token-123"
		draftSecret := "draft-private-rejected-token-456"
		r := constraintCheck(t, constraintEncode(t, map[string]any{"type": "string", "enum": []any{schemaSecret}}), draftSecret)
		constraintWant(t, r, false, "enum")
		rendered := r.Text() + constraintEncode(t, r)
		for _, secret := range []string{schemaSecret, draftSecret} {
			if strings.Contains(rendered, secret) {
				t.Fatal("constraint diagnostic copied an enum or draft value")
			}
		}
	})
}

func TestSchemaConstraintInspectionDoesNotMutateInputs(t *testing.T) {
	property := "{\"type\":\"object\",\"properties\":{\"mode\":{\"type\":\"string\",\"enum\":[\"fast\"],\"default\":\"fast\"},\"limit\":{\"type\":\"integer\",\"minimum\":9007199254740993,\"maximum\":9007199254740993},\"tags\":{\"type\":\"array\",\"minItems\":1,\"maxItems\":2,\"items\":{\"type\":\"string\",\"minLength\":1,\"maxLength\":4}},\"omitted\":{\"type\":\"string\",\"default\":\"must-not-be-inserted\"}}}"
	d, raw := constraintDocument(t, property)
	schemaBefore := bytes.Clone(raw)
	value := map[string]any{"mode": "private-invalid-mode", "limit": int64(9007199254740993), "tags": []any{"中🚢"}}
	input := constraintObject(value)
	before := constraintEncode(t, input)
	helpBefore, err := d.Help(context.Background(), "/value")
	if err != nil {
		t.Fatal(err)
	}
	first, err := d.Check(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	constraintWant(t, first, false, "enum")
	if len(first.Diagnostics) != 1 || first.Diagnostics[0].Pointer != "/value/mode" {
		t.Fatalf("constraint lost exact field location: %+v", first.Diagnostics)
	}
	good := map[string]any{"mode": "fast", "limit": int64(9007199254740993), "tags": []any{"中🚢"}}
	pass, err := d.Check(context.Background(), constraintObject(good))
	if err != nil {
		t.Fatal(err)
	}
	constraintWant(t, pass, false)
	second, err := d.Check(context.Background(), input)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("checking another draft changed the same document: %+v %+v %v", first, second, err)
	}
	helpAfter, err := d.Help(context.Background(), "/value")
	if err != nil || !reflect.DeepEqual(helpBefore, helpAfter) {
		t.Fatal("checking mutated field-help schema", err)
	}
	if !bytes.Equal(raw, schemaBefore) || constraintEncode(t, input) != before {
		t.Fatal("checking changed source bytes or draft values")
	}
	if _, present := value["omitted"]; present {
		t.Fatal("checking applied a schema default")
	}
}
