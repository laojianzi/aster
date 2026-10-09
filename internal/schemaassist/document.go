// Package schemaassist provides bounded, advisory OpenAPI assistance. It never
// resolves external references or evaluates admission, CEL or full JSON Schema.
package schemaassist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	MaxDocumentBytes = 16 << 20
	MaxIndexBytes    = 1 << 20
	MaxDepth         = 64
	MaxWork          = 20000
	MaxDiagnostics   = 100
	MaxFields        = 128
	MaxSchemaKeys    = 128
)

var (
	ErrInvalid     = errors.New("schema response is invalid")
	ErrLimit       = errors.New("schema inspection limit exceeded")
	ErrUnavailable = errors.New("schema unavailable; retry explicitly")
	ErrForbidden   = errors.New("schema access denied for this identity")
	ErrNotFound    = errors.New("schema or field not advertised for the selected resource")
	ErrUnsupported = errors.New("schema reference or structure is unsupported")
)

// ParseJSON rejects duplicate keys, trailing documents, excess depth and work.
// Returned errors never include response data or a credentialed request URL.
func ParseJSON(ctx context.Context, data []byte, maxBytes int) (any, error) {
	if len(data) == 0 || len(data) > maxBytes {
		return nil, ErrLimit
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	work := 0
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		work++
		if depth > MaxDepth || work > 500000 {
			return nil, ErrLimit
		}
		tok, err := d.Token()
		if err != nil {
			return nil, ErrInvalid
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return tok, nil
		}
		switch delim {
		case '{':
			m := map[string]any{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return nil, ErrInvalid
				}
				s, ok := key.(string)
				if !ok {
					return nil, ErrInvalid
				}
				if _, exists := m[s]; exists {
					return nil, ErrInvalid
				}
				v, e := read(depth + 1)
				if e != nil {
					return nil, e
				}
				m[s] = v
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return nil, ErrInvalid
			}
			return m, nil
		case '[':
			a := []any{}
			for d.More() {
				v, e := read(depth + 1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return nil, ErrInvalid
			}
			return a, nil
		default:
			return nil, ErrInvalid
		}
	}
	v, err := read(0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return v, nil
}

// Document owns its decoded maps. They are never exposed to callers or shared
// across identities. Each help/check request obtains a fresh document.
type Document struct {
	gvk               schema.GroupVersionKind
	root, definitions map[string]any
}

func Decode(ctx context.Context, data []byte, gvk schema.GroupVersionKind) (*Document, error) {
	v, err := ParseJSON(ctx, data, MaxDocumentBytes)
	if err != nil {
		return nil, err
	}
	top, ok := v.(map[string]any)
	if !ok {
		return nil, ErrInvalid
	}
	version, _ := top["openapi"].(string)
	if !strings.HasPrefix(version, "3.0.") {
		return nil, ErrUnsupported
	}
	components, _ := top["components"].(map[string]any)
	defs, _ := components["schemas"].(map[string]any)
	if len(defs) == 0 {
		return nil, ErrNotFound
	}
	if len(defs) > 8192 {
		return nil, ErrLimit
	}
	var root map[string]any
	for _, value := range defs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		node, ok := value.(map[string]any)
		if !ok {
			return nil, ErrInvalid
		}
		tags, _ := node["x-kubernetes-group-version-kind"].([]any)
		for _, tag := range tags {
			m, _ := tag.(map[string]any)
			if m["group"] == gvk.Group && m["version"] == gvk.Version && m["kind"] == gvk.Kind {
				if root != nil {
					return nil, ErrInvalid
				}
				root = node
			}
		}
	}
	if root == nil {
		return nil, ErrNotFound
	}
	return &Document{gvk: gvk, root: root, definitions: defs}, nil
}

// referenceWrapper recognizes the single-ref allOf emitted by Kubernetes
// WrapRefs. Structural siblings are deliberately not flattened: doing so could
// silently drop an intersection constraint. Allowed siblings are annotations or
// list/patch semantics that this advisory inspector explicitly does not validate.
func referenceWrapper(node map[string]any) (string, bool) {
	items, ok := node["allOf"].([]any)
	if !ok || len(items) != 1 {
		return "", false
	}
	child, ok := items[0].(map[string]any)
	if !ok || len(child) != 1 {
		return "", false
	}
	ref, ok := child["$ref"].(string)
	if !ok {
		return "", false
	}
	for key := range node {
		switch key {
		case "allOf", "description", "title", "default", "example", "externalDocs",
			"deprecated", "readOnly", "writeOnly", "format",
			"x-kubernetes-patch-strategy", "x-kubernetes-patch-merge-key",
			"x-kubernetes-list-type", "x-kubernetes-list-map-keys", "x-kubernetes-map-type":
		default:
			return "", false
		}
	}
	return ref, true
}
func (d *Document) resolve(ctx context.Context, node map[string]any) (map[string]any, error) {
	seen := map[string]bool{}
	// Only display annotations are overlaid; defaults/examples are never applied
	// or shown. A copy is made only at the end, leaving shared definitions intact.
	annotations := map[string]any{}
	for i := 0; i < 32; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Properties are nested in their own map; schema-node keywords have a
		// small independent budget so annotation overlays cannot amplify copies.
		if len(node) > MaxSchemaKeys {
			return nil, ErrLimit
		}
		value, exists := node["$ref"]
		ref, valid := value.(string)
		if !exists {
			ref, valid = referenceWrapper(node)
			if !valid {
				if len(annotations) == 0 {
					return node, nil
				}
				resolved := make(map[string]any, len(node)+len(annotations))
				for key, value := range node {
					resolved[key] = value
				}
				for key, value := range annotations {
					resolved[key] = value
				}
				return resolved, nil
			}
		}
		if !valid || len(ref) > 4096 || !strings.HasPrefix(ref, "#/components/schemas/") {
			return nil, ErrUnsupported
		}
		if seen[ref] {
			return nil, ErrUnsupported
		}
		seen[ref] = true
		for _, key := range []string{"description", "title"} {
			if _, present := annotations[key]; !present {
				if value, ok := node[key].(string); ok {
					annotations[key] = value
				}
			}
		}
		parts, err := pointer(strings.TrimPrefix(ref, "#"))
		if err != nil || len(parts) != 3 || parts[0] != "components" || parts[1] != "schemas" {
			return nil, ErrUnsupported
		}
		node, valid = d.definitions[parts[2]].(map[string]any)
		if !valid {
			return nil, ErrUnsupported
		}
	}
	return nil, ErrLimit
}
func pointer(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	if len(s) > 1024 || !strings.HasPrefix(s, "/") {
		return nil, ErrInvalid
	}
	parts := strings.Split(s[1:], "/")
	if len(parts) > MaxDepth {
		return nil, ErrLimit
	}
	for i, p := range parts {
		var b strings.Builder
		for j := 0; j < len(p); j++ {
			if p[j] != '~' {
				b.WriteByte(p[j])
				continue
			}
			if j+1 == len(p) {
				return nil, ErrInvalid
			}
			j++
			switch p[j] {
			case '0':
				b.WriteByte('~')
			case '1':
				b.WriteByte('/')
			default:
				return nil, ErrInvalid
			}
		}
		parts[i] = b.String()
	}
	return parts, nil
}
func escape(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func text(s string, limit int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= limit {
			b.WriteString("…")
			break
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
func nodeType(n map[string]any) string {
	if n["x-kubernetes-int-or-string"] == true {
		return "integer|string"
	}
	typ, _ := n["type"].(string)
	if typ == "" {
		if _, ok := n["properties"]; ok {
			return "object"
		}
	}
	return typ
}
func composite(n map[string]any) bool {
	for _, key := range []string{"allOf", "oneOf", "anyOf", "not"} {
		if _, ok := n[key]; ok && n["x-kubernetes-int-or-string"] != true {
			return true
		}
	}
	return false
}

// Rendered help is bounded independently from the decoded schema.
func boundedOutput(s string) string {
	const limit = 64 << 10
	const suffix = "\nOutput truncated; inspection is incomplete."
	if len(s) <= limit {
		return s
	}
	end := limit - len(suffix)
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + suffix
}
