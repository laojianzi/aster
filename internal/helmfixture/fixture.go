// Package helmfixture provides synthetic storage fixtures for tests only. They
// exercise Helm's documented wire shape, not an actual Helm install or release.
package helmfixture

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func Payload(namespace, release, status string, revision int) []byte {
	b, err := json.Marshal(map[string]any{
		"name": release, "namespace": namespace, "version": revision, "info": map[string]any{"status": status, "notes": "PRIVATE_NOTES"},
		"chart":    map[string]any{"metadata": map[string]any{"name": "example-chart", "version": "1.2.3"}, "values": map[string]any{"secret": "PRIVATE_DEFAULT"}, "templates": []any{map[string]any{"name": "PRIVATE_TEMPLATE", "data": "UFJJVkFURQ=="}}},
		"manifest": "apiVersion: v1\nkind: Secret\nstringData:\n  token: PRIVATE_MANIFEST\n",
		"hooks":    []any{map[string]any{"name": "PRIVATE_HOOK", "manifest": "PRIVATE_HOOK_CONTENT"}},
		"config":   map[string]any{"password": "PRIVATE_VALUE", "nested": map[string]any{"token": "PRIVATE_NESTED"}},
	})
	if err != nil {
		panic(err)
	}
	return b
}
func Stored(payload []byte) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if _, err := gz.Write(payload); err != nil {
		panic(err)
	}
	if err := gz.Close(); err != nil {
		panic(err)
	}
	return []byte(base64.StdEncoding.EncodeToString(b.Bytes()))
}
func Secret(namespace, release, status string, revision int) *corev1.Secret {
	v := strconv.Itoa(revision)
	return &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1." + release + ".v" + v, Namespace: namespace, UID: types.UID("uid" + v), ResourceVersion: "7", Labels: map[string]string{"owner": "helm", "name": release, "status": status, "version": v}},
		Type:       "helm.sh/release.v1", Data: map[string][]byte{"release": Stored(Payload(namespace, release, status, revision))},
	}
}
