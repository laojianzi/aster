package schemaassist

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/laojianzi/aster/internal/manifest"
)

func booleanSetSchema() map[string]any {
	n := listSetTestSchema()
	n["items"] = map[string]any{"type": "boolean"}
	return n
}
func booleanMapSchema(keys ...string) map[string]any {
	n := listMapTestSchema(keys...)
	props := n["items"].(map[string]any)["properties"].(map[string]any)
	for _, key := range keys {
		kind := "boolean"
		if key == "zone" {
			kind = "string"
		}
		if key == "port" {
			kind = "integer"
		}
		props[key] = map[string]any{"type": kind}
	}
	return n
}
func booleanCollectionWant(t testing.TB, r Report, code string, partial, duplicate bool, others ...string) {
	t.Helper()
	if duplicate {
		others = append(others, code)
	}
	constraintWant(t, r, partial, others...)
	message := "List entries repeat the advertised boolean value."
	if code == "list-map-keys" {
		message = "List entries repeat the advertised key identity."
	}
	for _, d := range r.Diagnostics {
		if d.Code == code && (d.Pointer != "/value" || d.Message != message) {
			t.Fatalf("non-private parent diagnostic: %+v", d)
		}
	}
	for _, text := range []string{"required string/int64/boolean map-list identities", "string/int64/boolean set-list values", "Not admission validation.", "Server dry-run remains required."} {
		if !strings.Contains(r.Text(), text) {
			t.Fatalf("missing scope: %s", text)
		}
	}
	if strings.Contains(r.Text(), "PRIVATE_") {
		t.Fatal("report disclosed input")
	}
}

func TestSchemaBooleanCollectionsFiniteIdentity(t *testing.T) {
	// Exhaust every boolean sequence of lengths 0..8. The independent oracle
	// counts the two values; it does not call production identity helpers.
	comparisons := 0
	for length := 0; length <= 8; length++ {
		for mask := 0; mask < 1<<length; mask++ {
			values := make([]any, length)
			trues := 0
			for i := range values {
				b := mask&(1<<i) != 0
				values[i] = b
				if b {
					trues++
				}
			}
			r := listSetTestCheck(t, booleanSetSchema(), values)
			booleanCollectionWant(t, r, "list-set", false, trues > 1 || length-trues > 1)
			comparisons++
		}
	}
	if comparisons != 511 {
		t.Fatal("finite set corpus reduced", comparisons)
	}
	// Vary each component separately, including zero/empty values and opposite
	// metadata order. Compare decoded scalar components, not tuple serialization.
	tuples := 0
	for _, a := range []bool{false, true} {
		for _, b := range []bool{false, true} {
			for _, x := range []int64{0, 1, 9007199254740993} {
				for _, y := range []int64{0, 1, 9007199254740993} {
					for _, zone := range []string{"", "PRIVATE_ZONE", "false"} {
						values := []any{map[string]any{"active": a, "port": x, "zone": "", "payload": "PRIVATE_FIRST"}, map[string]any{"zone": zone, "port": y, "active": b, "payload": "PRIVATE_SECOND"}}
						for _, keys := range [][]string{{"active"}, {"active", "port"}, {"zone", "active", "port"}, {"port", "active", "zone"}} {
							want := a == b && (len(keys) == 1 || x == y) && (len(keys) < 3 || zone == "")
							booleanCollectionWant(t, listMapTestCheck(t, booleanMapSchema(keys...), values), "list-map-keys", false, want)
							tuples++
						}
					}
				}
			}
		}
	}
	if tuples != 432 {
		t.Fatal("finite map corpus reduced", tuples)
	}
	t.Logf("%d exhaustive boolean sequences and %d mixed tuple comparisons", comparisons, tuples)
	decoded, err := manifest.Decode([]byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"n"},"value":[false,true,false]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Object["value"], []any{false, true, false}) {
		t.Fatal("strict decoding lost bool type")
	}
	booleanCollectionWant(t, listSetTestCheck(t, booleanSetSchema(), decoded.Object["value"]), "list-set", false, true)
	for _, isMap := range []bool{false, true} {
		node, code, values := booleanSetSchema(), "list-set", []any{false, false}
		if isMap {
			node, code, values = booleanMapSchema("active"), "list-map-keys", []any{map[string]any{"active": false}, map[string]any{"active": false}}
		}
		before, _ := json.Marshal(values)
		item := node["items"].(map[string]any)
		if isMap {
			item = item["properties"].(map[string]any)["active"].(map[string]any)
		}
		item["default"] = true
		booleanCollectionWant(t, listMapTestCheck(t, node, values), code, false, true)
		after, _ := json.Marshal(values)
		if string(before) != string(after) {
			t.Fatal("default changed explicit false")
		}
		node["nullable"] = true
		booleanCollectionWant(t, listMapTestCheck(t, node, nil), code, false, false)
	}
}

func TestSchemaBooleanCollectionsRuntimeAndMetadataBoundaries(t *testing.T) {
	type namedBool bool
	for _, isMap := range []bool{false, true} {
		prefix := "set"
		if isMap {
			prefix = "map"
		}
		for i, raw := range []any{nil, "PRIVATE_TRUE", int64(1), float64(0), json.Number("1"), namedBool(false), map[string]any{}, []any{}} {
			t.Run(prefix+"-late-type-"+strconv.Itoa(i), func(t *testing.T) {
				node, code, values := booleanSetSchema(), "list-set", []any{false, false, raw}
				if isMap {
					node, code, values = booleanMapSchema("active"), "list-map-keys", []any{map[string]any{"active": false}, map[string]any{"active": false}, map[string]any{"active": raw}}
				}
				d := listMapTestDocument(t, node)
				c, r := listMapTestChecker(context.Background())
				if isMap {
					c.checkListMap(d, node, values, "/value", true)
				} else {
					c.checkListSet(d, node, values, "/value", true)
				}
				booleanCollectionWant(t, *r, code, true, false)
				// Public decoded wrong types also receive the ordinary type diagnostic.
				if i != 5 {
					booleanCollectionWant(t, listMapTestCheck(t, node, values), code, true, false, "type")
				}
			})
		}
		for _, change := range []struct {
			name   string
			mutate func(map[string]any)
		}{
			{"nullable", func(n map[string]any) { n["nullable"] = true }},
			{"malformed-nullable", func(n map[string]any) { n["nullable"] = "PRIVATE_META" }},
			{"missing-type", func(n map[string]any) { delete(n, "type") }},
			{"number", func(n map[string]any) { n["type"] = "number" }},
			{"composition", func(n map[string]any) { n["oneOf"] = []any{map[string]any{}} }},
			{"int-or-string", func(n map[string]any) { n["x-kubernetes-int-or-string"] = true }},
			{"unknown-ref", func(n map[string]any) { n["$ref"] = "#/components/schemas/PRIVATE_MISSING" }},
		} {
			t.Run(prefix+"-metadata-"+change.name, func(t *testing.T) {
				node, code := booleanSetSchema(), "list-set"
				if isMap {
					node, code = booleanMapSchema("active"), "list-map-keys"
				}
				item := node["items"].(map[string]any)
				if isMap {
					item = item["properties"].(map[string]any)["active"].(map[string]any)
				}
				change.mutate(item)
				booleanCollectionWant(t, listMapTestCheck(t, node, []any{}), code, true, false)
			})
		}
	}
	node := booleanMapSchema("active")
	booleanCollectionWant(t, listMapTestCheck(t, node, []any{map[string]any{"active": false}, map[string]any{"active": false}, map[string]any{}}), "list-map-keys", true, false, "required")
	item := node["items"].(map[string]any)
	item["required"] = []any{}
	item["properties"].(map[string]any)["active"].(map[string]any)["default"] = false
	booleanCollectionWant(t, listMapTestCheck(t, node, []any{map[string]any{"active": false}, map[string]any{"active": false}}), "list-map-keys", true, false)
}

func TestSchemaBooleanCollectionsBudgetsReferencesAndPreservation(t *testing.T) {
	for _, isMap := range []bool{false, true} {
		prefix := "set"
		if isMap {
			prefix = "map"
		}
		t.Run(prefix, func(t *testing.T) {
			node, code, values := booleanSetSchema(), "list-set", []any{false, false, true}
			if isMap {
				node, code, values = booleanMapSchema("active"), "list-map-keys", []any{map[string]any{"active": false}, map[string]any{"active": false}, map[string]any{"active": true}}
			}
			d := listMapTestDocument(t, node)
			check := func(c *constraintChecker, v any) {
				if isMap {
					c.checkListMap(d, node, v, "/value", true)
				} else {
					c.checkListSet(d, node, v, "/value", true)
				}
			}
			baseline, br := listMapTestChecker(context.Background())
			check(baseline, values)
			booleanCollectionWant(t, *br, code, false, true)
			for _, meter := range []string{"work", "bytes"} {
				for _, short := range []int{0, 1} {
					t.Run(meter+"-short-"+strconv.Itoa(short), func(t *testing.T) {
						c, r := listMapTestChecker(context.Background())
						if meter == "work" {
							r.Checked = MaxWork - br.Checked + short
						} else {
							c.bytes = MaxConstraintBytes - baseline.bytes + short
						}
						check(c, values)
						booleanCollectionWant(t, *r, code, short == 1, short == 0)
						if c.exhausted != (short == 1) || r.Checked > MaxWork || c.bytes > MaxConstraintBytes {
							t.Fatal("budget escaped or exact allowance rejected")
						}
					})
				}
			}
			for _, count := range []int{MaxListSetItems, MaxListSetItems + 1} {
				many := make([]any, count)
				for i := range many {
					many[i] = false
					if isMap {
						many[i] = map[string]any{"active": false}
					}
				}
				booleanCollectionWant(t, listMapTestCheck(t, node, many), code, count > MaxListSetItems, count == MaxListSetItems)
			}
			booleanCollectionWant(t, listMapTestCheck(t, node, []any(nil)), code, true, false)
			for _, links := range []int{31, 32} {
				n := booleanSetSchema()
				if isMap {
					n = booleanMapSchema("active")
				}
				doc := listMapTestDocument(t, n)
				holder, key := n, "items"
				if isMap {
					holder, key = n["items"].(map[string]any)["properties"].(map[string]any), "active"
				}
				raw := holder[key]
				for i := links - 1; i >= 0; i-- {
					name := "Bool" + strconv.Itoa(i)
					doc.definitions[name] = raw
					raw = map[string]any{"$ref": "#/components/schemas/" + name}
				}
				holder[key] = raw
				r, err := doc.Check(context.Background(), constraintObject([]any{}))
				if err != nil {
					t.Fatal(err)
				}
				booleanCollectionWant(t, r, code, links == 32, false)
			}
			before, _ := json.Marshal(values)
			for _, after := range []int{0, 10, 80, 400} {
				ctx, cancel := context.WithCancel(context.Background())
				controlled := &constraintCancelAfter{Context: ctx, cancel: cancel, after: after}
				if after == 0 {
					cancel()
				}
				many := make([]any, MaxListSetItems)
				for i := range many {
					many[i] = values[i%len(values)]
				}
				r, err := d.Check(controlled, constraintObject(many))
				cancel()
				if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(r, Report{}) {
					t.Fatal("cancelled result escaped", err, r)
				}
			}
			after, _ := json.Marshal(values)
			if string(before) != string(after) {
				t.Fatal("inspection mutated identities")
			}
		})
	}
}
