package manifest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type ChangeKind string

const (
	Added    ChangeKind = "added"
	Removed  ChangeKind = "removed"
	Modified ChangeKind = "modified"
)

type Change struct {
	Path   string
	Kind   ChangeKind
	Before string
	After  string
}

var volatileMetadata = map[string]struct{}{
	"creationTimestamp": {},
	"generation":        {},
	"managedFields":     {},
	"resourceVersion":   {},
	"uid":               {},
}

func Diff(before, after map[string]any) ([]Change, error) {
	left := normalize(before)
	right := normalize(after)
	lf := map[string]any{}
	rf := map[string]any{}
	flatten("", left, lf)
	flatten("", right, rf)

	keys := make(map[string]struct{}, len(lf)+len(rf))
	for k := range lf {
		keys[k] = struct{}{}
	}
	for k := range rf {
		keys[k] = struct{}{}
	}
	paths := make([]string, 0, len(keys))
	for k := range keys {
		paths = append(paths, k)
	}
	sort.Strings(paths)

	secret := kindOf(left) == "Secret" || kindOf(right) == "Secret"
	out := make([]Change, 0)
	for _, path := range paths {
		lv, lok := lf[path]
		rv, rok := rf[path]
		if lok && rok && reflect.DeepEqual(lv, rv) {
			continue
		}
		change := Change{Path: path}
		switch {
		case !lok:
			change.Kind = Added
		case !rok:
			change.Kind = Removed
		default:
			change.Kind = Modified
		}
		if secret && isSecretValuePath(path) {
			if lok {
				change.Before = "***"
			}
			if rok {
				change.After = "***"
			}
		} else {
			var err error
			if lok {
				change.Before, err = render(lv)
				if err != nil {
					return nil, fmt.Errorf("render %s before: %w", path, err)
				}
			}
			if rok {
				change.After, err = render(rv)
				if err != nil {
					return nil, fmt.Errorf("render %s after: %w", path, err)
				}
			}
		}
		out = append(out, change)
	}
	return out, nil
}

func normalize(src map[string]any) map[string]any {
	dst := deepCopyMap(src)
	if metadata, ok := dst["metadata"].(map[string]any); ok {
		for key := range volatileMetadata {
			delete(metadata, key)
		}
	}
	return dst
}

func deepCopyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepCopy(v)
	}
	return dst
}

func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return deepCopyMap(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = deepCopy(x[i])
		}
		return out
	default:
		return x
	}
}

func flatten(path string, value any, out map[string]any) {
	switch x := value.(type) {
	case map[string]any:
		if len(x) == 0 {
			out[pathOrRoot(path)] = x
			return
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			flatten(path+"/"+escape(k), x[k], out)
		}
	case []any:
		// Lists are kept atomic. Kubernetes list merge semantics depend on schema,
		// so pretending index-based edits are semantic would be misleading.
		out[pathOrRoot(path)] = x
	default:
		out[pathOrRoot(path)] = x
	}
}

func escape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func pathOrRoot(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

func kindOf(v map[string]any) string {
	s, _ := v["kind"].(string)
	return s
}

func isSecretValuePath(path string) bool {
	return path == "/data" || strings.HasPrefix(path, "/data/") ||
		path == "/stringData" || strings.HasPrefix(path, "/stringData/")
}

func render(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
