package schemaassist

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxFormatNameBytes  = 64
	MaxFormatInputBytes = 16 << 10
)

func (c *constraintChecker) checkFormat(n map[string]any, value any, path string, inspectValue bool) {
	raw, exists := n["format"]
	if !exists {
		return
	}
	name, valid := raw.(string)
	if !valid || len(name) > MaxFormatNameBytes {
		c.report.Partial = true
		return
	}
	// Reserve the bounded name scan and normalization before allocating or
	// inspecting UTF-8. Kubernetes removes ASCII hyphens only: names remain
	// case-sensitive, and neither spaces nor Unicode variants are normalized.
	if !c.chargeWork(1+(len(name)+15)/16, len(name)) {
		return
	}
	if !utf8.ValidString(name) {
		c.report.Partial = true
		return
	}
	name = strings.ReplaceAll(name, "-", "")
	switch name {
	case "date", "byte", "uuid":
	default:
		// Unknown and explicitly empty metadata are not a value violation.
		// Keep declared but uninspected formats visible as incomplete.
		c.report.Partial = true
		return
	}
	// Metadata is checked even on nullable and wrong-type instances. Only a
	// string has format applicability; do not add a secondary type error.
	input, applicable := value.(string)
	if !inspectValue || !applicable {
		return
	}
	if len(input) > MaxFormatInputBytes {
		c.report.Partial = true
		return
	}
	// Both UTF-8 inspection and the linear validator are bounded and charged
	// against the same work/byte allowance as traversal, pattern and numbers.
	if !c.chargeWork(1+(len(input)+31)/32, len(input)) {
		return
	}
	if !utf8.ValidString(input) || c.ctx.Err() != nil {
		c.report.Partial = true
		return
	}
	matched := false
	switch name {
	case "date":
		if len(input) == 10 {
			_, err := time.Parse("2006-01-02", input)
			matched = err == nil
		}
	case "byte":
		matched = formatByte(input)
	case "uuid":
		matched = formatUUID(input)
	}
	// Cancellation is observed around bounded primitives, not by interrupting
	// time.Parse or the scans internally. Never render their inputs or errors.
	if c.ctx.Err() != nil {
		c.report.Partial = true
		return
	}
	if !matched {
		c.add(path, "format", "String does not match the supported advertised format.")
	}
}

// Match the Kubernetes string validator's nonempty base64 grammar. It checks
// shape, not canonical padding bits: AB== and AAB= are accepted. A decoder
// would additionally accept CR/LF (and empty input), or reject those padding
// bits in strict mode. No decoded payload is needed or allocated here.
func formatByte(input string) bool {
	if len(input) == 0 || len(input)%4 != 0 {
		return false
	}
	end := len(input)
	if input[end-1] == '=' {
		end--
		if input[end-1] == '=' {
			end--
		}
	}
	for i := 0; i < end; i++ {
		ch := input[i]
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '+' || ch == '/') {
			return false
		}
	}
	return true
}

// Kubernetes's generic uuid format permits each separator independently and
// does not restrict version or variant bits. Reject decorations or misplaced
// separators rather than accepting an unrelated UUID parser's extra forms.
func formatUUID(input string) bool {
	if len(input) < 32 || len(input) > 36 {
		return false
	}
	i := 0
	for group, width := range [...]int{8, 4, 4, 4, 12} {
		if width > len(input)-i {
			return false
		}
		for end := i + width; i < end; i++ {
			ch := input[i]
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
				return false
			}
		}
		if group < 4 && i < len(input) && input[i] == '-' {
			i++
		}
	}
	return i == len(input)
}
