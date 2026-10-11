package manifest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFindLiteralUnicodeCoordinatesAndExactRanges(t *testing.T) {
	for _, tc := range []struct {
		name, text, query string
		starts            []int
	}{
		{"unicode", "# 中文 🚢\r\nx: 🚢中文\r\ny: 🚢中文", "🚢中文", []int{18, 33}},
		{"overlap", "aaaaa", "aa", []int{0, 2}},
		{"literal", "a.*b A.*b a.*b", "a.*b", []int{0, 10}},
		{"combining", "e\u0301 é e\u0301", "e\u0301", []int{0, 7}},
		{"bom", "\ufeff🚢\nx: 🚢", "🚢", []int{3, 11}},
		{"newline", "a\r\nb\r\n", "\r\n", []int{1, 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := FindLiteral(context.Background(), tc.text, tc.query)
			if err != nil || r.Len() != len(tc.starts) || r.Truncated() {
				t.Fatalf("find result %v %d", err, r.Len())
			}
			for i, start := range tc.starts {
				m, ok := r.At(i)
				if !ok || m.ByteStart != start || tc.text[m.ByteStart:m.ByteEnd] != tc.query {
					t.Fatalf("%+v expected byte %d", m, start)
				}
				prefix := tc.text[:m.ByteStart]
				line := 1 + strings.Count(prefix, "\n")
				column := 1 + utf8.RuneCountInString(prefix[strings.LastIndexByte(prefix, '\n')+1:])
				if m.RuneStart != utf8.RuneCountInString(prefix) || m.RuneEnd-m.RuneStart != utf8.RuneCountInString(tc.query) || m.Line != line || m.Column != column {
					t.Fatalf("coordinates %+v", m)
				}
			}
			if !r.Matches(tc.text, tc.query) || r.Matches(tc.text+"x", tc.query) || r.Matches(tc.text, tc.query+"x") {
				t.Fatal("result was not bound to exact text and query")
			}
		})
	}
}
func TestFindLiteralLimitsCancellationAndTruncation(t *testing.T) {
	for _, tc := range []struct {
		source, query string
		want          error
	}{
		{"x", "", ErrFindInput}, {string([]byte{255}), "x", ErrFindInput}, {"x", string([]byte{255}), ErrFindInput},
		{strings.Repeat("x", MaxBytes+1), "x", ErrFindLimit}, {"x", strings.Repeat("x", MaxFindQueryBytes+1), ErrFindLimit},
		{"x", strings.Repeat("x", MaxFindQueryRunes+1), ErrFindLimit}, {strings.Repeat("\n", 5001), "x", ErrFindLimit},
	} {
		if _, e := FindLiteral(context.Background(), tc.source, tc.query); !errors.Is(e, tc.want) {
			t.Fatalf("%v", e)
		}
	}
	for _, count := range []int{MaxFindMatches, MaxFindMatches + 1} {
		r, e := FindLiteral(context.Background(), strings.Repeat("🚢", count), "🚢")
		if e != nil || r.Len() != MaxFindMatches || r.Truncated() != (count > MaxFindMatches) {
			t.Fatal("truncation not exact", e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := FindLiteral(ctx, "x", "x"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	ctx2 := &findCanceledDuringScan{Context: context.Background()}
	if _, e := FindLiteral(ctx2, strings.Repeat("a", 4096), "ab"); !errors.Is(e, context.Canceled) {
		t.Fatal("scan ignored cancellation", e)
	}
	var nilResult *FindResult
	if nilResult.Len() != 0 || nilResult.Truncated() || nilResult.Matches("", "") {
		t.Fatal("nil result unsafe")
	}
	if _, ok := nilResult.At(0); ok {
		t.Fatal("nil position")
	}
	r, e := FindLiteral(context.Background(), "", "x")
	if e != nil || r.Len() != 0 {
		t.Fatal(e)
	}
	if _, ok := r.At(-1); ok {
		t.Fatal("negative position")
	}
	if r.Matches(strings.Repeat("x", MaxBytes+1), "x") {
		t.Fatal("oversized freshness hashing")
	}
}

type findCanceledDuringScan struct {
	context.Context
	checks int
}

func (c *findCanceledDuringScan) Err() error {
	c.checks++
	if c.checks >= 3 {
		return context.Canceled
	}
	return nil
}
func TestFindLiteralLinearAdversarialPrefix(t *testing.T) {
	r, e := FindLiteral(context.Background(), strings.Repeat("a", MaxBytes-1)+"b", strings.Repeat("a", 255)+"b")
	if e != nil || r.Len() != 1 {
		t.Fatal(e)
	}
	m, _ := r.At(0)
	if m.ByteStart != MaxBytes-256 {
		t.Fatal(m)
	}
}
func FuzzFindLiteralRanges(f *testing.F) {
	for _, s := range [][2]string{{"中文🚢\r\nx: 🚢", "🚢"}, {"aaaaa", "aa"}, {"a.*b", ".*"}, {"é é", "é"}, {"", "a"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, source, query string) {
		r, e := FindLiteral(context.Background(), source, query)
		if e != nil {
			return
		}
		if !r.Matches(source, query) || r.Len() > MaxFindMatches {
			t.Fatal("identity/bound")
		}
		if query == "" {
			t.Fatal("empty query accepted")
		}
		offset := 0
		for i := 0; i < r.Len(); i++ {
			m, _ := r.At(i)
			j := strings.Index(source[offset:], query)
			if j < 0 || m.ByteStart != offset+j || m.ByteEnd != m.ByteStart+len(query) {
				t.Fatal("range is not the next literal occurrence")
			}
			if m.RuneStart != utf8.RuneCountInString(source[:m.ByteStart]) || m.RuneEnd != utf8.RuneCountInString(source[:m.ByteEnd]) {
				t.Fatal("invalid rune range")
			}
			p := source[:m.ByteStart]
			if m.Line != strings.Count(p, "\n")+1 || m.Column != utf8.RuneCountInString(p[strings.LastIndexByte(p, '\n')+1:])+1 {
				t.Fatal("wrong coordinates")
			}
			offset = m.ByteEnd
		}
		remaining := strings.Contains(source[offset:], query)
		if remaining != r.Truncated() {
			t.Fatal("missed or invented truncation")
		}
	})
}
