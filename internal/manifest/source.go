package manifest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	yamlv3 "go.yaml.in/yaml/v3"
)

// Source indexing is a presentation aid. Decode remains the authoritative local
// manifest decoder; this index never normalizes, expands aliases, or writes YAML.
const (
	MaxSourceNodes        = 20000
	MaxSourceDepth        = 64
	MaxSourcePointerBytes = 1024
	MaxSourceIndexBytes   = 2 << 20
)

var (
	ErrSourceInvalid = errors.New("source locations unavailable: invalid or ambiguous document")
	ErrSourceLimit   = errors.New("source locations exceed the inspection budget")
	ErrSourceForm    = errors.New("source locations unavailable for this text encoding or line-ending form")
)

// SourceLocation points at an explicit field key or sequence item. RuneOffset is
// zero based (MyGo's selection unit); Line and Column are one based Unicode code
// point coordinates, not byte offsets, display-cell widths or UTF-16 positions.
// Exact=false means Pointer is an enclosing explicit value, NOT the missing or
// alias-expanded field requested by the caller. No scalar values are retained.
type SourceLocation struct {
	Pointer                  string
	Line, Column, RuneOffset int
	Exact                    bool
}

// SourceIndex is immutable after construction and may cross a worker/UI boundary.
// It deliberately does not retain the draft, AST, or any scalar value.
type SourceIndex struct {
	digest    [32]byte
	positions map[string]SourceLocation
	partial   bool
}

func (s *SourceIndex) Matches(draft string) bool {
	return s != nil && s.digest == sha256.Sum256([]byte(draft))
}
func (s *SourceIndex) Partial() bool { return s != nil && s.partial }

// Locate returns the closest explicitly represented ancestor when the field is
// absent. An invalid JSON Pointer never falls back to the root. Alias/merge
// content is never followed to a different source location.
func (s *SourceIndex) Locate(pointer string) (SourceLocation, bool) {
	if s == nil || !sourcePointerValid(pointer) {
		return SourceLocation{}, false
	}
	original := pointer
	for {
		if location, ok := s.positions[pointer]; ok {
			location.Exact = pointer == original
			return location, true
		}
		if pointer == "" {
			return SourceLocation{}, false
		}
		pointer = pointer[:strings.LastIndexByte(pointer, '/')]
	}
}

func sourcePointerValid(path string) bool {
	if len(path) > MaxSourcePointerBytes || !utf8.ValidString(path) || (path != "" && path[0] != '/') || strings.Count(path, "/") > MaxSourceDepth {
		return false
	}
	for i := 0; i < len(path); i++ {
		if path[i] == '~' {
			i++
			if i == len(path) || (path[i] != '0' && path[i] != '1') {
				return false
			}
		}
	}
	return true
}

func sourceEscape(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

// IndexSource builds a bounded, read-only JSON-Pointer-to-source map. The YAML
// parser is synchronous within the byte/line budget; cancellation is checked
// around parsing and throughout the bounded AST walk. Unsupported coordinates
// are refused, not guessed. LF and CRLF are supported; other YAML line breaks
// and BOM-bearing text intentionally have no source-location contract here.
func IndexSource(ctx context.Context, data []byte) (*SourceIndex, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > MaxBytes || bytes.Count(data, []byte("\n")) > 5000 {
		return nil, ErrSourceLimit
	}
	if !utf8.Valid(data) {
		return nil, ErrSourceForm
	}
	// Build rune-based line starts once. Do not repeatedly scan the entire draft
	// for every node (which would make long/flat documents quadratic).
	starts, widths := []int{0}, []int{}
	offset, width := 0, 0
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == '\ufeff' || r == '\u0085' || r == '\u2028' || r == '\u2029' || (r == '\r' && (i+1 == len(data) || data[i+1] != '\n')) {
			return nil, ErrSourceForm
		}
		offset++
		if r == '\n' {
			widths = append(widths, width)
			starts = append(starts, offset)
			width = 0
		} else {
			width++
		}
		i += size
	}
	widths = append(widths, width)
	var root, extra yamlv3.Node
	decoder := yamlv3.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&root); err != nil || len(root.Content) != 1 || root.Content[0].Kind != yamlv3.MappingNode {
		return nil, ErrSourceInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, ErrSourceInvalid
	}
	index := &SourceIndex{digest: sha256.Sum256(data), positions: make(map[string]SourceLocation)}
	work, stored := 0, 0
	charge := func(n int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		work += n
		if work > MaxSourceNodes {
			return ErrSourceLimit
		}
		return nil
	}
	var visit func(*yamlv3.Node, *yamlv3.Node, string, int) error
	visit = func(node, position *yamlv3.Node, path string, depth int) error {
		if err := charge(1); err != nil {
			return err
		}
		if depth > MaxSourceDepth || len(path) > MaxSourcePointerBytes {
			return ErrSourceLimit
		}
		// Alias locations only describe the reference, not the value. Keep the
		// parent as the nearest reliable location rather than imply exactness.
		if node.Kind == yamlv3.AliasNode {
			index.partial = true
			return nil
		}
		if position.Line < 1 || position.Line > len(starts) || position.Column < 1 || position.Column > widths[position.Line-1]+1 {
			return ErrSourceInvalid
		}
		stored += len(path) + 64
		if stored > MaxSourceIndexBytes {
			return ErrSourceLimit
		}
		index.positions[path] = SourceLocation{Pointer: path, Line: position.Line, Column: position.Column, RuneOffset: starts[position.Line-1] + position.Column - 1, Exact: true}
		switch node.Kind {
		case yamlv3.MappingNode:
			if len(node.Content)%2 != 0 {
				return ErrSourceInvalid
			}
			if err := charge(len(node.Content)); err != nil {
				return err
			}
			seen := make(map[string]bool, len(node.Content)/2)
			for i := 0; i < len(node.Content); i += 2 {
				key, value := node.Content[i], node.Content[i+1]
				if key.Kind != yamlv3.ScalarNode || key.ShortTag() != "!!str" || ambiguousSourceKey(key) {
					index.partial = true
					continue
				}
				if seen[key.Value] {
					return ErrSourceInvalid
				}
				seen[key.Value] = true
				if err := visit(value, key, path+"/"+sourceEscape(key.Value), depth+1); err != nil {
					return err
				}
			}
		case yamlv3.SequenceNode:
			if len(node.Content) > MaxSourceNodes-work {
				return ErrSourceLimit
			}
			for i, child := range node.Content {
				if err := visit(child, child, path+"/"+strconv.Itoa(i), depth+1); err != nil {
					return err
				}
			}
		case yamlv3.ScalarNode:
		default:
			index.partial = true
		}
		return nil
	}
	if err := visit(root.Content[0], root.Content[0], "", 0); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return index, nil
}

// The Kubernetes decoder retains YAML 1.1 key coercion, unlike the AST reader.
// Do not map an unquoted yes/on/etc to a string-key JSON Pointer accidentally.
func ambiguousSourceKey(key *yamlv3.Node) bool {
	if key.Style&(yamlv3.DoubleQuotedStyle|yamlv3.SingleQuotedStyle|yamlv3.TaggedStyle) != 0 {
		return false
	}
	switch strings.ToLower(key.Value) {
	case "y", "n", "yes", "no", "on", "off":
		return true
	}
	return false
}
