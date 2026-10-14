package schemaassist

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/laojianzi/aster/internal/manifest"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func integerSetSchema() map[string]any {
	n := listSetTestSchema()
	n["items"] = map[string]any{"type": "integer"}
	return n
}
func integerSetWant(t testing.TB, r Report, partial, duplicate bool, codes ...string) {
	t.Helper()
	if duplicate {
		codes = append(codes, "list-set")
	}
	constraintWant(t, r, partial, codes...)
	for _, d := range r.Diagnostics {
		if d.Code == "list-set" && (d.Pointer != "/value" || d.Message != "List entries repeat the advertised integer value.") {
			t.Fatalf("integer set lost fixed parent hint: %+v", d)
		}
	}
	for _, text := range []string{"string/int64 set-list values", "Not admission validation.", "Server dry-run remains required."} {
		if !strings.Contains(r.Text(), text) {
			t.Fatalf("missing scope/boundary: %s", text)
		}
	}
}
func TestSchemaIntegerSetExactIdentity(t *testing.T) {
	corpus := []int64{math.MinInt64, math.MinInt64 + 1, -9007199254740993, -1, 0, 1, 8080, 9007199254740992, 9007199254740993, math.MaxInt64 - 1, math.MaxInt64}
	d := listSetTestDocument(t, integerSetSchema())
	comparisons := 0
	// Independent oracle sorts an owned copy instead of hashing or calling a
	// production identity/enum helper. Ordered triples include late matches.
	for _, a := range corpus {
		for _, b := range corpus {
			for _, c := range corpus {
				sorted := []int64{a, b, c}
				sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
				r, err := d.Check(context.Background(), constraintObject([]any{a, b, c}))
				if err != nil {
					t.Fatal(err)
				}
				integerSetWant(t, r, false, sorted[0] == sorted[1] || sorted[1] == sorted[2])
				comparisons++
			}
		}
	}
	if comparisons != 1331 {
		t.Fatal("integer oracle corpus reduced", comparisons)
	}
	t.Logf("%d independent sorted-triple integer comparisons", comparisons)
	for _, values := range []any{[]any{}, []any{int64(0)}} {
		integerSetWant(t, listSetTestCheck(t, integerSetSchema(), values), false, false)
	}
	obj, err := manifest.Decode([]byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"n"},"value":[-9223372036854775808,9007199254740992,9007199254740993,9223372036854775807,9223372036854775807]}`))
	if err != nil {
		t.Fatal(err)
	}
	values := obj.Object["value"].([]any)
	if !reflect.DeepEqual(values, []any{int64(math.MinInt64), int64(9007199254740992), int64(9007199254740993), int64(math.MaxInt64), int64(math.MaxInt64)}) {
		t.Fatal("strict decoder lost exact int64 values")
	}
	integerSetWant(t, listSetTestCheck(t, integerSetSchema(), values), false, true)
	n := integerSetSchema()
	delete(n, "x-kubernetes-list-type")
	integerSetWant(t, listSetTestCheck(t, n, []any{int64(1), int64(1)}), false, false)
	n = integerSetSchema()
	n["nullable"] = true
	integerSetWant(t, listSetTestCheck(t, n, nil), false, false)
	n = integerSetSchema()
	n["items"].(map[string]any)["default"] = int64(42)
	integerSetWant(t, listSetTestCheck(t, n, []any{int64(0), int64(0), int64(42)}), false, true)
}
func TestSchemaIntegerSetRuntimeAndMetadataBoundaries(t *testing.T) {
	// Exercise the complete document walk, not just the identity helper. Its
	// independent type diagnostics must not revive an early duplicate hint.
	for _, tc := range []struct {
		name  string
		value any
		codes []string
	}{
		{"float32", float32(42), nil},
		{"host-int32", int32(42), nil},
		{"unsigned-endpoint", uint64(math.MaxUint64), nil},
		{"quoted-item", "PRIVATE_REVIEW_VALUE", []string{"type"}},
		{"null-item", nil, []string{"type"}},
		{"fractional-item", float64(42.5), []string{"type"}},
	} {
		t.Run("whole-document-"+tc.name, func(t *testing.T) {
			values := []any{int64(42), int64(42), tc.value}
			integerSetWant(t, listSetTestCheck(t, integerSetSchema(), values), true, false, tc.codes...)
			if !reflect.DeepEqual(values, []any{int64(42), int64(42), tc.value}) {
				t.Fatal("whole-document inspection changed an integer-set item")
			}
		})
	}
	for i, raw := range []any{nil, float64(1), float64(1.5), math.Inf(1), math.NaN(), json.Number("1"), int(1), uint64(1), "PRIVATE_INTEGER", true, map[string]any{}, []any{}} {
		t.Run("late-runtime-"+strconv.Itoa(i), func(t *testing.T) {
			n := integerSetSchema()
			c, r := listMapTestChecker(context.Background())
			c.checkListSet(listSetTestDocument(t, n), n, []any{int64(1), int64(1), raw}, "/value", true)
			integerSetWant(t, *r, true, false)
		})
	}
	for _, kind := range []string{"number", "boolean", "object", "array"} {
		n := integerSetSchema()
		n["items"] = map[string]any{"type": kind}
		integerSetWant(t, listSetTestCheck(t, n, []any{}), true, false)
	}
	for _, change := range []func(map[string]any){
		func(n map[string]any) { n["items"].(map[string]any)["nullable"] = true },
		func(n map[string]any) { n["items"].(map[string]any)["nullable"] = "false" },
		func(n map[string]any) { n["items"].(map[string]any)["oneOf"] = []any{map[string]any{}} },
		func(n map[string]any) { n["items"].(map[string]any)["x-kubernetes-int-or-string"] = true },
		func(n map[string]any) { n["x-kubernetes-list-map-keys"] = []any{} },
		func(n map[string]any) { n["items"] = map[string]any{"$ref": "#/components/schemas/Missing"} },
	} {
		n := integerSetSchema()
		change(n)
		integerSetWant(t, listSetTestCheck(t, n, []any{}), true, false)
	}
	n := integerSetSchema()
	n["items"].(map[string]any)["default"] = int64(42)
	integerSetWant(t, listSetTestCheck(t, n, []any{int64(42), int64(42), nil}), true, false, "type")
}
func TestSchemaIntegerSetBudgetsReferencesAndPreservation(t *testing.T) {
	for _, count := range []int{MaxListSetItems, MaxListSetItems + 1} {
		values := make([]any, count)
		for i := range values {
			values[i] = int64(i)
		}
		values[count-1] = int64(0)
		integerSetWant(t, listSetTestCheck(t, integerSetSchema(), values), count > MaxListSetItems, count == MaxListSetItems)
	}
	integerSetWant(t, listSetTestCheck(t, integerSetSchema(), []any(nil)), true, false)
	n := integerSetSchema()
	d := listSetTestDocument(t, n)
	values := []any{int64(1), int64(1), int64(3)}
	baseline, br := listMapTestChecker(context.Background())
	baseline.checkListSet(d, n, values, "/value", true)
	integerSetWant(t, *br, false, true)
	// The exact remaining allowance must succeed as well as the one-unit-
	// shorter allowance below failing. Avoid an off-by-one over-rejection.
	for _, meter := range []string{"work", "bytes"} {
		t.Run("exact-"+meter, func(t *testing.T) {
			c, r := listMapTestChecker(context.Background())
			if meter == "work" {
				r.Checked = MaxWork - br.Checked
			} else {
				c.bytes = MaxConstraintBytes - baseline.bytes
			}
			c.checkListSet(d, n, values, "/value", true)
			integerSetWant(t, *r, false, true)
			if c.exhausted || c.bytes > MaxConstraintBytes || r.Checked > MaxWork {
				t.Fatal("exact integer-set budget was rejected or exceeded")
			}
		})
	}
	for _, meter := range []string{"work", "bytes"} {
		t.Run("late-"+meter, func(t *testing.T) {
			c, r := listMapTestChecker(context.Background())
			if meter == "work" {
				r.Checked = MaxWork - br.Checked + 1
			} else {
				c.bytes = MaxConstraintBytes - baseline.bytes + 1
			}
			c.checkListSet(d, n, values, "/value", true)
			integerSetWant(t, *r, true, false)
			if !c.exhausted || c.bytes > MaxConstraintBytes || r.Checked > MaxWork {
				t.Fatal("integer set escaped shared budget")
			}
		})
	}
	distinct, dr := listMapTestChecker(context.Background())
	distinct.checkListSet(d, n, []any{int64(1), int64(2), int64(3)}, "/value", true)
	integerSetWant(t, *dr, false, false)
	if distinct.bytes != baseline.bytes || dr.Checked != br.Checked {
		t.Fatal("early duplicate shortened accounting")
	}
	for _, links := range []int{31, 32} {
		n := integerSetSchema()
		d := listSetTestDocument(t, n)
		raw := n["items"]
		for i := links - 1; i >= 0; i-- {
			name := "IntegerSet" + strconv.Itoa(i)
			d.definitions[name] = raw
			raw = map[string]any{"$ref": "#/components/schemas/" + name}
		}
		n["items"] = raw
		r, err := d.Check(context.Background(), constraintObject([]any{}))
		if err != nil {
			t.Fatal(err)
		}
		integerSetWant(t, r, links == 32, false)
	}
	for _, after := range []int{0, 10, 80, 400} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		controlled := &constraintCancelAfter{Context: ctx, cancel: cancel, after: after}
		if after == 0 {
			cancel()
		}
		many := make([]any, MaxListSetItems)
		for i := range many {
			many[i] = int64(1)
		}
		r, err := d.Check(controlled, constraintObject(many))
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(r, Report{}) {
			t.Fatal("cancellation returned speculative evidence", err)
		}
	}
	n = integerSetSchema()
	n["description"] = "PRIVATE_SCHEMA"
	n["items"].(map[string]any)["default"] = int64(987654321)
	d = listSetTestDocument(t, n)
	obj := constraintObject([]any{int64(123456789), int64(123456789)})
	beforeObj, beforeSchema := constraintEncode(t, obj), constraintEncode(t, d.root)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 20 {
				r, err := d.Check(context.Background(), obj)
				if err != nil {
					t.Error(err)
					return
				}
				integerSetWant(t, r, false, true)
				for _, private := range []string{"PRIVATE_SCHEMA", "987654321", "123456789"} {
					if strings.Contains(r.Text(), private) {
						t.Error("private value disclosed")
					}
				}
			}
		})
	}
	workers.Wait()
	if constraintEncode(t, obj) != beforeObj || constraintEncode(t, d.root) != beforeSchema {
		t.Fatal("integer set inspection changed input")
	}
}
