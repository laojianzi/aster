package helmrelease

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	strictjson "sigs.k8s.io/json"
)

const (
	MaxSecretBytes     = 2 << 20
	MaxStoredBytes     = 1 << 20
	MaxCompressedBytes = 768 << 10
	MaxReleaseBytes    = 4 << 20
	MaxManifestBytes   = 2 << 20
	MaxJSONDepth       = 64
	MaxJSONTokens      = 100000
	MaxHooks           = 128
	MaxValuesKeys      = 1024
)

var (
	ErrInspectionConsent     = errors.New("Explicit acknowledgement is required before reading this revision's Secret content.")
	ErrInspectionSelection   = errors.New("Select a revision from a fresh metadata inventory before inspecting content.")
	ErrInspectionStale       = errors.New("The selected revision changed or disappeared; refresh metadata and select again.")
	ErrInspectionForbidden   = errors.New("The current identity cannot read this revision's Secret content (get secrets required).")
	ErrInspectionUnavailable = errors.New("Helm revision content could not be inspected; no retry, fallback or write was attempted.")
	ErrInspectionInvalid     = errors.New("Helm revision content is malformed or inconsistent; no content is shown.")
	ErrInspectionBudget      = errors.New("Helm revision content exceeded the bounded inspection budget; no content is shown.")
	ErrInspectionUnsupported = errors.New("This Helm storage representation is unsupported; only a single gzip-encoded Secret-driver revision is inspected.")
)

// Inspection retains only a small validated projection. Raw values, manifests,
// notes, chart templates and hook contents never escape the decoder. Consistency
// between editable fields is NOT authenticity, readiness, ownership or approval.
type Inspection struct {
	ChartName      string
	ChartVersion   string
	ManifestBytes  int
	HookCount      int
	SuppliedValues int
}

func ValidSelection(ns string, row Revision) bool {
	return ValidNamespace(ns) && len(row.Release) > 0 && len(row.Release) <= 53 &&
		len(validation.IsDNS1123Subdomain(row.Release)) == 0 && row.Number > 0 && knownStatus(row.Status) &&
		row.StorageName == "sh.helm.release.v1."+row.Release+".v"+strconv.FormatInt(int64(row.Number), 10) &&
		row.UID != "" && len(row.UID) <= 128 && row.ResourceVersion != "" && len(row.ResourceVersion) <= 256
}

// InspectSecret consumes one bounded JSON Secret response. Callers must bind the
// selected object to their current identity/lifetime and obtain consent before
// fetching it. Byte slices are cleared best-effort, not a Go/OS erasure promise.
func InspectSecret(ctx context.Context, data []byte, ns string, selected Revision) (Inspection, error) {
	empty := Inspection{}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if !ValidSelection(ns, selected) {
		return empty, ErrInspectionSelection
	}
	if len(data) > MaxSecretBytes {
		return empty, ErrInspectionBudget
	}
	if err := checkJSON(ctx, data); err != nil {
		return empty, err
	}
	var secret struct {
		metav1.TypeMeta `json:",inline"`
		Metadata        metav1.ObjectMeta `json:"metadata"`
		Type            string            `json:"type"`
		Data            map[string]string `json:"data"`
		Immutable       *bool             `json:"immutable,omitempty"`
	}
	issues, err := strictjson.UnmarshalStrict(data, &secret)
	if err != nil || len(issues) != 0 {
		return empty, ErrInspectionInvalid
	}
	if secret.APIVersion != "v1" || secret.Kind != "Secret" || secret.Type != "helm.sh/release.v1" {
		return empty, ErrInspectionUnsupported
	}
	m := secret.Metadata
	if m.Namespace != ns || m.Name != selected.StorageName || string(m.UID) != selected.UID || m.ResourceVersion != selected.ResourceVersion || m.DeletionTimestamp != nil ||
		m.Labels["owner"] != "helm" || m.Labels["name"] != selected.Release || m.Labels["version"] != strconv.FormatInt(int64(selected.Number), 10) || m.Labels["status"] != selected.Status {
		return empty, ErrInspectionStale
	}
	if len(secret.Data) != 1 || secret.Data["release"] == "" {
		return empty, ErrInspectionInvalid
	}
	stored, err := decodeBase64(secret.Data["release"], MaxStoredBytes)
	if err != nil {
		return empty, err
	}
	defer clear(stored)
	compressed, err := decodeBase64(string(stored), MaxCompressedBytes)
	if err != nil {
		return empty, err
	}
	defer clear(compressed)
	if len(compressed) < 3 || !bytes.Equal(compressed[:3], []byte{0x1f, 0x8b, 0x08}) {
		return empty, ErrInspectionUnsupported
	}
	source := bytes.NewReader(compressed)
	gz, err := gzip.NewReader(contextBytes{ctx, source})
	if err != nil {
		return empty, privateDecodeError(ctx)
	}
	gz.Multistream(false)
	payload, err := io.ReadAll(io.LimitReader(gz, MaxReleaseBytes+1))
	_ = gz.Close()
	defer clear(payload)
	if ctx.Err() != nil {
		return empty, ctx.Err()
	}
	if len(payload) > MaxReleaseBytes {
		return empty, ErrInspectionBudget
	}
	if err != nil || source.Len() != 0 {
		return empty, ErrInspectionInvalid
	}
	if err := checkJSON(ctx, payload); err != nil {
		return empty, err
	}
	if !canonicalPayload(payload) {
		return empty, ErrInspectionInvalid
	}
	// Unknown release fields are not interpreted, but all of their JSON was
	// checked for ambiguity and budgets before this minimal typed projection.
	var release struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
		Version   int32  `json:"version"`
		Info      *struct {
			Status string `json:"status"`
		} `json:"info"`
		Chart *struct {
			Metadata *struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"metadata"`
		} `json:"chart"`
		Manifest string                     `json:"manifest"`
		Config   map[string]json.RawMessage `json:"config"`
		Hooks    []json.RawMessage          `json:"hooks"`
	}
	if err := strictjson.UnmarshalCaseSensitivePreserveInts(payload, &release); err != nil {
		return empty, ErrInspectionInvalid
	}
	if release.Name != selected.Release || release.Namespace != ns || release.Version != selected.Number || release.Info == nil || release.Info.Status != selected.Status || release.Chart == nil || release.Chart.Metadata == nil {
		return empty, ErrInspectionInvalid
	}
	chart := release.Chart.Metadata
	if chart.Name == "" || len(chart.Name) > 63 || len(validation.IsDNS1123Subdomain(chart.Name)) != 0 || !safeVersion(chart.Version) {
		return empty, ErrInspectionInvalid
	}
	if len(release.Manifest) > MaxManifestBytes || len(release.Hooks) > MaxHooks || len(release.Config) > MaxValuesKeys {
		return empty, ErrInspectionBudget
	}
	for _, hook := range release.Hooks {
		if len(hook) == 0 || hook[0] != '{' {
			return empty, ErrInspectionInvalid
		}
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return Inspection{ChartName: chart.Name, ChartVersion: chart.Version, ManifestBytes: len(release.Manifest), HookCount: len(release.Hooks), SuppliedValues: len(release.Config)}, nil
}

func safeVersion(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '.' || c == '-' || c == '+') {
			return false
		}
	}
	return true // Display allowlist only, not semver or chart-schema validation.
}
func decodeBase64(s string, limit int) ([]byte, error) {
	if len(s) > base64.StdEncoding.EncodedLen(limit) {
		return nil, ErrInspectionBudget
	}
	if len(s)%4 != 0 || strings.ContainsAny(s, "\r\n") {
		return nil, ErrInspectionInvalid
	}
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil {
		clear(b)
		return nil, ErrInspectionInvalid
	}
	if len(b) > limit {
		clear(b)
		return nil, ErrInspectionBudget
	}
	return b, nil
}

type contextBytes struct {
	ctx    context.Context
	reader *bytes.Reader
}

func (r contextBytes) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func (r contextBytes) ReadByte() (byte, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.ReadByte()
}
func privateDecodeError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrInspectionInvalid
}

// checkJSON checks ALL fields, including ignored nested fields. A duplicate or
// exhausted budget late in the document invalidates even an early valid identity.
func checkJSON(ctx context.Context, data []byte) error {
	if !utf8.Valid(data) {
		return ErrInspectionInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	remaining := MaxJSONTokens
	token := func() (json.Token, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining--
		if remaining < 0 {
			return nil, ErrInspectionBudget
		}
		v, err := d.Token()
		if err != nil {
			return nil, ErrInspectionInvalid
		}
		return v, nil
	}
	var value func(int) error
	value = func(depth int) error {
		if depth > MaxJSONDepth {
			return ErrInspectionBudget
		}
		t, err := token()
		if err != nil {
			return err
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, err := token()
				if err != nil {
					return err
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return ErrInspectionInvalid
				}
				seen[key] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			t, err = token()
			if err != nil {
				return err
			}
			if t != json.Delim('}') {
				return ErrInspectionInvalid
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			t, err = token()
			if err != nil {
				return err
			}
			if t != json.Delim(']') {
				return ErrInspectionInvalid
			}
		default:
			return ErrInspectionInvalid
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInspectionInvalid
	}
	return ctx.Err()
}

// Unknown future fields remain inert; case aliases of interpreted identity
// fields are rejected, while values/config keys remain case-sensitive.
func canonicalObject(data []byte, names ...string) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return nil, false
	}
	for key := range fields {
		for _, name := range names {
			if key != name && strings.EqualFold(key, name) {
				return nil, false
			}
		}
	}
	return fields, true
}
func canonicalPayload(data []byte) bool {
	root, ok := canonicalObject(data, "name", "namespace", "version", "info", "chart", "manifest", "config", "hooks")
	if !ok {
		return false
	}
	if _, ok := canonicalObject(root["info"], "status"); !ok {
		return false
	}
	chart, ok := canonicalObject(root["chart"], "metadata")
	if !ok {
		return false
	}
	_, ok = canonicalObject(chart["metadata"], "name", "version")
	return ok
}
