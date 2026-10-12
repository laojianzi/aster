package schemaassist

import (
	"context"
	"unicode/utf8"
)

const (
	// Structured enum limits apply separately to each alternative and instance.
	// The root is depth zero; nodes count JSON values, not object keys.
	MaxEnumValueDepth = 32
	MaxEnumValueNodes = 1024
	MaxEnumTextBytes  = 16 << 10 // Each decoded object key or string value.
)

// enumValue owns only a bounded, validated JSON value tree. Numeric leaves use
// the same exact decoded-decimal contract as scalar enum and numeric bounds.
// Costs cover a complete comparison, including object-key lookup and leaf text.
// Paying the two trees' full costs before comparing makes shared budget use
// independent of the first mismatching key in Go's unordered map iteration.
type enumValue struct {
	value        scalar
	fields       map[string]enumValue
	items        []enumValue
	compareWork  int
	compareBytes int
}

func enumContainer(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}

func (c *constraintChecker) checkStructuredEnum(values []any, value any, path string, inspectValue bool) {
	// The outer enum shape/length was checked before selecting this path. Charge
	// its scan and bounded result slots before allocating them.
	if !c.chargeWork(1+len(values), 0) {
		return
	}
	alternatives := make([]enumValue, len(values))
	complete := true
	for i, candidate := range values {
		nodes := 0
		var valid bool
		alternatives[i], valid = c.enumValue(candidate, 0, &nodes)
		if !valid {
			complete, c.report.Partial = false, true
		}
		if c.exhausted {
			return
		}
	}
	// Metadata must be complete even after an apparent match, a known type
	// error, or nullable null. Wrong-type instances themselves are not scanned.
	if !complete || !inspectValue {
		return
	}
	nodes := 0
	instance, valid := c.enumValue(value, 0, &nodes)
	if !valid {
		c.report.Partial = true
		return
	}
	for _, candidate := range alternatives {
		if !c.chargeWork(instance.compareWork+candidate.compareWork, instance.compareBytes+candidate.compareBytes) {
			return
		}
		matched, inspected := instance.equal(c.ctx, candidate)
		if !inspected || c.ctx.Err() != nil {
			c.report.Partial, c.exhausted = true, true
			return
		}
		if matched {
			return // All alternatives were already validated in full.
		}
	}
	c.add(path, "enum", "Value is outside the advertised choices.")
}

func (c *constraintChecker) enumValue(value any, depth int, nodes *int) (enumValue, bool) {
	if depth > MaxEnumValueDepth || *nodes >= MaxEnumValueNodes {
		return enumValue{}, false
	}
	*nodes++
	switch v := value.(type) {
	case map[string]any:
		// Actual decoded null is a nil interface. A typed nil container is not
		// silently treated as either null or an empty JSON container.
		if v == nil || len(v) > MaxEnumValueNodes-*nodes {
			return enumValue{}, false
		}
		if !c.chargeWork(1+len(v), 0) {
			return enumValue{}, false
		}
		out := enumValue{value: scalar{kind: 'o'}, fields: make(map[string]enumValue, len(v)), compareWork: 1 + len(v)}
		complete := true
		for key, child := range v {
			validKey := len(key) <= MaxEnumTextBytes
			if validKey {
				// Pay for both the UTF-8 scan and hash insertion before either
				// operation. The next comparison pays separately for lookup.
				if !c.chargeWork(2, 2*len(key)) {
					return enumValue{}, false
				}
				validKey = utf8.ValidString(key)
			}
			normalized, validChild := c.enumValue(child, depth+1, nodes)
			if c.exhausted {
				return enumValue{}, false
			}
			if !validKey || !validChild {
				complete = false
				continue
			}
			out.fields[key] = normalized
			out.compareWork += normalized.compareWork
			out.compareBytes += len(key) + normalized.compareBytes
		}
		return out, complete
	case []any:
		if v == nil || len(v) > MaxEnumValueNodes-*nodes {
			return enumValue{}, false
		}
		if !c.chargeWork(1+len(v), 0) {
			return enumValue{}, false
		}
		out := enumValue{value: scalar{kind: 'a'}, items: make([]enumValue, len(v)), compareWork: 1}
		complete := true
		for i, child := range v {
			normalized, validChild := c.enumValue(child, depth+1, nodes)
			if c.exhausted {
				return enumValue{}, false
			}
			if !validChild {
				complete = false
				continue
			}
			out.items[i] = normalized
			out.compareWork += normalized.compareWork
			out.compareBytes += normalized.compareBytes
		}
		return out, complete
	default:
		if s, ok := value.(string); ok && len(s) > MaxEnumTextBytes {
			return enumValue{}, false
		}
		leaf, valid := c.scalar(value)
		if !valid {
			return enumValue{}, false
		}
		size := len(leaf.text)
		if leaf.kind == 'n' {
			size = len(leaf.number.digits)
		}
		return enumValue{value: leaf, compareWork: 1, compareBytes: size}, true
	}
}

// equal only receives validated bounded trees after their full comparison cost
// was charged. Context checks still occur at each node; no source is mutated,
// sorted, stringified, or coerced to another JSON kind.
func (a enumValue) equal(ctx context.Context, b enumValue) (bool, bool) {
	if ctx.Err() != nil {
		return false, false
	}
	if a.value.kind != b.value.kind {
		return false, true
	}
	switch a.value.kind {
	case 'o':
		if len(a.fields) != len(b.fields) {
			return false, true
		}
		for key, child := range a.fields {
			other, present := b.fields[key]
			if !present {
				return false, true
			}
			if equal, complete := child.equal(ctx, other); !equal || !complete {
				return equal, complete
			}
		}
	case 'a':
		if len(a.items) != len(b.items) {
			return false, true
		}
		for i, child := range a.items {
			if equal, complete := child.equal(ctx, b.items[i]); !equal || !complete {
				return equal, complete
			}
		}
	default:
		return a.value.equal(b.value), true
	}
	return true, true
}
