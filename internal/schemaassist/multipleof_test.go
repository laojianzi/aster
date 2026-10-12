package schemaassist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"
)

func TestSchemaMultipleOfExactDecodedValues(t *testing.T) {
	for _, tc := range []struct {
		name, divisor string
		value         any
		multiple      bool
	}{
		{"integer-multiple", "2", int64(6), true},
		{"integer-nonmultiple", "2", int64(3), false},
		{"negative-multiple", "2", int64(-6), true},
		{"negative-nonmultiple", "2", int64(-3), false},
		{"zero", "3", int64(0), true},
		{"negative-zero", "1e2147483647", json.Number("-0e-2147483648"), true},
		{"float-negative-zero", "0.01", math.Copysign(0, -1), true},
		{"large-integer-multiple", "9007199254740993", int64(18014398509481986), true},
		{"large-integer-neighbor", "9007199254740993", int64(18014398509481985), false},
		{"large-integer-neighbor-above", "9007199254740993", int64(18014398509481987), false},
		{"max-int64-self", "9223372036854775807", int64(math.MaxInt64), true},
		{"min-int64-power-of-two", "2", int64(math.MinInt64), true},
		{"max-uint64-self", "18446744073709551615", uint64(math.MaxUint64), true},
		{"uint64-even-boundary", "2", uint64(math.MaxUint64), false},
		{"equivalent-decimal-spelling", "0.0100", json.Number("7e-2"), true},
		{"decimal-nonmultiple", "0.01", json.Number("0.070000000000000001"), false},
		{"shortest-float-decimal", "0.1", float64(0.3), true},
		{"shortest-float-hundredth", "0.01", float64(0.07), true},
		{"float32-shortest-decimal", "0.1", float32(0.3), true},
		{"float-close-is-not-equal", "0.1", float64(0.30000000001), false},
		{"exact-close-above", "0.1", json.Number("0.30000000001"), false},
		{"exact-close-below", "0.1", json.Number("0.29999999999"), false},
		{"fractional-divisor-integer-value", "0.25", int64(1), true},
		{"fractional-divisor-zero-value", "0.25", int64(0), true},
		{"fractional-divisor-negative-value", "0.25", int64(-1), true},
		{"quarter-multiple", "0.25", json.Number("0.75"), true},
		{"quarter-nonmultiple", "0.25", json.Number("0.7"), false},
		{"coefficient-needs-power-of-ten", "4e-2", json.Number("12e-1"), true},
		{"coefficient-prime-factor", "6e-7", int64(1), false},
		{"coefficient-cancels-prime-factor", "6", json.Number("3e3"), true},
		{"equal-powers", "12e-100", json.Number("36e-100"), true},
		{"smaller-normalized-power", "3", json.Number("1.2"), false},
		{"smaller-power-and-larger-coefficient", "1e2", json.Number("999"), false},
		{"huge-positive-difference", "1e-2147483648", json.Number("1e2147483647"), true},
		{"huge-positive-modular-rejection", "3e-2147483648", json.Number("1e2147483647"), false},
		{"huge-negative-difference", "1e2147483647", json.Number("1e-2147483648"), false},
		{"huge-equal-exponents", "3e2147483647", json.Number("9e2147483647"), true},
		{"huge-negative-exponents", "3e-2147483648", json.Number("9e-2147483648"), true},
		{"normalization-crosses-32-bit-difference", "3e-2147483648", json.Number("1" + strings.Repeat("0", 498) + "e2147483647"), false},
		{"512-digit-coefficient-match", strings.Repeat("9", 512), json.Number(strings.Repeat("9", 512)), true},
		{"512-digit-coefficient-nonmatch", strings.Repeat("9", 512), json.Number(strings.Repeat("9", 511) + "8"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			property := constraintEncode(t, map[string]any{"type": "number", "multipleOf": json.Number(tc.divisor)})
			codes := []string{}
			if !tc.multiple {
				codes = append(codes, "multipleOf")
			}
			r := constraintCheck(t, property, tc.value)
			constraintWant(t, r, false, codes...)
			for _, diagnostic := range r.Diagnostics {
				if diagnostic.Pointer != "/value" {
					t.Fatal("multipleOf diagnostic lost its exact field pointer")
				}
			}
		})
	}
}

func TestSchemaMultipleOfMatchesIndependentRationalOracle(t *testing.T) {
	// big.Rat division over small exponents is independent of the production
	// modular-power implementation. It is only the test oracle: production
	// must not allocate a rational with an enormous expanded decimal power.
	values := []string{
		"-9e24", "-18014398509481987", "-18014398509481986", "-125.5", "-12.5",
		"-3", "-0.3", "-0.1", "-1e-24", "-0", "0", "0.000e20",
		"1e-24", "0.000125", "0.01", "0.07", "0.1", "0.25", "0.3",
		"0.29999999999", "0.30000000001", "0.75", "1", "1.000", "100e-2",
		"1.25", "3", "7", "12.5", "125.5", "9007199254740993",
		"18014398509481986", "18014398509481987", "1e24", "9e24",
	}
	divisors := []string{
		"1e-24", "0.000125", "0.01", "0.1", "0.25", "0.3", "1", "1.000",
		"100e-2", "2", "3", "6", "7", "12.5", "9007199254740993", "1e24", "9e24",
	}
	for _, divisor := range divisors {
		limit, ok := new(big.Rat).SetString(divisor)
		if !ok || limit.Sign() <= 0 {
			t.Fatal("invalid rational oracle divisor")
		}
		d, _ := constraintDocument(t, constraintEncode(t, map[string]any{"type": "number", "multipleOf": json.Number(divisor)}))
		for _, value := range values {
			operand, ok := new(big.Rat).SetString(value)
			if !ok {
				t.Fatal("invalid rational oracle value")
			}
			multiple := new(big.Rat).Quo(operand, limit).IsInt()
			r, err := d.Check(context.Background(), constraintObject(json.Number(value)))
			if err != nil {
				t.Fatal(err)
			}
			if r.Partial || (len(r.Diagnostics) == 0) != multiple ||
				(!multiple && (len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "multipleOf")) {
				t.Fatalf("value %q divisor %q disagreed with rational division: %+v", value, divisor, r)
			}
		}
	}
}

func TestSchemaMultipleOfRejectsMalformedAndInapplicable(t *testing.T) {
	for _, tc := range []struct {
		name, property string
		value          any
		partial        bool
		codes          []string
	}{
		{"zero-divisor", "{\"type\":\"number\",\"multipleOf\":0}", int64(1), true, nil},
		{"zero-value-zero-divisor", "{\"type\":\"number\",\"multipleOf\":0}", int64(0), true, nil},
		{"negative-zero-divisor", "{\"type\":\"number\",\"multipleOf\":-0.0}", int64(0), true, nil},
		{"negative-divisor", "{\"type\":\"number\",\"multipleOf\":-2}", int64(4), true, nil},
		{"null-divisor", "{\"type\":\"number\",\"multipleOf\":null}", int64(4), true, nil},
		{"string-divisor", "{\"type\":\"number\",\"multipleOf\":\"PRIVATE_SCHEMA_DIVISOR\"}", int64(4), true, nil},
		{"boolean-divisor", "{\"type\":\"number\",\"multipleOf\":true}", int64(4), true, nil},
		{"object-divisor", "{\"type\":\"number\",\"multipleOf\":{\"secret\":\"PRIVATE_SCHEMA_DIVISOR\"}}", int64(4), true, nil},
		{"array-divisor", "{\"type\":\"number\",\"multipleOf\":[2]}", int64(4), true, nil},
		{"divisor-exponent-out-of-range", "{\"type\":\"number\",\"multipleOf\":1e2147483648}", int64(4), true, nil},
		{"divisor-too-long", "{\"type\":\"number\",\"multipleOf\":" + strings.Repeat("9", 513) + "}", int64(4), true, nil},
		{"valid-metadata-on-wrong-type", "{\"type\":\"number\",\"multipleOf\":2}", "PRIVATE_DRAFT_VALUE", false, []string{"type"}},
		{"invalid-metadata-survives-type-error", "{\"type\":\"number\",\"multipleOf\":0}", "PRIVATE_DRAFT_VALUE", true, []string{"type"}},
		{"invalid-metadata-on-string", "{\"type\":\"string\",\"multipleOf\":0}", "PRIVATE_DRAFT_VALUE", true, nil},
		{"valid-metadata-on-string", "{\"type\":\"string\",\"multipleOf\":2}", "3", false, nil},
		{"valid-metadata-on-untyped-string", "{\"multipleOf\":2}", "3", false, nil},
		{"untyped-number", "{\"multipleOf\":2}", int64(3), false, []string{"multipleOf"}},
		{"valid-metadata-on-boolean", "{\"type\":\"boolean\",\"multipleOf\":2}", true, false, nil},
		{"nullable", "{\"type\":\"number\",\"nullable\":true,\"multipleOf\":2}", nil, false, nil},
		{"nullable-with-bad-metadata", "{\"type\":\"number\",\"nullable\":true,\"multipleOf\":-2}", nil, true, nil},
		{"nonnullable", "{\"type\":\"number\",\"multipleOf\":2}", nil, false, []string{"type"}},
		{"canonical-union-integer", "{\"x-kubernetes-int-or-string\":true,\"anyOf\":[{\"type\":\"integer\"},{\"type\":\"string\"}],\"multipleOf\":2}", int64(3), false, []string{"multipleOf"}},
		{"canonical-union-string", "{\"x-kubernetes-int-or-string\":true,\"anyOf\":[{\"type\":\"integer\"},{\"type\":\"string\"}],\"multipleOf\":2}", "3", false, nil},
		{"enum-and-multiple", "{\"type\":\"number\",\"enum\":[4],\"multipleOf\":2}", int64(3), false, []string{"enum", "multipleOf"}},
		{"bounds-and-multiple", "{\"type\":\"number\",\"minimum\":5,\"maximum\":10,\"multipleOf\":2}", int64(3), false, []string{"minimum", "multipleOf"}},
		{"unsupported-alongside-known-mismatch", "{\"type\":\"number\",\"format\":\"PRIVATE_SCHEMA_FORMAT\",\"multipleOf\":2}", int64(3), true, []string{"multipleOf"}},
		{"known-type-error-retains-other-unsupported", "{\"type\":\"number\",\"pattern\":\"PRIVATE_PATTERN\",\"multipleOf\":2}", "PRIVATE_DRAFT_VALUE", true, []string{"type"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := constraintCheck(t, tc.property, tc.value)
			constraintWant(t, r, tc.partial, tc.codes...)
			if strings.Contains(r.Text()+constraintEncode(t, r), "PRIVATE_") {
				t.Fatal("multipleOf inspection disclosed schema or draft content")
			}
		})
	}
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"nan", math.NaN()},
		{"infinity", math.Inf(1)},
		{"invalid-numeric-token", json.Number("private-number")},
		{"leading-zero-token", json.Number("01")},
		{"value-exponent-out-of-range", json.Number("1e2147483648")},
		{"value-too-long", json.Number(strings.Repeat("9", 513))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraintWant(t, constraintCheck(t, "{\"type\":\"number\",\"multipleOf\":2}", tc.value), true)
		})
	}
}

func TestSchemaMultipleOfBudgetsAndCancellation(t *testing.T) {
	// A legal bounded coefficient plus an enormous exponent must not allocate
	// the expanded integer. Repeating that work must exhaust the shared
	// inspection allowance, even when every quotient would be an integer.
	coefficient := strings.Repeat("9", 499)
	property := constraintEncode(t, map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "number", "multipleOf": json.Number(coefficient + "e-2147483648"),
		},
	})
	values := make([]any, 16)
	for i := range values {
		values[i] = json.Number(coefficient + "e2147483647")
	}
	t.Run("one-large-modular-check-is-bounded", func(t *testing.T) {
		constraintWant(t, constraintCheck(t, property, values[:1]), false)
	})
	t.Run("arithmetic-work-is-shared", func(t *testing.T) {
		r := constraintCheck(t, property, values)
		constraintWant(t, r, true)
		if r.Checked < MaxWork/2 || r.Checked > MaxWork+len(values)+8 {
			t.Fatalf("arithmetic escaped the shared work allowance: %d", r.Checked)
		}
	})
	t.Run("coefficient-times-power-needs-real-modular-exponentiation", func(t *testing.T) {
		// No early zero remainder is possible here. 10^n is never divisible
		// by a positive number whose final decimal digit is 3.
		divisor := strings.Repeat("9", 498) + "3e-2147483648"
		p := constraintEncode(t, map[string]any{"type": "number", "multipleOf": json.Number(divisor)})
		constraintWant(t, constraintCheck(t, p, json.Number("1e2147483647")), false, "multipleOf")
	})
	t.Run("cancel-before-inspection", func(t *testing.T) {
		d, _ := constraintDocument(t, property)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := d.Check(ctx, constraintObject(values)); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation was lost: %v", err)
		}
	})
	t.Run("cancel-during-repeated-arithmetic", func(t *testing.T) {
		d, _ := constraintDocument(t, property)
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &constraintCancelAfter{Context: base, cancel: cancel, after: 30}
		if _, err := d.Check(ctx, constraintObject(values)); !errors.Is(err, context.Canceled) {
			t.Fatalf("in-flight cancellation was lost: %v", err)
		}
	})
	t.Run("prior-byte-budget-does-not-fabricate-mismatch", func(t *testing.T) {
		r := Report{}
		c := constraintChecker{
			ctx: context.Background(), report: &r, bytes: MaxConstraintBytes,
			add: func(path, code, message string) {
				r.Diagnostics = append(r.Diagnostics, Diagnostic{path, code, message})
			},
		}
		c.checkMultipleOf(map[string]any{"multipleOf": json.Number("2")}, int64(3), "/value", true)
		constraintWant(t, r, true)
	})
}

func TestSchemaMultipleOfInspectionPreservesInputsAndPrivacy(t *testing.T) {
	property := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "number", "multipleOf": json.Number("987654321098765432109876543211")},
			"amount": map[string]any{
				"type": "number", "multipleOf": json.Number("0.1"),
				"default": "PRIVATE_DEFAULT", "example": "PRIVATE_EXAMPLE",
			},
		},
	}
	d, raw := constraintDocument(t, constraintEncode(t, property))
	rawBefore := append([]byte(nil), raw...)
	object := constraintObject(map[string]any{
		"count": json.Number("987654321098765432109876543212"), "amount": json.Number("0.30000000001"),
	})
	objectBefore := constraintEncode(t, object)
	documentBefore := constraintEncode(t, d.root)
	r, err := d.Check(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	constraintWant(t, r, false, "multipleOf", "multipleOf")
	if constraintEncode(t, object) != objectBefore || constraintEncode(t, d.root) != documentBefore || !bytes.Equal(raw, rawBefore) {
		t.Fatal("multipleOf inspection mutated draft, document or raw source")
	}
	wantPointers := map[string]bool{"/value/count": true, "/value/amount": true}
	gotPointers := map[string]bool{}
	for _, diagnostic := range r.Diagnostics {
		gotPointers[diagnostic.Pointer] = true
	}
	if !reflect.DeepEqual(gotPointers, wantPointers) {
		t.Fatal("multipleOf inspection lost exact diagnostic pointers")
	}
	output := r.Text() + constraintEncode(t, r)
	for _, private := range []string{"987654321098765432109876543211", "987654321098765432109876543212", "0.30000000001", "PRIVATE_"} {
		if strings.Contains(output, private) {
			t.Fatal("multipleOf report disclosed a value, divisor, default or example")
		}
	}
	again, err := d.Check(context.Background(), object)
	if err != nil || !reflect.DeepEqual(r, again) {
		t.Fatal("read-only multipleOf inspection was not repeatable on unchanged inputs")
	}
}
