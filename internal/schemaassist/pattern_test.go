package schemaassist

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"regexp/syntax"
	"strings"
	"sync"
	"testing"
)

func patternProperty(t testing.TB, pattern any) string {
	t.Helper()
	return constraintEncode(t, map[string]any{"type": "string", "pattern": pattern})
}

func TestSchemaPatternPortableMatching(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, value string
		matched              bool
	}{
		{"literal-unanchored", "ok", "prefix-ok-suffix", true},
		{"literal-case-sensitive", "ok", "OK", false},
		{"literal-absent", "ok", "private-value", false},
		{"empty-pattern", "", "中文🚢\n", true},
		{"empty-pattern-empty-value", "", "", true},
		{"empty-value-mismatch", "a", "", false},
		{"empty-anchors", "^$", "", true},
		{"empty-anchors-reject-line-feed", "^$", "\n", false},
		{"start-anchor", "^ok", "okay", true},
		{"start-anchor-mismatch", "^ok", "xok", false},
		{"end-anchor", "ok$", "xok", true},
		{"end-anchor-line-feed", "ok$", "ok\n", false},
		{"end-anchor-carriage-return", "ok$", "ok\r", false},
		{"end-anchor-line-separator", "ok$", "ok\u2028", false},
		{"end-anchor-paragraph-separator", "ok$", "ok\u2029", false},
		{"anchors-not-multiline", "^ok$", "before\nok\nafter", false},
		{"literal-across-lines", "o\\nk", "before\no\nk\nafter", true},
		{"literal-not-across-lines", "ok", "o\nk", false},
		{"ascii-positive-class", "^[a-z][a-z0-9-]*$", "native-fixed", true},
		{"ascii-class-rejects-uppercase", "^[a-z][a-z0-9-]*$", "Native", false},
		{"ascii-class-rejects-astral", "^[a-z]+$", "a🚢b", false},
		{"class-literal-dash-at-start", "^[-ab]+$", "-aba-", true},
		{"class-literal-dash-at-end", "^[ab-]+$", "ab-a", true},
		{"class-escaped-dash", "^[a\\-z]+$", "a-z", true},
		{"class-range-and-literal-dash", "^[a-c-e]+$", "ace-", true},
		{"class-literal-caret", "^[a^]+$", "a^", true},
		{"class-literal-dot", "^[.]+$", "..", true},
		{"class-escaped-bracket", "^[\\]]+$", "]]", true},
		{"class-positive-digit-members", "^[\\dA]+$", "A029", true},
		{"class-positive-word-members", "^[\\w-]+$", "A_9-", true},
		{"class-digits-do-not-match-unicode-digits", "^\\d+$", "１２", false},
		{"class-words-remain-ascii", "^\\w+$", "船", false},
		{"class-parser-folded-literal", "^[aA]+$", "aAa", true},
		{"class-kelvin-not-implied", "^[kK]+$", "\u212a", false},
		{"class-explicit-kelvin-orbit", "^[kK\u212a]+$", "kK\u212a", true},
		{"bmp-literal-with-astral-neighbors", "船", "🚢中文船🚢", true},
		{"bmp-literal-missing-with-astral", "船", "🚢中文🚢", false},
		{"bmp-range", "^[一-龥]+$", "中文船", true},
		{"bmp-range-astral-rejected", "^[一-龥]+$", "中文🚢", false},
		{"literal-no-normalization", "^é$", "e\u0301", false},
		{"combining-sequence", "^e\u0301$", "e\u0301", true},
		{"replacement-character-is-a-valid-literal", "^\ufffd$", "\ufffd", true},
		{"group-alternation", "^(ab|cd)+$", "abcdab", true},
		{"noncapturing-group", "^(?:a|bc){2}$", "abc", true},
		{"alternative-precedence", "^a|b$", "ax", true},
		{"empty-alternative", "a|", "🚢", true},
		{"optional", "^ab?c$", "ac", true},
		{"lazy-optional", "^ab??c$", "abc", true},
		{"lazy-star", "^a*?$", "aaa", true},
		{"lazy-plus", "^a+?$", "aaa", true},
		{"lazy-finite-repeat", "^a{1,3}?$", "aaa", true},
		{"repeat-zero", "^a{0}$", "", true},
		{"repeat-exact", "^a{3}$", "aaa", true},
		{"repeat-exact-mismatch", "^a{3}$", "aa", false},
		{"repeat-bounded", "^a{2,4}$", "aaaa", true},
		{"repeat-above-bounded", "^a{2,4}$", "aaaaa", false},
		{"repeat-open", "^a{2,}$", "aaaaa", true},
		{"repeat-open-zero", "^a{0,}$", "", true},
		{"nullable-star", "^(a?)*$", "aaaa", true},
		{"nullable-star-reject", "^(a?)*$", "aaaab", false},
		{"empty-assertion-group-star", "(?:^)*", "🚢", true},
		{"control-escapes", "^\\t\\n\\r\\f\\v$", "\t\n\r\f\v", true},
		{"escaped-metacharacters", "^\\.\\*\\+\\?\\(\\)\\[\\]\\{\\}\\^\\$\\|\\-\\/\\\\$", ".*+?()[]{}^$|-/\\", true},
		{"literal-line-terminators", "\r\u2028\u2029", "x\r\u2028\u2029y", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := constraintCheck(t, patternProperty(t, tc.pattern), tc.value)
			codes := []string{}
			if !tc.matched {
				codes = append(codes, "pattern")
			}
			constraintWant(t, r, false, codes...)
			for _, diagnostic := range r.Diagnostics {
				if diagnostic.Pointer != "/value" {
					t.Fatal("pattern diagnostic lost the original field pointer")
				}
			}
		})
	}
}

func TestSchemaPatternMatchesIndependentFiniteLanguages(t *testing.T) {
	// Enumerate finite languages directly from their definition. Neither the
	// language oracle nor substring membership uses any regular expression
	// parser, compiler or matcher.
	words := func(alphabet []string, lower, upper int) []string {
		all, level := []string{}, []string{""}
		for length := 0; length <= upper; length++ {
			if length >= lower {
				all = append(all, level...)
			}
			next := []string{}
			for _, prefix := range level {
				for _, atom := range alphabet {
					next = append(next, prefix+atom)
				}
			}
			level = next
		}
		return all
	}
	for _, tc := range []struct {
		pattern  string
		language []string
		anchored bool
	}{
		{"^(a|bb){0,3}$", words([]string{"a", "bb"}, 0, 3), true},
		{"^[ab]{2,4}$", words([]string{"a", "b"}, 2, 4), true},
		{"^(?:船|é){1,2}$", words([]string{"船", "é"}, 1, 2), true},
		{"^(a?){0,3}$", words([]string{"a"}, 0, 3), true},
		{"^(?:ab|){0,3}$", words([]string{"ab"}, 0, 3), true},
		{"(?:ab|船){1,2}", words([]string{"ab", "船"}, 1, 2), false},
	} {
		inputs := words([]string{"a", "b"}, 0, 5)
		inputs = append(inputs, "", "🚢", "船", "é", "e\u0301", "a\n", "a\r", "\u212a")
		for _, word := range tc.language {
			inputs = append(inputs, word, "🚢"+word+"🚢", "\n"+word+"\n", word+"x")
		}
		d, _ := constraintDocument(t, patternProperty(t, tc.pattern))
		for _, input := range inputs {
			want := false
			for _, word := range tc.language {
				if (tc.anchored && input == word) || (!tc.anchored && strings.Contains(input, word)) {
					want = true
				}
			}
			r, err := d.Check(context.Background(), constraintObject(input))
			if err != nil || r.Partial || (len(r.Diagnostics) == 0) != want ||
				(!want && (len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "pattern")) {
				t.Fatalf("finite language disagreed: pattern %q, input %q, report %+v, err %v", tc.pattern, input, r, err)
			}
		}
	}
}

func TestSchemaPatternRejectsMalformedAndUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern any
	}{
		{"null-metadata", nil}, {"number-metadata", 42}, {"boolean-metadata", true},
		{"array-metadata", []any{"private-pattern"}}, {"object-metadata", map[string]any{"private": "pattern"}},
		{"invalid-unclosed-class", "[private-pattern"}, {"invalid-unclosed-group", "(private-pattern"},
		{"invalid-closed-group", "private-pattern)"}, {"invalid-dangling-escape", "private\\"},
		{"invalid-range", "[z-a]"}, {"empty-class", "[]"}, {"first-close-class-ambiguity", "[]a]"},
		{"dot", "a.b"}, {"negated-class", "[^a]"}, {"inline-flags", "(?i)private"},
		{"scoped-flags", "(?s:private)"}, {"positive-lookahead", "(?=private)"},
		{"negative-lookahead", "(?!private)"}, {"lookbehind", "(?<=private)"},
		{"backreference", "(private)\\1"}, {"named-capture", "(?<private>a)"},
		{"go-named-capture", "(?P<private>a)"}, {"go-begin", "\\Aprivate"}, {"go-end", "private\\z"},
		{"go-quoted", "\\Qprivate\\E"}, {"unicode-property", "\\p{Han}"},
		{"unicode-escape", "\\u0061"}, {"hex-escape", "\\x61"}, {"hex-braces", "\\x{61}"},
		{"octal", "\\141"}, {"identity-escape", "\\a"}, {"word-boundary", "\\bprivate"},
		{"nonword-boundary", "\\Bprivate"}, {"digit-complement", "\\D"}, {"word-complement", "\\W"},
		{"whitespace", "\\s"}, {"whitespace-complement", "\\S"}, {"posix-class", "[[:alpha:]]"},
		{"nested-class-extension", "[a[b]]"}, {"class-backspace", "[\\b]"},
		{"class-shorthand-left-range", "[\\d-a]"}, {"class-shorthand-right-range", "[a-\\w]"},
		{"astral-literal", "🚢"}, {"astral-class", "[🚢]"},
		{"surrogate-crossing-range", "[a-\ue000]"},
		{"surrogate-crossing-range-unused", "[a-\ue000]{0}"},
		{"all-bmp-range", "[\x00-\uffff]"},
		{"direct-start-assertion-repeat", "^*"}, {"direct-end-assertion-repeat", "$+"},
		{"direct-assertion-count", "^{2}"}, {"leading-quantifier", "*a"},
		{"duplicate-quantifier", "a?*"}, {"possessive-quantifier", "a++"},
		{"repeat-leading-zero-min", "a{01}"}, {"repeat-leading-zero-max", "a{1,02}"},
		{"repeat-empty-min", "a{,2}"}, {"repeat-reversed", "a{3,2}"},
		{"repeat-too-large", "a{1001}"}, {"repeat-integer-overflow", "a{999999999999999999999999}"},
		{"repeat-not-numeric", "a{private}"}, {"unmatched-close-repeat", "a}"},
		{"unsupported-zero-repeat", ".{0}"}, {"unsupported-empty-alternative", "(?i:a)|"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := constraintCheck(t, patternProperty(t, tc.pattern), "PRIVATE_DRAFT_VALUE🚢")
			constraintWant(t, r, true)
			if strings.Contains(r.Text()+constraintEncode(t, r), "private") || strings.Contains(r.Text(), "PRIVATE_") {
				t.Fatal("unsupported pattern report disclosed data or parser errors")
			}
		})
	}
	for _, tc := range []struct {
		name     string
		property map[string]any
		value    any
		partial  bool
		codes    []string
	}{
		{"valid-pattern-wrong-type", map[string]any{"type": "string", "pattern": "ok"}, int64(7), false, []string{"type"}},
		{"invalid-pattern-wrong-type", map[string]any{"type": "string", "pattern": "["}, int64(7), true, []string{"type"}},
		{"unsupported-pattern-wrong-type", map[string]any{"type": "string", "pattern": "."}, false, true, []string{"type"}},
		{"nullable-null", map[string]any{"type": "string", "nullable": true, "pattern": "ok"}, nil, false, nil},
		{"nullable-null-invalid", map[string]any{"type": "string", "nullable": true, "pattern": "["}, nil, true, nil},
		{"nullable-null-unsupported", map[string]any{"type": "string", "nullable": true, "pattern": "."}, nil, true, nil},
		{"nonnullable-null", map[string]any{"type": "string", "pattern": "ok"}, nil, false, []string{"type"}},
		{"untyped-string", map[string]any{"pattern": "^ok$"}, "no", false, []string{"pattern"}},
		{"untyped-number", map[string]any{"pattern": "^ok$"}, int64(7), false, nil},
		{"boolean-irrelevant-valid-pattern", map[string]any{"type": "boolean", "pattern": "ok"}, true, false, nil},
		{"boolean-irrelevant-invalid-pattern", map[string]any{"type": "boolean", "pattern": "["}, true, true, nil},
		{"canonical-union-string", map[string]any{"x-kubernetes-int-or-string": true, "anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}}, "pattern": "^ok$"}, "no", false, []string{"pattern"}},
		{"canonical-union-integer", map[string]any{"x-kubernetes-int-or-string": true, "anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}}, "pattern": "^ok$"}, int64(7), false, nil},
		{"enum-and-pattern", map[string]any{"type": "string", "enum": []any{"allowed"}, "pattern": "^ok$"}, "no", false, []string{"enum", "pattern"}},
		{"length-and-pattern", map[string]any{"type": "string", "minLength": 3, "pattern": "^ok$"}, "x", false, []string{"minLength", "pattern"}},
		{"unknown-alongside-pattern", map[string]any{"type": "string", "format": "private-format", "pattern": "^ok$"}, "no", true, []string{"pattern"}},
		{"unknown-alongside-type", map[string]any{"type": "string", "format": "private-format", "pattern": "^ok$"}, int64(7), true, []string{"type"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraintWant(t, constraintCheck(t, constraintEncode(t, tc.property), tc.value), tc.partial, tc.codes...)
		})
	}
	t.Run("invalid-utf8-decoded-value", func(t *testing.T) {
		constraintWant(t, constraintCheck(t, patternProperty(t, "ok"), string([]byte{0xff, 'o', 'k'})), true)
	})
	t.Run("invalid-utf8-schema-metadata", func(t *testing.T) {
		d, _ := constraintDocument(t, patternProperty(t, "ok"))
		d.root["properties"].(map[string]any)["value"].(map[string]any)["pattern"] = string([]byte{0xff})
		r, err := d.Check(context.Background(), constraintObject("no"))
		if err != nil {
			t.Fatal(err)
		}
		constraintWant(t, r, true)
	})
}

func TestSchemaPatternBudgetsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, value string
		partial              bool
		codes                []string
	}{
		{"pattern-byte-limit", "a" + strings.Repeat("|a", 255) + "|", "a", false, nil},
		{"pattern-byte-limit-plus-one", strings.Repeat("a", MaxPatternBytes+1), "a", true, nil},
		{"input-byte-limit", "x", strings.Repeat("x", MaxPatternInputBytes), false, nil},
		{"input-byte-limit-plus-one", "x", strings.Repeat("x", MaxPatternInputBytes+1), true, nil},
		{"input-limit-multibyte", "船", strings.Repeat("船", MaxPatternInputBytes/3+1), true, nil},
		{"group-depth-limit", strings.Repeat("(", MaxPatternDepth) + "a" + strings.Repeat(")", MaxPatternDepth), "a", false, nil},
		{"group-depth-limit-plus-one", strings.Repeat("(", MaxPatternDepth+1) + "a" + strings.Repeat(")", MaxPatternDepth+1), "a", true, nil},
		{"program-exact-limit", strings.Repeat("a", MaxPatternProgram-2), strings.Repeat("a", MaxPatternProgram-2), false, nil},
		{"program-limit-plus-one", strings.Repeat("a", MaxPatternProgram-1), "no", true, nil},
		{"pre-simplify-nested-repetition", "((ab){20}){20}", "no", true, nil},
		{"pre-simplify-nested-nullable-repetition", "((a?){20}){20}", "no", true, nil},
		{"program-times-input-work", "(?:a?){100}b", strings.Repeat("a", MaxPatternInputBytes), true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraintWant(t, constraintCheck(t, patternProperty(t, tc.pattern), tc.value), tc.partial, tc.codes...)
		})
	}
	t.Run("nullable-star-and-repetition-program-bounds", func(t *testing.T) {
		for _, pattern := range []string{"(a?)*", "(?:^)*", "((a?)*)*", "(a?){0,}", "(a?){2,}", "(a?){2,7}", "a{0}", "(a|bc){3}", "(?:a|){0,5}"} {
			re, err := syntax.Parse(pattern, syntax.Perl)
			if err != nil {
				t.Fatal(err)
			}
			bound, ok := patternProgramBound(re)
			program, err := syntax.Compile(re.Simplify())
			if err != nil || !ok || len(program.Inst) > bound+2 {
				t.Fatalf("pre-expansion bound underestimated %q: bound=%d, program=%v, err=%v", pattern, bound, program, err)
			}
		}
	})
	t.Run("matching-work-is-shared-across-values", func(t *testing.T) {
		property := constraintEncode(t, map[string]any{"type": "array", "items": map[string]any{"type": "string", "pattern": "^a+$"}})
		values := make([]any, 20)
		for i := range values {
			values[i] = strings.Repeat("a", 4096)
		}
		r := constraintCheck(t, property, values)
		constraintWant(t, r, true)
		if r.Checked < MaxWork/2 || r.Checked > MaxWork+len(values)+8 {
			t.Fatal("pattern matching did not consume the shared work allowance", r.Checked)
		}
	})
	for _, prior := range []string{"work", "pattern-bytes", "input-bytes"} {
		t.Run("prior-"+prior+"-exhaustion-does-not-invent-hint", func(t *testing.T) {
			r := Report{}
			c := constraintChecker{ctx: context.Background(), report: &r, add: func(path, code, message string) {
				r.Diagnostics = append(r.Diagnostics, Diagnostic{path, code, message})
			}}
			switch prior {
			case "work":
				r.Checked = MaxWork
			case "pattern-bytes":
				c.bytes = MaxConstraintBytes
			case "input-bytes":
				c.bytes = MaxConstraintBytes - 2
			}
			c.checkPattern(map[string]any{"pattern": "ok"}, "PRIVATE_DRAFT_VALUE", "/value", true)
			constraintWant(t, r, true)
		})
	}
	t.Run("cancel-before-inspection", func(t *testing.T) {
		d, _ := constraintDocument(t, patternProperty(t, "^a+$"))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := d.Check(ctx, constraintObject("aaaa")); !errors.Is(err, context.Canceled) {
			t.Fatal("initial cancellation was lost", err)
		}
	})
	t.Run("cancel-during-repeated-pattern-inspection", func(t *testing.T) {
		property := constraintEncode(t, map[string]any{"type": "array", "items": map[string]any{"type": "string", "pattern": "^a+$"}})
		d, _ := constraintDocument(t, property)
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &constraintCancelAfter{Context: base, cancel: cancel, after: 35}
		if _, err := d.Check(ctx, constraintObject([]any{"aaa", "aaa", "aaa", "aaa"})); !errors.Is(err, context.Canceled) {
			t.Fatal("in-flight pattern cancellation was lost", err)
		}
	})
}

func TestSchemaPatternInspectionPreservesInputsAndPrivacy(t *testing.T) {
	property := map[string]any{"type": "object", "properties": map[string]any{
		"checked": map[string]any{"type": "string", "pattern": "^PRIVATE_SCHEMA_PATTERN$", "default": "PRIVATE_DEFAULT", "example": "PRIVATE_EXAMPLE"},
		"unknown": map[string]any{"type": "string", "pattern": "(?i)PRIVATE_UNSUPPORTED"},
		"nested":  map[string]any{"type": "array", "items": map[string]any{"type": "string", "pattern": "船"}},
	}}
	d, raw := constraintDocument(t, constraintEncode(t, property))
	rawBefore := append([]byte(nil), raw...)
	object := constraintObject(map[string]any{"checked": "PRIVATE_DRAFT_VALUE🚢", "unknown": "PRIVATE_UNKNOWN_VALUE", "nested": []any{"中文船🚢", "PRIVATE_OTHER_VALUE🚢"}})
	objectBefore, documentBefore := constraintEncode(t, object), constraintEncode(t, d.root)
	want, err := d.Check(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	constraintWant(t, want, true, "pattern", "pattern")
	for _, diagnostic := range want.Diagnostics {
		if diagnostic.Pointer != "/value/checked" && diagnostic.Pointer != "/value/nested/1" {
			t.Fatal("pattern inspection lost a nested original-source pointer")
		}
	}
	if strings.Contains(want.Text()+constraintEncode(t, want), "PRIVATE_") {
		t.Fatal("pattern report disclosed source, schema, default or example")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := d.Check(context.Background(), object)
			if e != nil || !reflect.DeepEqual(r, want) {
				t.Error("concurrent read-only pattern inspection changed its result", e)
			}
		}()
	}
	wg.Wait()
	if objectBefore != constraintEncode(t, object) || documentBefore != constraintEncode(t, d.root) || !bytes.Equal(rawBefore, raw) {
		t.Fatal("pattern inspection changed draft, document or original source")
	}
}
