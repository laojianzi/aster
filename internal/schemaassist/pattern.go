package schemaassist

import (
	"math/bits"
	"regexp"
	"regexp/syntax"
	"unicode"
	"unicode/utf8"
)

const (
	MaxPatternBytes      = 512
	MaxPatternInputBytes = 16 << 10
	MaxPatternDepth      = 32
	MaxPatternProgram    = 256
)

func (c *constraintChecker) checkPattern(n map[string]any, value any, path string, inspectValue bool) {
	raw, exists := n["pattern"]
	if !exists {
		return
	}
	pattern, valid := raw.(string)
	if !valid || len(pattern) > MaxPatternBytes {
		c.report.Partial = true
		return
	}
	// Charge before parsing. The byte/depth/grammar limits bound parser work;
	// the square also accounts for class sorting and common-prefix factoring.
	blocks := (len(pattern) + 15) / 16
	parseWork := 1 + blocks*blocks
	if !c.chargeWork(parseWork, len(pattern)) {
		return
	}
	if !utf8.ValidString(pattern) || !portablePattern(pattern) {
		c.report.Partial = true
		return
	}
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil || c.ctx.Err() != nil {
		c.report.Partial = true
		return
	}
	bound, valid := patternProgramBound(parsed)
	if !valid || bound+2 > MaxPatternProgram {
		c.report.Partial = true
		return
	}
	// Simplify can expand counted repetitions. Its complete instruction bound
	// is established before either compiler allocates that expansion. Public
	// syntax and regexp APIs deliberately compile twice; both are charged.
	if !c.chargeWork(parseWork+2*(bound+2), 0) {
		return
	}
	program, err := syntax.Compile(parsed.Simplify())
	if err != nil || len(program.Inst) > bound+2 || len(program.Inst) > MaxPatternProgram || c.ctx.Err() != nil {
		c.report.Partial = true
		return
	}
	expression, err := regexp.Compile(pattern)
	if err != nil || c.ctx.Err() != nil {
		c.report.Partial = true
		return
	}
	// Validate metadata even on nullable or wrong-type values. Pattern has no
	// applicability to non-strings, and must not invent a second type error.
	input, applicable := value.(string)
	if !inspectValue || !applicable {
		return
	}
	if len(input) > MaxPatternInputBytes {
		c.report.Partial = true
		return
	}
	weight := len(program.Inst)
	for _, inst := range program.Inst {
		// Rune-class lookup uses bounded binary search (or small linear cases).
		// Include that cost as well as every instruction; no match-prefix
		// shortcut is assumed when reserving the worst-case whole-input work.
		weight += 2*bits.Len(uint(len(inst.Rune))) + 2
	}
	work := ((len(input)+1)*weight + 63) / 64
	if !c.chargeWork(max(1, work), len(input)) {
		return
	}
	if !utf8.ValidString(input) || c.ctx.Err() != nil {
		c.report.Partial = true
		return
	}
	// Go guarantees linear matching for a fixed expression. Program/input
	// limits and the shared weighted allowance bound the remaining factor.
	// Cancellation is observed around this primitive, not inside its engine.
	matched := expression.MatchString(input)
	if c.ctx.Err() != nil {
		c.report.Partial = true
		return
	}
	if !matched {
		c.add(path, "pattern", "String does not match the supported advertised pattern.")
	}
}

// portablePattern admits a conservative ECMA-262 5.1 / Go intersection. Each
// consuming atom is a BMP character excluding UTF-16 surrogates, so matching
// valid UTF-8 input containing supplementary characters cannot disagree merely
// because Go advances runes while ECMA-262 5.1 advances UTF-16 code units.
// Unknown dialects stay incomplete even in an unused or zero-repeat branch.
func portablePattern(pattern string) bool {
	depth, atom := 0, false
	for i := 0; i < len(pattern); {
		r, width := utf8.DecodeRuneInString(pattern[i:])
		if !patternRune(r) {
			return false
		}
		switch r {
		case '\\':
			_, _, next, ok := patternEscape(pattern, i)
			if !ok {
				return false
			}
			i, atom = next, true
			continue
		case '[':
			next, ok := patternClass(pattern, i+1)
			if !ok {
				return false
			}
			i, atom = next, true
			continue
		case '(':
			depth++
			if depth > MaxPatternDepth {
				return false
			}
			if i+1 < len(pattern) && pattern[i+1] == '?' {
				if i+2 >= len(pattern) || pattern[i+2] != ':' {
					return false
				}
				i += 2
			}
			atom = false
		case ')':
			depth--
			if depth < 0 {
				return false
			}
			atom = true
		case '^', '$', '|':
			atom = false
		case '*', '+', '?', '{':
			// In particular, reject directly quantified assertions, which
			// some engines accept but ECMA-262's Atom grammar does not.
			if !atom {
				return false
			}
			if r == '{' {
				next, ok := patternRepeat(pattern, i+1)
				if !ok {
					return false
				}
				i = next
			} else {
				i += width
			}
			if i < len(pattern) && pattern[i] == '?' {
				i++ // Lazy preference changes no boolean match result.
			}
			atom = false
			continue
		case '.', ']', '}':
			return false
		default:
			atom = true
		}
		i += width
	}
	return depth == 0
}

func patternRune(r rune) bool {
	return r >= 0 && r <= 0xffff && (r < 0xd800 || r > 0xdfff)
}

// Return a scalar or a reviewed positive shorthand class, never an arbitrary
// identity escape, word boundary, octal/hex/Unicode escape or dialect extension.
func patternEscape(pattern string, i int) (r rune, scalar bool, next int, ok bool) {
	if i+1 >= len(pattern) {
		return
	}
	next, scalar, ok = i+2, true, true
	switch ch := pattern[i+1]; ch {
	case 't':
		r = '\t'
	case 'n':
		r = '\n'
	case 'r':
		r = '\r'
	case 'f':
		r = '\f'
	case 'v':
		r = '\v'
	case 'd', 'w':
		scalar = false
	case '\\', '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '^', '$', '|', '-', '/':
		r = rune(ch)
	default:
		ok = false
	}
	return
}

func patternClass(pattern string, start int) (int, bool) {
	if start >= len(pattern) || pattern[start] == '^' {
		return 0, false
	}
	type atom struct {
		value           rune
		scalar, rawDash bool
	}
	// At most MaxPatternBytes tokens; this allocation is after parse charging.
	atoms := make([]atom, 0, min(len(pattern)-start, MaxPatternBytes))
	for i := start; i < len(pattern); {
		r, width := utf8.DecodeRuneInString(pattern[i:])
		if r == ']' {
			if len(atoms) == 0 {
				return 0, false // Empty class has different Go parsing.
			}
			for j := 0; j < len(atoms); {
				if j+2 < len(atoms) && atoms[j+1].rawDash {
					lo, hi := atoms[j], atoms[j+2]
					// ECMA ranges require two single-character endpoints.
					// Checking overlap, not only endpoints, is essential:
					// a wide BMP range can consume an astral surrogate.
					if !lo.scalar || !hi.scalar || lo.value > hi.value ||
						(lo.value <= 0xdfff && hi.value >= 0xd800) {
						return 0, false
					}
					j += 3
				} else {
					j++
				}
			}
			return i + width, true
		}
		if r == '[' || !patternRune(r) {
			return 0, false
		}
		entry := atom{value: r, scalar: true, rawDash: r == '-'}
		if r == '\\' {
			value, scalar, next, ok := patternEscape(pattern, i)
			if !ok {
				return 0, false
			}
			entry, i = atom{value: value, scalar: scalar}, next
		} else {
			i += width
		}
		atoms = append(atoms, entry)
	}
	return 0, false
}

func patternRepeat(pattern string, start int) (int, bool) {
	read := func(i int) (int, int, bool) {
		first, value := i, 0
		for i < len(pattern) && pattern[i] >= '0' && pattern[i] <= '9' {
			value = value*10 + int(pattern[i]-'0')
			if value > 1000 {
				return 0, i, false
			}
			i++
		}
		return value, i, i > first && (i-first == 1 || pattern[first] != '0')
	}
	minimum, i, ok := read(start)
	if !ok || i >= len(pattern) {
		return 0, false
	}
	if pattern[i] == ',' {
		i++
		if i < len(pattern) && pattern[i] != '}' {
			var maximum int
			maximum, i, ok = read(i)
			if !ok || maximum < minimum {
				return 0, false
			}
		}
	}
	return i + 1, i < len(pattern) && pattern[i] == '}'
}

// Bound the complete expanded compiler output before Simplify, saturating every
// intermediate at the instruction cap. The compiler adds Fail and Match later.
// Nullable Star compiles as (sub+)? and can need two, not one, extra branches.
func patternProgramBound(re *syntax.Regexp) (int, bool) {
	nodes := 0
	var visit func(*syntax.Regexp, int) (int, bool)
	visit = func(node *syntax.Regexp, depth int) (int, bool) {
		nodes++
		if node == nil || depth > MaxPatternDepth*4 || nodes > MaxPatternBytes*2 {
			return 0, false
		}
		cost := 0
		for _, child := range node.Sub {
			part, ok := visit(child, depth+1)
			if !ok || part > MaxPatternProgram-cost {
				return 0, false
			}
			cost += part
		}
		switch node.Op {
		case syntax.OpNoMatch, syntax.OpEmptyMatch, syntax.OpBeginText, syntax.OpEndText:
			cost = 1
		case syntax.OpLiteral:
			for _, r := range node.Rune {
				if !patternRune(r) {
					return 0, false
				}
				if node.Flags&syntax.FoldCase != 0 {
					// The parser can compact a positive class such as [aA]
					// into a folded literal even without source flags.
					fold, count := unicode.SimpleFold(r), 0
					for fold != r {
						count++
						if count > 8 || !patternRune(fold) {
							return 0, false
						}
						fold = unicode.SimpleFold(fold)
					}
				}
			}
			cost = max(1, len(node.Rune))
		case syntax.OpCharClass:
			if len(node.Rune)%2 != 0 {
				return 0, false
			}
			for i := 0; i < len(node.Rune); i += 2 {
				lo, hi := node.Rune[i], node.Rune[i+1]
				if !patternRune(lo) || !patternRune(hi) || (lo <= 0xdfff && hi >= 0xd800) {
					return 0, false
				}
			}
			cost = 1
		case syntax.OpCapture, syntax.OpStar:
			cost += 2
		case syntax.OpPlus, syntax.OpQuest:
			cost++
		case syntax.OpConcat:
			cost = max(1, cost)
		case syntax.OpAlternate:
			cost += max(0, len(node.Sub)-1)
		case syntax.OpRepeat:
			if len(node.Sub) != 1 || node.Min < 0 || node.Max < -1 {
				return 0, false
			}
			copies, extra := node.Max, node.Max-node.Min
			if node.Max == -1 {
				copies, extra = max(1, node.Min), 1
				if node.Min == 0 {
					extra = 2
				}
			}
			if copies == 0 {
				cost = 1
			} else if copies < 0 || extra < 0 || extra > MaxPatternProgram || cost > (MaxPatternProgram-extra)/copies {
				return 0, false
			} else {
				cost = copies*cost + extra
			}
		default:
			return 0, false
		}
		return cost, cost <= MaxPatternProgram
	}
	return visit(re, 0)
}
