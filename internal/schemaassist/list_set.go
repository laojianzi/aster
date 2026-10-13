package schemaassist

import "unicode/utf8"

const (
	MaxListSetItems      = 256
	MaxListSetValueBytes = 16 << 10
)

// Only explicit, non-nullable string items have an inspected set identity here.
// Server defaulting, numeric runtime types and compound-value serialization are
// separate contracts; the structured-enum comparator is not a set comparator.
func (c *constraintChecker) listSetSchema(d *Document, node map[string]any) bool {
	_, mapKeys := node["x-kubernetes-list-map-keys"]
	_, uninspected := node["x-aster-uninspected-reference"]
	if node["x-kubernetes-list-type"] != "set" || node["type"] != "array" || nodeType(node) != "array" || mapKeys || uninspected || composite(node) {
		c.report.Partial = true
		return false
	}
	item, valid := c.listIdentitySchema(d, node["items"])
	if !valid {
		return false
	}
	if item["type"] != "string" || nodeType(item) != "string" || !listIdentityNonnullable(item) {
		c.report.Partial = true
		return false
	}
	return true
}

func (c *constraintChecker) checkListSet(d *Document, node map[string]any, value any, path string, inspectValue bool) {
	if c.exhausted || !c.listSetSchema(d, node) || !inspectValue {
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
