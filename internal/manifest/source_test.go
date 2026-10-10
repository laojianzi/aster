package manifest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func sourceIndex(t *testing.T, text string) *SourceIndex {
	t.Helper()
	x, err := IndexSource(context.Background(), []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return x
}
func TestSourceIndexUnicodeCRLFAndJSONPositions(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("newline-%q", newline), func(t *testing.T) {
			text := strings.ReplaceAll("# 中文 🚢 é\napiVersion: v1\nkind: Pod\nmetadata: {name: example}\nspec:\n  containers:\n  - name: app\n    image: 42 # invalid type\n", "\n", newline)
			x := sourceIndex(t, text)
			p, ok := x.Locate("/spec/containers/0/image")
			want := utf8.RuneCountInString(text[:strings.Index(text, "image:")])
			if !ok || !p.Exact || p.Line != 8 || p.Column != 5 || p.RuneOffset != want {
				t.Fatalf("got %+v, rune offset %d", p, want)
			}
			if !x.Matches(text) || x.Matches(text+"\n") {
				t.Fatal("revision binding failed")
			}
		})
	}
	text := `{"中文🚢": "value", "spec": {"special/key~": 42}, "": 1}`
	x := sourceIndex(t, text)
	p, ok := x.Locate("/spec/special~1key~0")
	want := utf8.RuneCountInString(text[:strings.Index(text, `"special/key~"`)])
	if !ok || !p.Exact || p.RuneOffset != want || p.Column != want+1 || p.Line != 1 {
		t.Fatalf("JSON position %+v", p)
	}
	if p, ok = x.Locate("/"); !ok || !p.Exact || p.Pointer != "/" {
		t.Fatalf("empty key confused with root: %+v", p)
	}
}
func TestSourceIndexAbsentFieldsUseExplicitAncestor(t *testing.T) {
	x := sourceIndex(t, "spec:\n  containers:\n  - name: app\n")
	for _, path := range []string{"/spec/containers/0/image", "/spec/containers/0/image/deeper"} {
		p, ok := x.Locate(path)
		if !ok || p.Exact || p.Pointer != "/spec/containers/0" || p.Line != 3 || p.Column != 5 {
			t.Fatalf("invented missing field location %+v", p)
		}
	}
	for _, path := range []string{"spec", "/~2bad", "/~", "/" + strings.Repeat("a", MaxSourcePointerBytes), strings.Repeat("/a", MaxSourceDepth+1), string([]byte{'/', 255})} {
		if _, ok := x.Locate(path); ok {
			t.Errorf("invalid pointer accepted: %q", path)
		}
	}
	if p, ok := x.Locate(""); !ok || !p.Exact || p.Line != 1 {
		t.Fatal("root missing")
	}
	var nilIndex *SourceIndex
	if _, ok := nilIndex.Locate(""); ok || nilIndex.Matches("") || nilIndex.Partial() {
		t.Fatal("nil index usable")
	}
}
func TestSourceIndexDoesNotExpandAliasesOrCoerceKeys(t *testing.T) {
	text := "base: &base {image: 42}\nspec:\n  containers: [*base]\nmerged:\n  <<: *base\n  name: local\nlabels:\n  yes: one\n  7: number\n  'no': quoted\n  !!str on: tagged\n"
	x := sourceIndex(t, text)
	if !x.Partial() {
		t.Fatal("unsupported source branches not disclosed")
	}
	for _, path := range []string{"/spec/containers/0/image", "/merged/image", "/labels/yes", "/labels/7"} {
		if p, ok := x.Locate(path); !ok || p.Exact || p.Pointer == path {
			t.Fatalf("alias/coercion produced exact location: %s %+v", path, p)
		}
	}
	for _, path := range []string{"/base/image", "/merged/name", "/labels/no", "/labels/on"} {
		if p, ok := x.Locate(path); !ok || !p.Exact {
			t.Fatalf("explicit source lost: %s %+v", path, p)
		}
	}
}
func TestSourceIndexRejectsAmbiguousAndUnsupportedInput(t *testing.T) {
	for _, s := range []string{"a: 1\na: 2", `{"a":1,"a":2}`, "a: 1\n---\nb: 2", "a: 1\n---\n", "a: [", "[1,2]", "null", "# empty"} {
		if x, err := IndexSource(context.Background(), []byte(s)); err == nil || x != nil {
			t.Errorf("ambiguous source accepted: %q", s)
		}
	}
	for _, s := range []string{"\ufeffa: 1", "a: 1\rb: 2", "a: 1\u0085b: 2", "a: 1\u2028b: 2", "a: 1\u2029b: 2", string([]byte{'a', ':', 255})} {
		if x, err := IndexSource(context.Background(), []byte(s)); !errors.Is(err, ErrSourceForm) || x != nil {
			t.Errorf("unsupported coordinates accepted: %q %v", s, err)
		}
	}
}
func TestSourceIndexBudgetsAndCancellation(t *testing.T) {
	largePaths := strings.Repeat("p", 950) + ":\n"
	for i := 0; i < 3000; i++ {
		largePaths += fmt.Sprintf("  k%d: 1\n", i)
	}
	cases := map[string]string{
		"bytes":        strings.Repeat("x", MaxBytes+1),
		"lines":        strings.Repeat("\n", 5001),
		"nodes":        "a: [" + strings.Repeat("0,", MaxSourceNodes) + "0]",
		"depth":        "a: " + strings.Repeat("[", MaxSourceDepth+1) + "0" + strings.Repeat("]", MaxSourceDepth+1),
		"pointer":      "? " + strings.Repeat("k", MaxSourcePointerBytes+1) + "\n: 1",
		"stored-paths": largePaths,
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			if x, err := IndexSource(context.Background(), []byte(s)); !errors.Is(err, ErrSourceLimit) || x != nil {
				t.Fatalf("budget not enforced: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if x, err := IndexSource(ctx, []byte("a: 1")); !errors.Is(err, context.Canceled) || x != nil {
		t.Fatal("cancellation ignored")
	}
}
func TestSourceIndexRetainsNoValuesAndNeverRewritesDraft(t *testing.T) {
	text := "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: example}\ndata:\n  payload: PRIVATE-SCALAR-CONTENT\n"
	data := []byte(text)
	x, err := IndexSource(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != text || strings.Contains(fmt.Sprintf("%#v", x), "PRIVATE-SCALAR-CONTENT") {
		t.Fatal("source rewritten or value retained")
	}
	for i := range data {
		data[i] = 0
	}
	if !x.Matches(text) {
		t.Fatal("index depends on mutable caller buffer")
	}
	if _, err := Decode([]byte(text)); err != nil {
		t.Fatal(err)
	}
}
func FuzzSourceIndexLocations(f *testing.F) {
	for _, s := range []string{"a: {b: 1}", "# 中文\nspec:\n  image: 42\n", "a: &a {b: *a}", `{"a/b~": null}`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		x, err := IndexSource(context.Background(), []byte(s))
		if err != nil {
			return
		}
		if !x.Matches(s) {
			t.Fatal("wrong source identity")
		}
		runes := utf8.RuneCountInString(s)
		for path, p := range x.positions {
			if !sourcePointerValid(path) || p.Line < 1 || p.Column < 1 || p.RuneOffset < 0 || p.RuneOffset > runes {
				t.Fatalf("invalid position %+v", p)
			}
		}
	})
}
