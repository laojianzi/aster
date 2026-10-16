package helmrelease

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/laojianzi/aster/internal/manifest"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

const (
	MaxPreviewObjects  = 32
	MaxPreviewSegments = 128
	MaxPreviewNewlines = 40000
	PreviewLifetime    = 2 * time.Minute
)

var (
	ErrPreviewSession     = errors.New("Helm scope preview requires the current workspace identity.")
	ErrPreviewInvalid     = errors.New("Stored Helm manifest identities are ambiguous or unsupported; no partial preview is shown.")
	ErrPreviewBudget      = errors.New("Stored Helm manifests exceed the bounded preview budget; no partial preview is shown.")
	ErrPreviewUnavailable = errors.New("Helm scope preview could not be created; no write was attempted.")
)

// UninstallItem is an observation from storage, NOT a resolved live target.
// Namespace is only what the manifest declares. Empty means unresolved, not
// cluster-scoped. Order is source order, NOT Helm deletion order. Payloads and
// arbitrary annotations never escape. Booleans describe observed risk markers.
type UninstallItem struct {
	APIVersion, Kind, Namespace, Name string
	Keep, Hook, CRD, ForeignNamespace bool
}

// UninstallPreview is an immutable, nonexecutable, expiring storage observation.
// It does not contain manifest bytes or an operation.Prepared and cannot submit,
// import, dry-run or authorize a mutation. No public accessor returns aliases.
type UninstallPreview struct {
	id, session, namespace string
	revision               Revision
	created, expires       time.Time
	summary                Inspection
	items                  []UninstallItem
}

func (p *UninstallPreview) ID() string {
	if p == nil {
		return ""
	}
	return p.id
}
func (p *UninstallPreview) Revision() Revision {
	if p == nil {
		return Revision{}
	}
	return p.revision
}
func (p *UninstallPreview) Namespace() string {
	if p == nil {
		return ""
	}
	return p.namespace
}
func (p *UninstallPreview) Summary() Inspection {
	if p == nil {
		return Inspection{}
	}
	return p.summary
}
func (p *UninstallPreview) ExpiresAt() time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.expires
}
func (p *UninstallPreview) Items() []UninstallItem {
	if p == nil {
		return nil
	}
	return append([]UninstallItem(nil), p.items...)
}

// Matches binds the observation to a workspace and exact selected object. It
// only bounds local lifetime; it does not prove the cluster is unchanged.
func (p *UninstallPreview) Matches(session, ns string, row Revision, now time.Time) bool {
	return p != nil && p.id != "" && p.session == session && p.namespace == ns && p.revision == row &&
		!now.Before(p.created) && now.Before(p.expires)
}

func ValidPreviewSession(s string) bool {
	if s == "" || len(s) > 256 || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

// PreviewUninstallSecret reuses the strict, bounded inspection decoder. Only
// the inert, owned projection survives this call. Even a marker-free inventory
// is not a dry run: latest revision, discovery, live UID/ownership, dependents,
// hook execution, storage history, CRDs outside the manifest and admission are
// deliberately NOT inferred. The caller must obtain sensitive-read consent.
func PreviewUninstallSecret(ctx context.Context, data []byte, ns string, row Revision, session string, now time.Time) (*UninstallPreview, error) {
	if !ValidPreviewSession(session) || now.IsZero() {
		return nil, ErrPreviewSession
	}
	var items []UninstallItem
	summary, err := inspectSecret(ctx, data, ns, row, func(_ Inspection, text string) error {
		var err error
		items, err = previewItems(ctx, text, ns)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, ErrPreviewUnavailable
	}
	// ID is random, not an exposed digest of potentially low-entropy secrets.
	return &UninstallPreview{id: hex.EncodeToString(id[:]), session: session, namespace: ns,
		revision: row, created: now, expires: now.Add(PreviewLifetime), summary: summary, items: items}, nil
}

func previewItems(ctx context.Context, text, ns string) ([]UninstallItem, error) {
	if len(text) > MaxManifestBytes || strings.Count(text, "\n") > MaxPreviewNewlines {
		return nil, ErrPreviewBudget
	}
	// Validate before the Kubernetes document reader, including comment-only
	// segments. The parser's documented synchronous byte/line limits still apply.
	if !utf8.ValidString(text) {
		return nil, ErrPreviewInvalid
	}
	for i, c := range text {
		if c == '\ufeff' || c == '\u0085' || c == '\u2028' || c == '\u2029' ||
			(c == '\r' && (i+1 == len(text) || text[i+1] != '\n')) {
			return nil, ErrPreviewInvalid
		}
	}
	reader := utilyaml.NewYAMLReader(bufio.NewReader(strings.NewReader(text)))
	items := []UninstallItem{}
	seen := map[string]bool{}
	for segments := 0; ; segments++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrPreviewInvalid
		}
		if segments >= MaxPreviewSegments {
			return nil, ErrPreviewBudget
		}
		if onlyComments(data) {
			continue
		}
		if len(items) >= MaxPreviewObjects || len(data) > manifest.MaxBytes || bytes.Count(data, []byte{'\n'}) > 5000 {
			return nil, ErrPreviewBudget
		}
		index, err := manifest.IndexSource(ctx, data)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil || index.Partial() {
			return nil, ErrPreviewInvalid
		}
		obj, err := manifest.Decode(data)
		if err != nil {
			return nil, ErrPreviewInvalid
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		kind, name, api := obj.GetKind(), obj.GetName(), obj.GetAPIVersion()
		gv, err := schema.ParseGroupVersion(api)
		if err != nil || len(validation.IsDNS1123Label(gv.Version)) != 0 ||
			(gv.Group != "" && len(validation.IsDNS1123Subdomain(gv.Group)) != 0) ||
			!safeKind(kind) || kind == "List" || len(validation.IsDNS1123Subdomain(name)) != 0 {
			return nil, ErrPreviewInvalid
		}
		if _, list := obj.Object["items"]; list {
			return nil, ErrPreviewInvalid
		}
		meta, ok := obj.Object["metadata"].(map[string]interface{})
		if !ok {
			return nil, ErrPreviewInvalid
		}
		if _, generated := meta["generateName"]; generated {
			return nil, ErrPreviewInvalid
		}
		item := UninstallItem{APIVersion: api, Kind: kind, Name: name, CRD: gv.Group == "apiextensions.k8s.io" && kind == "CustomResourceDefinition"}
		if value, exists := meta["namespace"]; exists {
			declared, ok := value.(string)
			if !ok || !ValidNamespace(declared) {
				return nil, ErrPreviewInvalid
			}
			item.Namespace, item.ForeignNamespace = declared, declared != ns
		}
		if value, exists := meta["annotations"]; exists {
			annotations, ok := value.(map[string]interface{})
			if !ok {
				return nil, ErrPreviewInvalid
			}
			for key, value := range annotations {
				v, ok := value.(string)
				if !ok || len(validation.IsQualifiedName(key)) != 0 {
					return nil, ErrPreviewInvalid
				}
				if strings.HasPrefix(key, "helm.sh/hook") {
					item.Hook = true
				}
				if key == "helm.sh/resource-policy" {
					// Do not guess how unknown/case-variant policy strings are handled.
					if v != "keep" {
						return nil, ErrPreviewInvalid
					}
					item.Keep = true
				}
			}
		}
		// Different API versions may address one stored object. No GVR/ownership
		// claim is made without discovery; obvious GVK aliases are still rejected.
		identityNS := item.Namespace
		if identityNS == "" {
			identityNS = ns
		} // Reject potential omitted/default aliases, without claiming resolved scope.
		key := gv.Group + "\x00" + kind + "\x00" + identityNS + "\x00" + name
		if seen[key] {
			return nil, ErrPreviewInvalid
		}
		seen[key] = true
		items = append(items, item)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return items, nil // An empty manifest does NOT mean an empty Helm uninstall.
}

func onlyComments(data []byte) bool {
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) != 0 && line[0] != '#' {
			return false
		}
	}
	return true
}
func safeKind(s string) bool {
	if len(s) == 0 || len(s) > 63 {
		return false
	}
	for i, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
