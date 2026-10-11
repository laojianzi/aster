package manifest

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	MaxFindQueryBytes = 1024
	MaxFindQueryRunes = 256
	MaxFindMatches    = 512
)

var (
	ErrFindInput = errors.New("find requires non-empty valid UTF-8 literal text")
	ErrFindLimit = errors.New("find exceeds the document or query budget")
)

// FindMatch is a non-overlapping exact literal match. Ranges are half open;
// rune offsets are MyGo's selection units. Line/Column are one-based Unicode
// code-point coordinates (LF delimits lines, CR is preserved), not display cells.
type FindMatch struct {
	ByteStart, ByteEnd int
	RuneStart, RuneEnd int
	Line, Column       int
}

// FindResult is immutable. It retains positions and digests, never source text,
// query text or excerpts. At returns a value rather than an alias of the slice.
type FindResult struct {
	source, query [32]byte
	matches       []FindMatch
	truncated     bool
}

func (r *FindResult) Len() int {
	if r == nil {
		return 0
	}
	return len(r.matches)
}
func (r *FindResult) At(i int) (FindMatch, bool) {
	if r == nil || i < 0 || i >= len(r.matches) {
		return FindMatch{}, false
	}
	return r.matches[i], true
}
func (r *FindResult) Truncated() bool { return r != nil && r.truncated }
func (r *FindResult) Matches(source, query string) bool {
	return r != nil && len(source) <= MaxBytes && len(query) <= MaxFindQueryBytes &&
		r.source == sha256.Sum256([]byte(source)) && r.query == sha256.Sum256([]byte(query))
}

// FindLiteral runs a bounded linear-time KMP scan. Queries are case-sensitive,
// unnormalized and literal, including punctuation/newlines; empty queries do
// not manufacture matches. Invalid YAML is still searchable as plain text.
func FindLiteral(ctx context.Context, source, query string) (*FindResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source) > MaxBytes || len(query) > MaxFindQueryBytes || strings.Count(source, "\n") > 5000 {
		return nil, ErrFindLimit
	}
	if query == "" || !utf8.ValidString(source) || !utf8.ValidString(query) {
		return nil, ErrFindInput
	}
	needle := []rune(query)
	if len(needle) > MaxFindQueryRunes {
		return nil, ErrFindLimit
	}
	prefix := make([]int, len(needle))
	for i, j := 1, 0; i < len(needle); i++ {
		for j > 0 && needle[i] != needle[j] {
			j = prefix[j-1]
		}
		if needle[i] == needle[j] {
			j++
		}
		prefix[i] = j
	}
	type point struct{ b, r, line, col int }
	ring := make([]point, len(needle))
	result := &FindResult{source: sha256.Sum256([]byte(source)), query: sha256.Sum256([]byte(query))}
	index, line, col, matched := 0, 1, 1, 0
	for b, char := range source {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		ring[index%len(needle)] = point{b, index, line, col}
		for matched > 0 && char != needle[matched] {
			matched = prefix[matched-1]
		}
		if char == needle[matched] {
			matched++
		}
		if matched == len(needle) {
			if len(result.matches) == MaxFindMatches {
				result.truncated = true
				break
			}
			start := ring[(index-len(needle)+1)%len(needle)]
			result.matches = append(result.matches, FindMatch{start.b, b + utf8.RuneLen(char), start.r, index + 1, start.line, start.col})
			matched = 0 // Explicit non-overlapping find semantics.
		}
		if char == '\n' {
			line++
			col = 1
		} else {
			col++
		}
		index++
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
