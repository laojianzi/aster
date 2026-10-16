package helmfixture

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
)

// WithManifest creates synthetic stored content for preview tests, NOT a real
// Helm release. All other private canaries and the separate hook are retained.
func WithManifest(namespace, release, status string, revision int, manifest string) *corev1.Secret {
	var payload map[string]any
	if err := json.Unmarshal(Payload(namespace, release, status, revision), &payload); err != nil {
		panic(err)
	}
	payload["manifest"] = manifest
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	s := Secret(namespace, release, status, revision)
	s.Data["release"] = Stored(data)
	return s
}

// PreviewManifest retains only a fixed, harmless object identity in the report.
// Its Secret content is never eligible for display, import or mutation.
func PreviewManifest(namespace string) string {
	return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: preview-config\n  namespace: " + namespace + "\n  annotations:\n    helm.sh/resource-policy: keep\ndata:\n  token: PRIVATE_CONFIG\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: preview-secret\nstringData:\n  token: PRIVATE_SECRET\n"
}
