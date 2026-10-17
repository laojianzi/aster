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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StatusStep describes only a storage encoding transition, never permission to
// execute it. Neither an Inspection nor this enum is a reviewed capability.
type StatusStep uint8

const (
	BeginUninstallStatus StatusStep = iota + 1
	FinishUninstallStatus
)

var ErrStatusTransition = errors.New("Helm status transition is invalid or unsupported; no patch was produced.")

// BuildUninstallStatusPatch returns SENSITIVE application/json-patch+json bytes.
// It performs no requests. The caller must clear the returned bytes after use;
// they contain the entire encoded release and must not enter logs/UI/exports.
//
// This codec is NOT eligibility, authorization, a dry-run, a lock or a completed
// uninstall. A future executor must separately prove its restricted profile,
// perform server dry-run, obtain independent single-use confirmation and verify
// every effect. The finish step must NEVER be called just because a CLI exited.
// Only the status/description/deleted payload fields and status/modifiedAt Secret
// labels change. All other uncompressed JSON bytes are retained exactly.
func BuildUninstallStatusPatch(ctx context.Context, data []byte, ns string, row Revision, step StatusStep, now time.Time) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now = now.UTC()
	from, to, description := "", "", ""
	switch step {
	case BeginUninstallStatus:
		from, to, description = "deployed", "uninstalling", "Deletion in progress (or silently failed)"
	case FinishUninstallStatus:
		from, to, description = "uninstalling", "uninstalled", "Uninstallation complete"
	default:
		return nil, ErrStatusTransition
	}
	if now.IsZero() || now.Year() < 1970 || now.Year() > 9999 || row.Status != from {
		return nil, ErrStatusTransition
	}
	var result []byte
	_, err := inspectSecretPayload(ctx, data, ns, row, func(_ Inspection, _ string, payload []byte) error {
		// Inspection already validated the complete wire; reject extra lifecycle
		// constraints here without broadening the existing read-only inspector.
		var storage struct {
			Metadata  metav1.ObjectMeta `json:"metadata"`
			Immutable *bool             `json:"immutable"`
		}
		if json.Unmarshal(data, &storage) != nil || (storage.Immutable != nil && *storage.Immutable) ||
			len(storage.Metadata.Finalizers) != 0 || len(storage.Metadata.OwnerReferences) != 0 {
			return ErrStatusTransition
		}
		root, ok := canonicalObject(payload, "info")
		if !ok {
			return ErrStatusTransition
		}
		info, ok := canonicalObject(root["info"], "status", "description", "deleted")
		if !ok {
			return ErrStatusTransition
		}
		if raw, exists := info["description"]; exists {
			var description string
			if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &description) != nil {
				return ErrStatusTransition
			}
		}
		var deleted time.Time
		if raw, exists := info["deleted"]; exists {
			if len(raw) > 128 || json.Unmarshal(raw, &deleted) != nil {
				return ErrStatusTransition
			}
		}
		if step == BeginUninstallStatus && !deleted.IsZero() ||
			step == FinishUninstallStatus && (deleted.IsZero() || deleted.Year() < 1970 || deleted.After(now)) {
			return ErrStatusTransition
		}
		changes := []struct{ key, value string }{{"status", to}, {"description", description}}
		if step == BeginUninstallStatus {
			changes = append(changes, struct{ key, value string }{"deleted", now.UTC().Format(time.RFC3339Nano)})
		}
		updatedInfo := bytes.Clone(root["info"])
		defer func() { clear(updatedInfo) }()
		for _, change := range changes {
			value, _ := json.Marshal(change.value)
			next, err := replaceStatusMember(ctx, updatedInfo, change.key, value)
			clear(updatedInfo)
			if err != nil {
				return err
			}
			updatedInfo = next
		}
		updated, err := replaceStatusMember(ctx, payload, "info", updatedInfo)
		if err != nil {
			return err
		}
		defer clear(updated)
		if err := checkJSON(ctx, updated); err != nil {
			return err
		}
		encoded, err := encodeStatusPayload(ctx, updated)
		if err != nil {
			return err
		}
		defer clear(encoded)
		// JSON Patch tests execute atomically on the server with the mutations.
		// RV is opaque, never parsed or guessed. No update-or-create or purge.
		ops := []statusPatchOperation{
			{"test", "/metadata/name", row.StorageName},
			{"test", "/metadata/namespace", ns},
			{"test", "/metadata/uid", row.UID},
			{"test", "/metadata/resourceVersion", row.ResourceVersion},
			{"test", "/metadata/labels/status", from},
			{"replace", "/metadata/labels/status", to},
			{"add", "/metadata/labels/modifiedAt", strconv.FormatInt(now.Unix(), 10)},
			{"replace", "/data/release", base64.StdEncoding.EncodeToString(encoded)},
		}
		result, err = json.Marshal(ops)
		if err != nil {
			return ErrStatusTransition
		}
		if len(result) > MaxSecretBytes {
			return ErrInspectionBudget
		}
		return ctx.Err()
	})
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		clear(result)
		return nil, err
	}
	return result, nil
}

type statusPatchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value"`
}

// replaceStatusMember splices one object value or appends a missing member. It
// never reserializes other members, so number spelling, strings and whitespace
// in unknown fields remain intact. Complete ambiguity/budget validation is done
// before this private helper; the decoder is used only to find exact spans.
func replaceStatusMember(ctx context.Context, object []byte, key string, value []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(object))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrStatusTransition
	}
	start, end, members := -1, -1, 0
	for d.More() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := d.Token()
		if err != nil {
			return nil, ErrStatusTransition
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return nil, ErrStatusTransition
		}
		members++
		if token == key {
			if start >= 0 {
				clear(raw)
				return nil, ErrStatusTransition
			}
			end = int(d.InputOffset())
			start = end - len(raw)
			if start < 0 || !bytes.Equal(object[start:end], raw) {
				clear(raw)
				return nil, ErrStatusTransition
			}
		}
		clear(raw)
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return nil, ErrStatusTransition
	}
	closeAt := int(d.InputOffset()) - 1
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrStatusTransition
	}
	replacement := value
	if start < 0 {
		start, end = closeAt, closeAt
		quoted, _ := json.Marshal(key)
		replacement = make([]byte, 0, len(quoted)+len(value)+2)
		if members > 0 {
			replacement = append(replacement, ',')
		}
		replacement = append(replacement, quoted...)
		replacement = append(replacement, ':')
		replacement = append(replacement, value...)
		defer clear(replacement)
	}
	length := len(object) - (end - start) + len(replacement)
	if length > MaxReleaseBytes {
		return nil, ErrInspectionBudget
	}
	out := make([]byte, 0, length)
	out = append(out, object[:start]...)
	out = append(out, replacement...)
	out = append(out, object[end:]...)
	if err := ctx.Err(); err != nil {
		clear(out)
		return nil, err
	}
	return out, nil
}

// Recompression is deterministic and bounded, but the original compressed
// representation is not preserved. Preservation applies to unchanged JSON.
func encodeStatusPayload(ctx context.Context, payload []byte) ([]byte, error) {
	if len(payload) > MaxReleaseBytes {
		return nil, ErrInspectionBudget
	}
	buffer := statusCompressionBuffer{ctx: ctx}
	defer func() { clear(buffer.Bytes()) }()
	gz, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return nil, ErrStatusTransition
	}
	_, writeErr := io.Copy(gz, contextBytes{ctx, bytes.NewReader(payload)})
	closeErr := gz.Close()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if errors.Is(writeErr, ErrInspectionBudget) || errors.Is(closeErr, ErrInspectionBudget) {
		return nil, ErrInspectionBudget
	}
	if writeErr != nil || closeErr != nil {
		return nil, ErrStatusTransition
	}
	if base64.StdEncoding.EncodedLen(buffer.Len()) > MaxStoredBytes {
		return nil, ErrInspectionBudget
	}
	encoded := make([]byte, base64.StdEncoding.EncodedLen(buffer.Len()))
	base64.StdEncoding.Encode(encoded, buffer.Bytes())
	return encoded, nil
}

type statusCompressionBuffer struct {
	bytes.Buffer
	ctx context.Context
}

func (b *statusCompressionBuffer) Write(data []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if len(data) > MaxCompressedBytes-b.Len() {
		return 0, ErrInspectionBudget
	}
	return b.Buffer.Write(data)
}
