package schemaassist

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

type Field struct {
	Name, Pointer, Type string
	Required            bool
}
type Help struct {
	Pointer, Type, Description string
	Fields                     []Field
	Partial                    bool
}

func (h Help) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Field %s · type %s\n%s\n", text(h.Pointer, 1024), text(h.Type, 80), h.Description)
	for _, f := range h.Fields {
		required := ""
		if f.Required {
			required = " · required"
		}
		fmt.Fprintf(&b, "%s : %s%s\n", text(f.Pointer, 256), text(f.Type, 80), required)
	}
	if h.Partial {
		b.WriteString("Help is partial (field limit or unsupported schema branch).\n")
	}
	b.WriteString("Read-only field help. Constraints/defaults are not applied; use server dry-run.")
	return boundedOutput(b.String())
}
func (d *Document) Help(ctx context.Context, path string) (Help, error) {
	out := Help{Pointer: path}
	parts, err := pointer(path)
	if err != nil {
		return out, err
	}
	n := d.root
	for _, part := range parts {
		n, err = d.resolve(ctx, n)
		if err != nil {
			return out, err
		}
		props, _ := n["properties"].(map[string]any)
		next, ok := props[part].(map[string]any)
		if !ok && nodeType(n) == "array" {
			if part == "" || len(part) > 9 || strings.Trim(part, "0123456789") != "" {
				return out, ErrNotFound
			}
			next, ok = n["items"].(map[string]any)
		}
		if !ok {
			next, ok = n["additionalProperties"].(map[string]any)
		}
		if !ok {
			if composite(n) {
				return out, ErrUnsupported
			}
			return out, ErrNotFound
		}
		n = next
	}
	n, err = d.resolve(ctx, n)
	if err != nil {
		return out, err
	}
	out.Type = nodeType(n)
	if out.Type == "" {
		out.Type = "unspecified"
	}
	desc, _ := n["description"].(string)
	out.Description = text(desc, 2048)
	out.Partial = composite(n)
	props, _ := n["properties"].(map[string]any)
	if len(props) > MaxWork {
		return out, ErrLimit
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	required := map[string]bool{}
	a, _ := n["required"].([]any)
	if len(a) > MaxWork {
		return out, ErrLimit
	}
	for _, v := range a {
		if err := ctx.Err(); err != nil {
			return Help{}, err
		}
		if k, ok := v.(string); ok {
			required[k] = true
		}
	}
	if len(keys) > MaxFields {
		keys = keys[:MaxFields]
		out.Partial = true
	}
	for _, k := range keys {
		child, ok := props[k].(map[string]any)
		typ := "unspecified"
		if !ok {
			out.Partial = true
		} else {
			resolved, e := d.resolve(ctx, child)
			if ctx.Err() != nil {
				return Help{}, ctx.Err()
			}
			if e != nil {
				out.Partial = true
			} else {
				typ = nodeType(resolved)
				if typ == "" {
					typ = "unspecified"
				}
			}
		}
		out.Fields = append(out.Fields, Field{Name: k, Pointer: path + "/" + escape(k), Type: typ, Required: required[k]})
	}
	return out, ctx.Err()
}

type Diagnostic struct{ Pointer, Code, Message string }
type Report struct {
	Diagnostics []Diagnostic
	Partial     bool
	Checked     int
}

func (r Report) Text() string {
	var b strings.Builder
	b.WriteString("Structural hints only: required, type and undescribed fields. Not admission validation.\n")
	for _, d := range r.Diagnostics {
		fmt.Fprintf(&b, "%s · %s: %s\n", text(d.Pointer, 160), d.Code, d.Message)
	}
	if len(r.Diagnostics) == 0 {
		b.WriteString("No required/type/unknown-field hints found.\n")
	}
	if r.Partial {
		b.WriteString("Inspection is incomplete: unsupported structure or inspection limit reached.\n")
	}
	b.WriteString("The draft was not changed. Server dry-run remains required.")
	return boundedOutput(b.String())
}
func integer(v any) bool {
	switch n := v.(type) {
	case int, int32, int64, uint, uint32, uint64:
		return true
	case float64:
		return !math.IsNaN(n) && !math.IsInf(n, 0) && math.Trunc(n) == n
	case json.Number:
		// Classify decimal/exponent spelling without allocating a huge big.Int.
		s := string(n)
		if len(s) > 512 || !json.Valid([]byte(s)) {
			return false
		}
		exponent := int64(0)
		if i := strings.IndexAny(s, "eE"); i >= 0 {
			var err error
			exponent, err = strconv.ParseInt(s[i+1:], 10, 32)
			if err != nil {
				return false
			}
			s = s[:i]
		}
		s = strings.TrimPrefix(s, "-")
		fraction := int64(0)
		if i := strings.IndexByte(s, '.'); i >= 0 {
			fraction = int64(len(s) - i - 1)
			s = s[:i] + s[i+1:]
		}
		zeros := fraction - exponent
		if zeros <= 0 {
			return true
		}
		if zeros > int64(len(s)) {
			return strings.Trim(s, "0") == ""
		}
		return strings.Trim(s[len(s)-int(zeros):], "0") == ""
	}
	return false
}
func typeMatches(typ string, v any) bool {
	switch typ {
	case "":
		return true
	case "integer":
		return integer(v)
	case "integer|string":
		_, s := v.(string)
		return s || integer(v)
	case "number":
		switch v.(type) {
		case json.Number, float64, int, int32, int64, uint, uint64:
			return true
		}
		return false
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "null":
		return v == nil
	}
	return true
}

// Check only reads the draft. No values, examples or defaults enter diagnostic
// output. Unknown/composite structures and budget exhaustion remain explicit.
func (d *Document) Check(ctx context.Context, obj map[string]any) (Report, error) {
	r := Report{}
	if obj["apiVersion"] != d.gvk.GroupVersion().String() || obj["kind"] != d.gvk.Kind {
		return r, ErrInvalid
	}
	add := func(path, code, message string) {
		if len(r.Diagnostics) >= MaxDiagnostics {
			r.Partial = true
			return
		}
		r.Diagnostics = append(r.Diagnostics, Diagnostic{path, code, message})
	}
	var visit func(map[string]any, any, string, int)
	visit = func(raw map[string]any, value any, path string, depth int) {
		if ctx.Err() != nil {
			return
		}
		r.Checked++
		if r.Checked > MaxWork || depth > MaxDepth || len(path) > 1024 || len(r.Diagnostics) >= MaxDiagnostics {
			r.Partial = true
			return
		}
		n, err := d.resolve(ctx, raw)
		if err != nil {
			r.Partial = true
			add(path, "unsupported", "Reference could not be inspected locally.")
			return
		}
		typ := nodeType(n)
		if value == nil && n["nullable"] == true {
			return
		}
		if value == nil && typ != "null" && typ != "" {
			add(path, "type", "Expected "+text(typ, 80)+"; null is not nullable.")
			return
		}
		if !typeMatches(typ, value) {
			add(path, "type", "Expected "+text(typ, 80)+".")
			return
		}
		isComposite := composite(n)
		if isComposite {
			r.Partial = true
		}
		switch typ {
		case "", "integer", "integer|string", "number", "string", "boolean", "object", "array", "null":
		default:
			r.Partial = true
			return
		}
		switch v := value.(type) {
		case map[string]any:
			if len(v) > MaxWork-r.Checked {
				r.Partial = true
				return
			}
			props, _ := n["properties"].(map[string]any)
			required, _ := n["required"].([]any)
			if len(required) > MaxWork-r.Checked {
				r.Partial = true
				return
			}
			for _, item := range required {
				if ctx.Err() != nil {
					return
				}
				// A hostile required array can repeat present keys, causing no
				// diagnostics. Charge each entry, not only visited value nodes.
				if r.Checked >= MaxWork || len(r.Diagnostics) >= MaxDiagnostics {
					r.Partial = true
					return
				}
				r.Checked++
				key, ok := item.(string)
				if !ok {
					r.Partial = true
					continue
				}
				if _, present := v[key]; !present {
					add(path+"/"+escape(key), "required", "Required field is absent.")
				}
			}
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if len(r.Diagnostics) >= MaxDiagnostics || r.Checked >= MaxWork {
					r.Partial = true
					break
				}
				child, ok := props[key].(map[string]any)
				if !ok {
					child, ok = n["additionalProperties"].(map[string]any)
				}
				if ok {
					visit(child, v[key], path+"/"+escape(key), depth+1)
					continue
				}
				if n["x-kubernetes-preserve-unknown-fields"] == true || n["additionalProperties"] == true || isComposite {
					continue
				}
				if n["x-kubernetes-embedded-resource"] == true && (key == "apiVersion" || key == "kind" || key == "metadata") {
					continue
				}
				if props != nil || n["additionalProperties"] == false {
					add(path+"/"+escape(key), "unknown", "Field is not described here; the server decides rejection or pruning.")
				}
			}
		case []any:
			if len(v) > MaxWork-r.Checked {
				r.Partial = true
				return
			}
			child, ok := n["items"].(map[string]any)
			if !ok {
				if len(v) > 0 {
					r.Partial = true
				}
				return
			}
			for i, x := range v {
				if r.Checked >= MaxWork || len(r.Diagnostics) >= MaxDiagnostics {
					r.Partial = true
					break
				}
				visit(child, x, path+"/"+strconv.Itoa(i), depth+1)
			}
		}
	}
	visit(d.root, obj, "", 0)
	if ctx.Err() != nil {
		return Report{}, ctx.Err()
	}
	return r, nil
}
