package schemaassist

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxEnumValues      = 128
	MaxNumberBytes     = 512
	MaxConstraintBytes = 8 << 20
)

// decimal keeps a bounded coefficient and a decimal power. Comparison never
// expands 10^power, including when a hostile schema uses an enormous exponent.
// Float inputs use their JSON serialization, not their exact binary fraction.
// Precision already lost by the manifest decoder cannot be recovered here.
type decimal struct {
	sign   int
	digits string
	power  int64
}

func number(v any) bool {
	switch v.(type) {
	case json.Number, float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

func decimalValue(v any) (decimal, bool) {
	var s string
	switch n := v.(type) {
	case json.Number:
		s = string(n)
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return decimal{}, false
		}
		s = strconv.FormatFloat(n, 'g', -1, 64)
	case float32:
		if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) {
			return decimal{}, false
		}
		s = strconv.FormatFloat(float64(n), 'g', -1, 32)
	case int:
		s = strconv.FormatInt(int64(n), 10)
	case int8:
		s = strconv.FormatInt(int64(n), 10)
	case int16:
		s = strconv.FormatInt(int64(n), 10)
	case int32:
		s = strconv.FormatInt(int64(n), 10)
	case int64:
		s = strconv.FormatInt(n, 10)
	case uint:
		s = strconv.FormatUint(uint64(n), 10)
	case uint8:
		s = strconv.FormatUint(uint64(n), 10)
	case uint16:
		s = strconv.FormatUint(uint64(n), 10)
	case uint32:
		s = strconv.FormatUint(uint64(n), 10)
	case uint64:
		s = strconv.FormatUint(n, 10)
	default:
		return decimal{}, false
	}
	if len(s) == 0 || len(s) > MaxNumberBytes || strings.TrimSpace(s) != s || (s[0] != '-' && (s[0] < '0' || s[0] > '9')) || !json.Valid([]byte(s)) {
		return decimal{}, false
	}
	out := decimal{sign: 1}
	if s[0] == '-' {
		out.sign, s = -1, s[1:]
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		var err error
		out.power, err = strconv.ParseInt(s[i+1:], 10, 32)
		if err != nil {
			return decimal{}, false
		}
		s = s[:i]
	}
	if i := strings.IndexByte(s, '.'); i >= 0 {
		out.power -= int64(len(s) - i - 1)
		s = s[:i] + s[i+1:]
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return decimal{}, true
	}
	out.digits = strings.TrimRight(s, "0")
	out.power += int64(len(s) - len(out.digits))
	return out, true
}

func (a decimal) compare(b decimal) int {
	if a.sign != b.sign {
		if a.sign < b.sign {
			return -1
		}
		return 1
	}
	if a.sign == 0 {
		return 0
	}
	am, bm := int64(len(a.digits))+a.power, int64(len(b.digits))+b.power
	if am < bm {
		return -a.sign
	}
	if am > bm {
		return a.sign
	}
	for i := 0; i < max(len(a.digits), len(b.digits)); i++ {
		ac, bc := byte('0'), byte('0')
		if i < len(a.digits) {
			ac = a.digits[i]
		}
		if i < len(b.digits) {
			bc = b.digits[i]
		}
		if ac < bc {
			return -a.sign
		}
		if ac > bc {
			return a.sign
		}
	}
	return 0
}

type scalar struct {
	kind   byte
	text   string
	number decimal
}

type constraintChecker struct {
	ctx       context.Context
	report    *Report
	add       func(string, string, string)
	bytes     int
	exhausted bool
}

func (c *constraintChecker) charge(size int) bool {
	return c.chargeWork(1, size)
}

// Arithmetic, enum/pattern/format inspection and traversal share one allowance.
// Primitive input/program limits and weighted work prevent repeated bounded
// operations from monopolizing one inspection.
func (c *constraintChecker) chargeWork(units, size int) bool {
	if c.ctx.Err() != nil || c.exhausted || units < 1 || size < 0 || units > MaxWork-c.report.Checked || size > MaxConstraintBytes-c.bytes {
		c.report.Partial, c.exhausted = true, true
		return false
	}
	c.report.Checked += units
	c.bytes += size
	return true
}

func (c *constraintChecker) scalar(v any) (scalar, bool) {
	size := 32 // Bounds any primitive Go numeric JSON representation.
	switch n := v.(type) {
	case string:
		size = len(n)
	case json.Number:
		size = len(n)
	}
	if !c.charge(size) {
		return scalar{}, false
	}
	switch n := v.(type) {
	case nil:
		return scalar{kind: '0'}, true
	case bool:
		if n {
			return scalar{kind: 'b', text: "true"}, true
		}
		return scalar{kind: 'b', text: "false"}, true
	case string:
		return scalar{kind: 's', text: n}, utf8.ValidString(n)
	default:
		d, ok := decimalValue(v)
		return scalar{kind: 'n', number: d}, ok
	}
}

func (a scalar) equal(b scalar) bool {
	if a.kind != b.kind {
		return false
	}
	if a.kind == 'n' {
		return a.number.compare(b.number) == 0
	}
	return a.text == b.text
}

func (c *constraintChecker) checkEnum(n map[string]any, value any, path string, inspectValue bool) {
	raw, exists := n["enum"]
	if !exists {
		return
	}
	values, ok := raw.([]any)
	if !ok || len(values) == 0 || len(values) > MaxEnumValues {
		c.report.Partial = true
		return
	}
	structured := inspectValue && enumContainer(value)
	for _, candidate := range values {
		if enumContainer(candidate) {
			structured = true
			break
		}
	}
	if structured {
		c.checkStructuredEnum(values, value, path, inspectValue)
		return
	}
	// Preserve the established scalar path, including its byte/work accounting.
	// The additional per-value limits only apply to structured comparisons.
	var want scalar
	if inspectValue {
		want, ok = c.scalar(value)
		if !ok {
			c.report.Partial = true
			return
		}
	}
	matched, complete := false, true
	for _, v := range values {
		candidate, valid := c.scalar(v)
		if !valid {
			complete, c.report.Partial = false, true
		}
		if c.exhausted {
			return
		}
		if inspectValue && valid && want.equal(candidate) {
			matched = true
		}
	}
	// A malformed entry makes a non-match uncertain. Do not turn a partial
	// scan (including a late exhausted budget) into a constraint hint.
	if inspectValue && complete && !matched {
		c.add(path, "enum", "Value is outside the advertised scalar choices.")
	}
}

func (c *constraintChecker) bound(raw any, count bool) (decimal, bool) {
	size := 32
	if n, ok := raw.(json.Number); ok {
		size = len(n)
	}
	if !c.charge(size) {
		return decimal{}, false
	}
	d, ok := decimalValue(raw)
	if !ok || (count && (d.sign < 0 || (d.sign != 0 && d.power < 0))) {
		c.report.Partial = true
		return decimal{}, false
	}
	return d, true
}

func (c *constraintChecker) checkNumber(n map[string]any, value any, path string, inspectValue bool) {
	instance, applicable := decimalValue(value)
	applicable = applicable && inspectValue
	for _, key := range []string{"minimum", "maximum"} {
		raw, exists := n[key]
		exclusiveKey := "exclusiveMinimum"
		if key == "maximum" {
			exclusiveKey = "exclusiveMaximum"
		}
		exclusive := false
		if flag, present := n[exclusiveKey]; present {
			var ok bool
			exclusive, ok = flag.(bool)
			if !ok || !exists {
				c.report.Partial = true
				continue
			}
		}
		if !exists {
			continue
		}
		limit, valid := c.bound(raw, false)
		if !valid || !applicable {
			continue
		}
		comparison := instance.compare(limit)
		violated := comparison < 0
		message := "Value is below the advertised lower bound."
		if key == "maximum" {
			violated = comparison > 0
			message = "Value is above the advertised upper bound."
		}
		if exclusive && comparison == 0 {
			violated = true
			message = "Value equals an excluded bound."
		}
		if violated {
			c.add(path, key, message)
		}
	}
}

func (c *constraintChecker) checkSize(n map[string]any, path, suffix string, size int, applicable bool) {
	instance, _ := decimalValue(size)
	for _, prefix := range []string{"min", "max"} {
		key := prefix + suffix
		raw, exists := n[key]
		if !exists {
			continue
		}
		limit, valid := c.bound(raw, true)
		if !valid || !applicable {
			continue
		}
		comparison := instance.compare(limit)
		if (prefix == "min" && comparison < 0) || (prefix == "max" && comparison > 0) {
			c.add(path, key, "Size is outside the advertised bound.")
		}
	}
}

func (c *constraintChecker) inspectKeywords(n map[string]any) {
	// Unknown validation keywords and Kubernetes/CEL/list semantics must not
	// disappear behind a successful scan of the supported subset. Display
	// incompleteness once in the report, without flooding field diagnostics.
	for key := range n {
		switch key {
		case "title", "description", "default", "example", "externalDocs", "deprecated", "readOnly", "writeOnly",
			"type", "nullable", "properties", "required", "additionalProperties", "items", "$ref",
			"allOf", "oneOf", "anyOf", "not", "enum", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
			"minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties", "pattern", "format",
			"x-kubernetes-int-or-string", "x-kubernetes-preserve-unknown-fields", "x-kubernetes-embedded-resource",
			"x-kubernetes-group-version-kind", "x-kubernetes-patch-strategy", "x-kubernetes-patch-merge-key",
			"x-kubernetes-list-type", "x-kubernetes-list-map-keys":
		default:
			c.report.Partial = true
		}
	}
}

func (c *constraintChecker) check(n map[string]any, value any, path string, inspectValue bool) {
	if c.exhausted {
		return
	}
	c.checkEnum(n, value, path, inspectValue)
	c.checkNumber(n, value, path, inspectValue)
	c.checkMultipleOf(n, value, path, inspectValue)
	c.checkPattern(n, value, path, inspectValue)
	c.checkFormat(n, value, path, inspectValue)
	length, stringValue := 0, false
	if s, ok := value.(string); ok && inspectValue {
		_, lower := n["minLength"]
		_, upper := n["maxLength"]
		if lower || upper {
			if c.charge(len(s)) && utf8.ValidString(s) {
				length, stringValue = utf8.RuneCountInString(s), true
			} else {
				c.report.Partial = true
			}
		}
	}
	c.checkSize(n, path, "Length", length, stringValue)
	items, arrayValue := value.([]any)
	c.checkSize(n, path, "Items", len(items), arrayValue && inspectValue)
	properties, objectValue := value.(map[string]any)
	c.checkSize(n, path, "Properties", len(properties), objectValue && inspectValue)
}
