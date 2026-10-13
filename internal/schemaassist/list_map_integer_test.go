package schemaassist

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/laojianzi/aster/internal/manifest"
)

func integerMapSchema(keys ...string) map[string]any {
	n := listMapTestSchema(keys...)
	for _, k := range keys {
		if k != "zone" {
			n["items"].(map[string]any)["properties"].(map[string]any)[k] = map[string]any{"type": "integer"}
		}
	}
	return n
}

func integerMapWant(t testing.TB, r Report, partial, duplicate bool, codes ...string) {
	t.Helper()
	if duplicate {
		codes = append(codes, "list-map-keys")
	}
	constraintWant(t, r, partial, codes...)
	for _, d := range r.Diagnostics {
		if d.Code == "list-map-keys" && (d.Pointer != "/value" || d.Message != "List entries repeat the advertised key identity.") {
			t.Fatalf("integer hint lost its generic parent location: %+v", d)
		}
	}
}

func TestSchemaIntegerMapKeysExactIdentity(t *testing.T) {
	corpus := []int64{math.MinInt64, math.MinInt64 + 1, -9007199254740993, -1, 0, 1, 8080, 9007199254740992, 9007199254740993, math.MaxInt64 - 1, math.MaxInt64}
	// Pairwise scalar comparisons are independent of the production fixed tuple.
	comparisons := 0
	for _, a := range corpus {
		for _, b := range corpus {
			for _, zone := range []string{"east", "west"} {
				values := []any{map[string]any{"port": a, "zone": "east", "payload": "PRIVATE_FIRST"}, map[string]any{"zone": zone, "port": b, "payload": "PRIVATE_SECOND"}}
				for _, keys := range [][]string{{"port"}, {"port", "zone"}, {"zone", "port"}} {
					comparisons++
					want := a == b && (len(keys) == 1 || zone == "east")
					integerMapWant(t, listMapTestCheck(t, integerMapSchema(keys...), values), false, want)
				}
			}
		}
	}
	if comparisons != 726 {
		t.Fatal("independent integer corpus reduced", comparisons)
	}
	t.Logf("%d independent single/mixed/order-reversed integer tuple comparisons", comparisons)
	for _, values := range []any{[]any{}, []any{map[string]any{"port": int64(0)}}} {
		integerMapWant(t, listMapTestCheck(t, integerMapSchema("port"), values), false, false)
	}
	n := integerMapSchema("port")
	n["nullable"] = true
	integerMapWant(t, listMapTestCheck(t, n, nil), false, false)
	integerMapWant(t, listMapTestCheck(t, integerMapSchema("port"), "PRIVATE_PARENT"), false, false, "type")
}

func TestSchemaIntegerMapKeysRuntimeAndMetadataBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		codes []string
	}{
		{"float-integral", float64(8080), nil}, {"float-fraction", 8080.5, []string{"type"}},
		{"json-number", json.Number("8080"), nil}, {"host-int", int(8080), nil},
		{"unsigned", uint64(8080), nil}, {"unsigned-overflow", uint64(math.MaxUint64), nil},
		{"quoted", "8080", []string{"type"}}, {"boolean", true, []string{"type"}},
		{"null", nil, []string{"type"}}, {"object", map[string]any{}, []string{"type"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := []any{map[string]any{"port": int64(8080)}, map[string]any{"port": int64(8080)}, map[string]any{"port": tc.value}}
			integerMapWant(t, listMapTestCheck(t, integerMapSchema("port"), values), true, false, tc.codes...)
		})
	}
	t.Run("missing-after-duplicate", func(t *testing.T) {
		values := []any{map[string]any{"port": int64(0)}, map[string]any{"port": int64(0)}, map[string]any{}}
		integerMapWant(t, listMapTestCheck(t, integerMapSchema("port"), values), true, false, "required")
	})
	for _, change := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"number-schema", func(n map[string]any) { n["type"] = "number" }},
		{"nullable", func(n map[string]any) { n["nullable"] = true }},
		{"malformed-nullable", func(n map[string]any) { n["nullable"] = "PRIVATE_META" }},
		{"int-or-string", func(n map[string]any) { n["x-kubernetes-int-or-string"] = true }},
		{"composition", func(n map[string]any) { n["allOf"] = []any{map[string]any{}} }},
	} {
		t.Run(change.name, func(t *testing.T) {
			n := integerMapSchema("port")
			change.mutate(n["items"].(map[string]any)["properties"].(map[string]any)["port"].(map[string]any))
			integerMapWant(t, listMapTestCheck(t, n, []any{}), true, false)
		})
	}
	t.Run("optional-default-only", func(t *testing.T) {
		n := integerMapSchema("port")
		item := n["items"].(map[string]any)
		item["required"] = []any{}
		item["properties"].(map[string]any)["port"].(map[string]any)["default"] = int64(8080)
		integerMapWant(t, listMapTestCheck(t, n, []any{map[string]any{"port": int64(8080)}, map[string]any{"port": int64(8080)}}), true, false)
	})
	t.Run("explicit-default-does-not-change-identity", func(t *testing.T) {
		n := integerMapSchema("port")
		n["items"].(map[string]any)["properties"].(map[string]any)["port"].(map[string]any)["default"] = int64(8080)
		integerMapWant(t, listMapTestCheck(t, n, []any{map[string]any{"port": int64(0)}, map[string]any{"port": int64(0)}}), false, true)
	})
}

func TestSchemaIntegerMapKeysBudgetsReferencesAndPreservation(t *testing.T) {
	n := integerMapSchema("port", "zone")
	values := []any{map[string]any{"port": int64(math.MaxInt64), "zone": "PRIVATE_ZONE"}, map[string]any{"port": int64(math.MaxInt64), "zone": "PRIVATE_ZONE"}, map[string]any{"port": int64(42), "zone": "PRIVATE_LATE"}}
	d := listMapTestDocument(t, n)
	baseline, br := listMapTestChecker(context.Background())
	baseline.checkListMap(d, n, values, "/value", true)
	integerMapWant(t, *br, false, true)
	for _, mode := range []string{"work", "bytes", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, r := listMapTestChecker(ctx)
			switch mode {
			case "work":
				r.Checked = MaxWork - br.Checked + 1
			case "bytes":
				c.bytes = MaxConstraintBytes - baseline.bytes + 1
			case "cancel":
				cancel()
			}
			c.checkListMap(d, n, values, "/value", true)
			integerMapWant(t, *r, true, false)
			if !c.exhausted || r.Checked > MaxWork || c.bytes > MaxConstraintBytes {
				t.Fatal("identity exceeded shared limits")
			}
		})
	}
	many := make([]any, MaxListMapItems+1)
	for i := range many {
		many[i] = map[string]any{"port": int64(i)}
	}
	integerMapWant(t, listMapTestCheck(t, integerMapSchema("port"), many[:MaxListMapItems]), false, false)
	integerMapWant(t, listMapTestCheck(t, integerMapSchema("port"), many), true, false)
	n = integerMapSchema("port")
	d = listMapTestDocument(t, n)
	item := n["items"].(map[string]any)
	d.definitions["Port"] = map[string]any{"type": "integer"}
	item["properties"].(map[string]any)["port"] = map[string]any{"$ref": "#/components/schemas/Port"}
	r, err := d.Check(context.Background(), constraintObject([]any{map[string]any{"port": int64(7)}, map[string]any{"port": int64(7)}}))
	if err != nil {
		t.Fatal(err)
	}
	integerMapWant(t, r, false, true)
	d.root["properties"].(map[string]any)["metadata"] = map[string]any{"type": "object", "additionalProperties": true}
	// Exercise the same strict manifest decoder as the application, including
	// exact int64 endpoints. No float or string conversion is the oracle.
	for _, v := range []int64{math.MinInt64, 9007199254740993, math.MaxInt64} {
		text := `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"test"},"value":[{"port":` + strconv.FormatInt(v, 10) + `},{"port":` + strconv.FormatInt(v, 10) + `}]}`
		obj, err := manifest.Decode([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		before := obj.DeepCopy()
		r, err = d.Check(context.Background(), obj.Object)
		if err != nil {
			t.Fatal(err)
		}
		integerMapWant(t, r, false, true)
		if !reflect.DeepEqual(obj.Object, before.Object) || strings.Contains(r.Text(), strconv.FormatInt(v, 10)) {
			t.Fatal("integer identity rewrote or disclosed input")
		}
	}
}
