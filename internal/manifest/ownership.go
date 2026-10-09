package manifest

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const MaxOwnershipBytes = 24 << 10

// Ownership describes the actual managedFields field set, not inferred ownership
// of a value. It never traverses the resource payload. Atomic fields/list keys
// retain their Kubernetes FieldsV1 notation; this is not a JSON pointer editor.
// Bounds are display/processing limits; the original resource is already read.
func Ownership(obj *unstructured.Unstructured) string {
	if obj == nil {
		return "No resource loaded."
	}
	value, found, err := unstructured.NestedFieldNoCopy(obj.Object, "metadata", "managedFields")
	raw, valid := value.([]interface{})
	if err != nil || (found && !valid) {
		return "Field ownership metadata is malformed; not displayed."
	}
	if !found || len(raw) == 0 {
		return "No field ownership metadata returned by this API."
	}
	var out strings.Builder
	remaining := 256
	truncated := false
	appendLine := func(s string) bool {
		if out.Len()+len(s)+1 > MaxOwnershipBytes {
			truncated = true
			return false
		}
		out.WriteString(s)
		out.WriteByte('\n')
		return true
	}
	clean := func(s string) string {
		if len(s) > 512 {
			s = s[:512] + "…"
		}
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return '�'
			}
			return r
		}, s)
	}
	for i, item := range raw {
		if i >= 64 {
			truncated = true
			break
		}
		entry, ok := item.(map[string]interface{})
		if !ok {
			appendLine("Malformed manager entry.")
			continue
		}
		value := func(key string) string { s, _ := entry[key].(string); return clean(s) }
		scope := value("subresource")
		if scope == "" {
			scope = "main resource"
		}
		if !appendLine(fmt.Sprintf("%s · %s · %s · %s", value("manager"), value("operation"), value("apiVersion"), scope)) {
			break
		}
		if value("fieldsType") != "FieldsV1" {
			appendLine("  Unsupported or missing field-set format.")
			continue
		}
		fields, ok := entry["fieldsV1"].(map[string]interface{})
		if !ok {
			appendLine("  Missing/malformed field set.")
			continue
		}
		var walk func(map[string]interface{}, string, int)
		walk = func(m map[string]interface{}, prefix string, depth int) {
			if remaining <= 0 || out.Len() >= MaxOwnershipBytes {
				truncated = true
				return
			}
			if depth > 32 {
				truncated = true
				return
			}
			if len(m) == 0 {
				remaining--
				appendLine("  " + prefix)
				return
			}
			if len(m) > 1024 {
				truncated = true
				return
			}
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if remaining <= 0 {
					truncated = true
					return
				}
				path := prefix + "/" + clean(k)
				if len(path) > 2048 {
					truncated = true
					continue
				}
				next, ok := m[k].(map[string]interface{})
				if !ok {
					appendLine("  Malformed field-set node.")
					remaining--
					continue
				}
				walk(next, path, depth+1)
			}
		}
		walk(fields, "", 0)
		if remaining <= 0 || out.Len() >= MaxOwnershipBytes {
			break
		}
	}
	if truncated {
		const message = "\n[Field ownership display truncated by entry/path/depth/byte budget]\n"
		text := out.String()
		if len(text)+len(message) > MaxOwnershipBytes {
			text = text[:MaxOwnershipBytes-len(message)]
		}
		return strings.ToValidUTF8(text+message, "?")
	}
	return out.String()
}

func OwnershipReview(before, after []byte) (string, error) {
	if len(before) > MaxBytes || len(after) > MaxBytes {
		return "", fmt.Errorf("ownership preview exceeds document budget")
	}
	var old, next unstructured.Unstructured
	if err := json.Unmarshal(before, &old.Object); err != nil {
		return "", err
	}
	if err := json.Unmarshal(after, &next.Object); err != nil {
		return "", err
	}
	return "\nFIELD OWNERSHIP — BEFORE\n" + Ownership(&old) + "\nFIELD OWNERSHIP — AFTER SERVER DRY-RUN\n" + Ownership(&next), nil
}
