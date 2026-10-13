package schemaassist

import "unicode/utf8"

const (
	MaxListSetItems      = 256
	MaxListSetValueBytes = 16 << 10
)

// Only explicit, non-nullable string or decoded int64 items are inspected.
// Server defaulting, numeric runtime types and compound-value serialization are
// separate contracts; the structured-enum comparator is not a set comparator.
func (c *constraintChecker) listSetSchema(d *Document, node map[string]any) (string, bool) {
	_, mapKeys := node["x-kubernetes-list-map-keys"]
	_, uninspected := node["x-aster-uninspected-reference"]
	if node["x-kubernetes-list-type"] != "set" || node["type"] != "array" || nodeType(node) != "array" || mapKeys || uninspected || composite(node) {
		c.report.Partial = true
		return "", false
	}
	item, valid := c.listIdentitySchema(d, node["items"])
	if !valid {
		return "", false
	}
	kind := nodeType(item)
	if (kind != "string" && kind != "integer") || item["type"] != kind || !listIdentityNonnullable(item) {
		c.report.Partial = true
		return "", false
	}
	return kind, true
}

func (c *constraintChecker) checkListSet(d *Document, node map[string]any, value any, path string, inspectValue bool) {
	if c.exhausted {
		return
	}
	kind, valid := c.listSetSchema(d, node)
	if !valid || !inspectValue {
		return
	}
	// Wrong-type and nullable-null parent values still inspect the advertised
	// metadata above. A typed nil slice is not a decoded JSON empty array.
	items, applicable := value.([]any)
	if !applicable {
		return
	}
	if items == nil || len(items) > MaxListSetItems {
		c.report.Partial = true
		return
	}
	if !c.chargeWork(1+len(items), 0) {
		return
	}
	if kind == "integer" {
		c.checkIntegerSet(items, path)
		return
	}
	complete := true
	for _, raw := range items {
		value, valid := raw.(string)
		if !valid || len(value) > MaxListSetValueBytes {
			complete, c.report.Partial = false, true
			continue
		}
		if !c.chargeWork(1+(len(value)+31)/32, len(value)) {
			return
		}
		if !utf8.ValidString(value) {
			complete, c.report.Partial = false, true
		}
	}
	if !complete || !c.chargeWork(1+len(items), 0) {
		return
	}
	seen := make(map[string]struct{}, len(items))
	duplicate := false
	for _, raw := range items {
		value := raw.(string) // The complete preflight above established the type.
		// Reserve lookup, possible insertion and equality before hashing. Even
		// after a duplicate, finish charging every item before emitting a hint.
		if !c.chargeWork(1+(3*len(value)+31)/32, 3*len(value)) {
			return
		}
		if _, exists := seen[value]; exists {
			duplicate = true
		} else {
			seen[value] = struct{}{}
		}
	}
	if c.ctx.Err() != nil {
		c.report.Partial, c.exhausted = true, true
		return
	}
	if duplicate {
		c.add(path, "list-set", "List entries repeat the advertised string value.")
	}
}

// Integers retain their strict decoder representation. In particular, an
// integral float64 is not coerced into this identity domain. Preflight every
// value before allocating the table; finish all accounting before any hint.
func (c *constraintChecker) checkIntegerSet(items []any, path string) {
	complete := true
	for _, raw := range items {
		if !c.chargeWork(1, 8) {
			return
		}
		if _, valid := raw.(int64); !valid {
			complete, c.report.Partial = false, true
		}
	}
	if !complete || !c.chargeWork(1+len(items), 0) {
		return
	}
	seen := make(map[int64]struct{}, len(items))
	duplicate := false
	for _, raw := range items {
		// Reserve lookup, possible insertion and equality, including after an
		// early duplicate. Integers consume eight bytes per operand.
		if !c.chargeWork(4, 24) {
			return
		}
		value := raw.(int64)
		if _, exists := seen[value]; exists {
			duplicate = true
		} else {
			seen[value] = struct{}{}
		}
	}
	if c.ctx.Err() != nil {
		c.report.Partial, c.exhausted = true, true
		return
	}
	if duplicate {
		c.add(path, "list-set", "List entries repeat the advertised integer value.")
	}
}
