package schemaassist

import "unicode/utf8"

const (
	MaxListMapKeys       = 8
	MaxListMapItems      = 256
	MaxListMapNameBytes  = 256
	MaxListMapValueBytes = 16 << 10
)

// listIdentitySchema reuses the existing resolver, including its exact local-ref,
// wrapper, cycle and depth rules, with shared-budget accounting for this extra
// metadata inspection. A wrapper's uninspected semantics are never flattened.
func (c *constraintChecker) listIdentitySchema(d *Document, raw any) (map[string]any, bool) {
	node, valid := raw.(map[string]any)
	if !valid || node == nil {
		c.report.Partial = true
		return nil, false
	}
	node, err := d.resolveWithBudget(c.ctx, node, c.chargeWork)
	if err != nil || composite(node) {
		c.report.Partial = true
		return nil, false
	}
	if _, uninspected := node["x-aster-uninspected-reference"]; uninspected {
		c.report.Partial = true
		return nil, false
	}
	return node, true
}

func listIdentityNonnullable(node map[string]any) bool {
	value, exists := node["nullable"]
	return !exists || value == false
}

type listMapKey struct {
	name    string
	integer bool
	boolean bool
}

// Every component's type is fixed by the completely inspected key schema.
// Keep signed integers as integers: neither float conversion nor serialization
// is needed, and no integer can collide with an empty string component.
type listMapKeyValue struct {
	text    string
	integer int64
	boolean bool
}

// listMapKeys validates the complete advertised identity before examining the
// draft. Non-key properties are not part of identity; their independent Schema
// constraints are still inspected by the ordinary document traversal.
func (c *constraintChecker) listMapKeys(d *Document, node map[string]any) ([]listMapKey, bool) {
	if node["x-kubernetes-list-type"] != "map" || nodeType(node) != "array" || node["type"] != "array" || composite(node) {
		c.report.Partial = true
		return nil, false
	}
	if _, uninspected := node["x-aster-uninspected-reference"]; uninspected {
		c.report.Partial = true
		return nil, false
	}
	rawKeys, valid := node["x-kubernetes-list-map-keys"].([]any)
	if !valid || len(rawKeys) == 0 || len(rawKeys) > MaxListMapKeys {
		c.report.Partial = true
		return nil, false
	}
	if !c.chargeWork(1+2*len(rawKeys), 0) {
		return nil, false
	}
	keys := make([]listMapKey, len(rawKeys))
	positions := make(map[string]int, len(rawKeys))
	for i, raw := range rawKeys {
		key, valid := raw.(string)
		if !valid || len(key) == 0 || len(key) > MaxListMapNameBytes {
			c.report.Partial = true
			return nil, false
		}
		// Pay for UTF-8 validation and both key-table lookups/insertions.
		if !c.chargeWork(3, 3*len(key)) {
			return nil, false
		}
		if !utf8.ValidString(key) {
			c.report.Partial = true
			return nil, false
		}
		if _, duplicate := positions[key]; duplicate {
			c.report.Partial = true
			return nil, false
		}
		keys[i].name, positions[key] = key, i
	}
	item, valid := c.listIdentitySchema(d, node["items"])
	if !valid {
		return nil, false
	}
	if nodeType(item) != "object" || item["type"] != "object" || !listIdentityNonnullable(item) {
		c.report.Partial = true
		return nil, false
	}
	required, valid := item["required"].([]any)
	properties, propertiesValid := item["properties"].(map[string]any)
	if !valid || !propertiesValid {
		c.report.Partial = true
		return nil, false
	}
	if !c.chargeWork(1+len(required), 0) {
		return nil, false
	}
	var found [MaxListMapKeys]bool
	for _, raw := range required {
		name, valid := raw.(string)
		if !valid {
			c.report.Partial = true
			return nil, false
		}
		if !c.chargeWork(2, 2*len(name)) {
			return nil, false
		}
		if !utf8.ValidString(name) {
			c.report.Partial = true
			return nil, false
		}
		if index, isKey := positions[name]; isKey {
			found[index] = true
		}
	}
	for i, descriptor := range keys {
		key := descriptor.name
		if !found[i] {
			c.report.Partial = true
			return nil, false
		}
		if !c.chargeWork(1, len(key)) {
			return nil, false
		}
		keySchema, valid := c.listIdentitySchema(d, properties[key])
		if !valid {
			return nil, false
		}
		kind := nodeType(keySchema)
		if (kind != "string" && kind != "integer" && kind != "boolean") || keySchema["type"] != kind || !listIdentityNonnullable(keySchema) {
			c.report.Partial = true
			return nil, false
		}
		keys[i].integer = kind == "integer"
		keys[i].boolean = kind == "boolean"
	}
	return keys, true
}

func (c *constraintChecker) checkListMap(d *Document, node map[string]any, value any, path string, inspectValue bool) {
	_, hasType := node["x-kubernetes-list-type"]
	_, hasKeys := node["x-kubernetes-list-map-keys"]
	if c.exhausted || (!hasType && !hasKeys) {
		return
	}
	keys, valid := c.listMapKeys(d, node)
	if !valid || !inspectValue {
		return
	}
	// Metadata remains checked on wrong-type and nullable-null values. Only an
	// actual decoded array has applicable identities; typed nil is not JSON [].
	items, applicable := value.([]any)
	if !applicable {
		return
	}
	if items == nil || len(items) > MaxListMapItems {
		c.report.Partial = true
		return
	}
	if !c.chargeWork(1+len(items)*MaxListMapKeys, 0) {
		return
	}
	// A fixed tuple preserves component boundaries, including empty strings,
	// separators and embedded NULs. No serialized key or payload is allocated.
	identities := make([][MaxListMapKeys]listMapKeyValue, len(items))
	identityBytes := make([]int, len(items))
	complete := true
	for i, raw := range items {
		item, object := raw.(map[string]any)
		if !object || item == nil {
			complete, c.report.Partial = false, true
			continue
		}
		for j, descriptor := range keys {
			key := descriptor.name
			if !c.chargeWork(1, len(key)) {
				return
			}
			if descriptor.integer {
				// Unstructured's strict decoder preserves integer tokens as
				// int64. Other runtime numeric representations are not this
				// identity contract, even when mathematically integral.
				integer, valid := item[key].(int64)
				if !valid {
					complete, c.report.Partial = false, true
					continue
				}
				if !c.chargeWork(1, 8) {
					return
				}
				identities[i][j].integer = integer
				identityBytes[i] += 8
				continue
			}
			if descriptor.boolean {
				boolean, valid := item[key].(bool)
				if !valid {
					complete, c.report.Partial = false, true
					continue
				}
				if !c.chargeWork(1, 1) {
					return
				}
				identities[i][j].boolean = boolean
				identityBytes[i]++
				continue
			}
			text, stringValue := item[key].(string)
			if !stringValue || len(text) > MaxListMapValueBytes {
				complete, c.report.Partial = false, true
				continue
			}
			if !c.chargeWork(1+(len(text)+31)/32, len(text)) {
				return
			}
			if !utf8.ValidString(text) {
				complete, c.report.Partial = false, true
				continue
			}
			identities[i][j].text = text
			identityBytes[i] += len(text)
		}
	}
	if !complete || !c.chargeWork(1+len(items)*MaxListMapKeys, 0) {
		return
	}
	seen := make(map[[MaxListMapKeys]listMapKeyValue]struct{}, len(items))
	duplicate := false
	for i, identity := range identities {
		// Reserve lookup, possible insertion and tuple comparison before hashing.
		// Pay the same complete cost after an early duplicate so a late budget
		// failure cannot masquerade as a finished identity inspection.
		if !c.chargeWork(1+3*MaxListMapKeys, 3*identityBytes[i]) {
			return
		}
		if _, exists := seen[identity]; exists {
			duplicate = true
		} else {
			seen[identity] = struct{}{}
		}
	}
	if c.ctx.Err() != nil {
		c.report.Partial, c.exhausted = true, true
		return
	}
	if duplicate {
		message := "List entries repeat the advertised string-key identity."
		for _, key := range keys {
			if key.integer || key.boolean {
				message = "List entries repeat the advertised key identity."
				break
			}
		}
		c.add(path, "list-map-keys", message)
	}
}
