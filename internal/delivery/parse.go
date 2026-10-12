package delivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strings"
	"unicode/utf8"

	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/manifest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Parse imports already-rendered manifests. It performs no discovery or cluster
// requests; kinds must come from fresh discovery for this session. The caller
// explicitly selects and reviews an existing namespace before importing.
func Parse(ctx context.Context, data []byte, sessionID, namespace string, kinds []kube.ResourceKind) (*Set, error) {
	if ctx == nil {
		return nil, errors.New("manifest set: missing context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > MaxBytes {
		return nil, errors.New("manifest set: input must contain at most 2 MiB")
	}
	if bytes.Count(data, []byte{'\n'}) > MaxNewlines {
		return nil, errors.New("manifest set: input exceeds the total line budget")
	}
	if sessionID == "" || len(sessionID) > 256 || !utf8.ValidString(sessionID) {
		return nil, errors.New("manifest set: invalid session")
	}
	if len(validation.IsDNS1123Label(namespace)) != 0 {
		return nil, errors.New("manifest set: select one valid namespace")
	}
	if len(kinds) > MaxKinds {
		return nil, errors.New("manifest set: discovery exceeds the kind budget")
	}
	if !utf8.Valid(data) {
		return nil, errors.New("manifest set: input is not UTF-8")
	}
	for i := 0; i < len(data); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, size := utf8.DecodeRune(data[i:])
		if r == '\ufeff' || r == '\u0085' || r == '\u2028' || r == '\u2029' || (r == '\r' && (i+1 == len(data) || data[i+1] != '\n')) {
			return nil, errors.New("manifest set: unsupported text encoding or line-ending form")
		}
		i += size
	}
	owned := bytes.Clone(data)
	discovery := append([]kube.ResourceKind(nil), kinds...)
	s := &Set{sessionID: sessionID, namespace: namespace}
	seen := make(map[string]struct{})
	err := scan(ctx, owned, func(segment []byte) error {
		if commentOnly(segment) {
			return nil
		}
		n := len(s.documents) + 1
		if n > MaxObjects {
			return invalid(n, "more than 32 objects")
		}
		doc, err := parseDocument(ctx, segment, namespace, discovery, n)
		if err != nil {
			return err
		}
		// Versions alias the same stored resource identity and cannot appear twice.
		key := doc.item.Kind.GVR.Group + "\x00" + doc.item.Kind.GVR.Resource + "\x00" + namespace + "\x00" + doc.item.Name
		if _, exists := seen[key]; exists {
			return invalid(n, "duplicate resource identity")
		}
		seen[key] = struct{}{}
		s.documents = append(s.documents, doc)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.Len() == 0 {
		return nil, errors.New("manifest set: no objects")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h := sha256.New()
	writeIdentity(h, []byte("aster-reviewed-manifest-set-v1"))
	writeIdentity(h, []byte(sessionID))
	writeIdentity(h, []byte(namespace))
	writeIdentity(h, owned)
	for _, d := range s.documents {
		for _, value := range []string{d.item.Kind.GVR.Group, d.item.Kind.GVR.Version, d.item.Kind.GVR.Resource, d.item.Kind.Kind, "namespaced", d.item.Namespace, d.item.Name} {
			writeIdentity(h, []byte(value))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.id = hex.EncodeToString(h.Sum(nil))
	return s, nil
}

func writeIdentity(h hash.Hash, data []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(data)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(data)
}

func invalid(document int, reason string) error {
	// Reasons at all call sites are fixed text. Parser messages can contain
	// supplied scalar values, annotation contents or source fragments.
	return fmt.Errorf("manifest set document %d: %s", document, reason)
}

// scan recognizes the same column-zero document separator used by Kubernetes'
// YAML reader. It never splits an indented block scalar, bounds even empty
// segments, and checks cancellation while walking the bounded original bytes.
func scan(ctx context.Context, data []byte, visit func([]byte) error) error {
	start, offset, segments := 0, 0, 1
	for offset < len(data) {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := bytes.IndexByte(data[offset:], '\n')
		if end < 0 {
			end = len(data)
		} else {
			end += offset + 1
		}
		line := data[offset:end]
		if bytes.HasPrefix(line, []byte("---")) {
			tail := bytes.Trim(line[3:], " \t\r\n")
			if len(tail) != 0 && tail[0] != '#' {
				return errors.New("manifest set: invalid document separator")
			}
			if err := visit(data[start:offset]); err != nil {
				return err
			}
			segments++
			if segments > MaxSegments {
				return errors.New("manifest set: document scan exceeds the segment budget")
			}
			start = end
		}
		offset = end
	}
	return visit(data[start:])
}

func commentOnly(data []byte) bool {
	for len(data) != 0 {
		line, rest, found := bytes.Cut(data, []byte{'\n'})
		line = bytes.Trim(line, " \t\r")
		if len(line) != 0 && line[0] != '#' {
			return false
		}
		if !found {
			break
		}
		data = rest
	}
	return true
}

func parseDocument(ctx context.Context, data []byte, namespace string, kinds []kube.ResourceKind, n int) (document, error) {
	if err := ctx.Err(); err != nil {
		return document{}, err
	}
	// Reuse the bounded AST inspection without expanding aliases or merge keys.
	// Only explicit unambiguous object structure reaches the semantic decoder.
	index, err := manifest.IndexSource(ctx, data)
	if err != nil || index.Partial() {
		if ctx.Err() != nil {
			return document{}, ctx.Err()
		}
		return document{}, invalid(n, "unsupported, ambiguous or oversized object structure")
	}
	obj, err := manifest.Decode(data)
	if err != nil {
		return document{}, invalid(n, "invalid or oversized single object")
	}
	if err := ctx.Err(); err != nil {
		return document{}, err
	}
	if obj.GetKind() == "Secret" || obj.GetKind() == "List" {
		return document{}, invalid(n, "Secret and list documents are unsupported")
	}
	gv, err := schema.ParseGroupVersion(obj.GetAPIVersion())
	if err != nil || gv.Version == "" {
		return document{}, invalid(n, "invalid resource type")
	}
	var selected kube.ResourceKind
	matches := 0
	for _, kind := range kinds {
		if err := ctx.Err(); err != nil {
			return document{}, err
		}
		if kind.Kind == obj.GetKind() && kind.GVR.GroupVersion() == gv {
			selected = kind
			matches++
		}
	}
	if matches != 1 {
		return document{}, invalid(n, "unknown or ambiguous resource type")
	}
	if !selected.Namespaced {
		return document{}, invalid(n, "cluster-scoped resources are unsupported")
	}
	if selected.GVR.Resource == "secrets" || len(validation.IsDNS1123Subdomain(selected.GVR.Resource)) != 0 || len(validation.IsDNS1123Label(gv.Version)) != 0 || (gv.Group != "" && len(validation.IsDNS1123Subdomain(gv.Group)) != 0) {
		return document{}, invalid(n, "unsupported discovered resource type")
	}
	if err := metadata(obj, namespace); err != nil {
		return document{}, invalid(n, err.Error())
	}
	obj.SetNamespace(namespace)
	payload, err := obj.MarshalJSON()
	if err != nil || len(payload) > manifest.MaxBytes {
		return document{}, invalid(n, "resolved object exceeds the byte budget")
	}
	if err := ctx.Err(); err != nil {
		return document{}, err
	}
	return document{item: Item{Kind: selected, Namespace: namespace, Name: obj.GetName()}, payload: bytes.Clone(payload)}, nil
}

func metadata(obj *unstructured.Unstructured, namespace string) error {
	if _, exists := obj.Object["status"]; exists {
		return errors.New("status is unsupported")
	}
	meta, ok := obj.Object["metadata"].(map[string]interface{})
	if !ok {
		return errors.New("invalid metadata")
	}
	for key, value := range meta {
		switch key {
		case "name":
			name, ok := value.(string)
			if !ok || name == "" || len(name) > 253 || name == "." || name == ".." || strings.ContainsAny(name, "/%\x00\r\n\t") {
				return errors.New("invalid resource name")
			}
		case "namespace":
			ns, ok := value.(string)
			if !ok || ns != namespace {
				return errors.New("namespace differs from the reviewed target")
			}
		case "labels", "annotations":
			values, ok := value.(map[string]interface{})
			if !ok {
				return errors.New("labels and annotations must contain strings")
			}
			for name, value := range values {
				text, ok := value.(string)
				if !ok || len(validation.IsQualifiedName(name)) != 0 || (key == "labels" && len(validation.IsValidLabelValue(text)) != 0) {
					return errors.New("invalid label or annotation")
				}
				if key == "annotations" && lifecycleAnnotation(name) {
					return errors.New("workflow or lifecycle annotations are unsupported")
				}
			}
		default:
			return errors.New("server-owned or lifecycle metadata is unsupported")
		}
	}
	return nil
}

func lifecycleAnnotation(name string) bool {
	if name == "kubectl.kubernetes.io/last-applied-configuration" {
		return true
	}
	if strings.HasPrefix(name, "helm.sh/hook") || name == "helm.sh/resource-policy" || strings.HasPrefix(name, "meta.helm.sh/") {
		return true
	}
	for _, prefix := range []string{"argocd.argoproj.io/", "kustomize.toolkit.fluxcd.io/", "helm.toolkit.fluxcd.io/", "reconcile.fluxcd.io/", "fluxcd.io/"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
