package kube

import (
	"slices"

	"k8s.io/client-go/rest"
)

// copyConnectionConfig snapshots mutable identity and credential configuration.
// client-go's CopyConfig intentionally retains several slice/map/pointer aliases;
// that is not sufficient for an immutable desktop connection boundary.
// Transport callbacks, serializers and the shared rate limiter are trusted code
// and intentionally retained. Files referenced by kubeconfig are not snapshotted.
func copyConnectionConfig(source *rest.Config) *rest.Config {
	seed := *source
	// Copy before rest.CopyConfig, which can assign ExecProvider.Config through
	// the original ExecProvider pointer even while making its "copy".
	if source.ExecProvider != nil {
		seed.ExecProvider = source.ExecProvider.DeepCopy()
	}
	copy := rest.CopyConfig(&seed)
	if source.AuthProvider != nil {
		copy.AuthProvider = source.AuthProvider.DeepCopy()
	}
	if source.GroupVersion != nil {
		gv := *source.GroupVersion
		copy.GroupVersion = &gv
	}
	copy.Impersonate.Groups = slices.Clone(source.Impersonate.Groups)
	if source.Impersonate.Extra != nil {
		copy.Impersonate.Extra = make(map[string][]string, len(source.Impersonate.Extra))
		for key, values := range source.Impersonate.Extra {
			copy.Impersonate.Extra[key] = slices.Clone(values)
		}
	}
	copy.CAData = slices.Clone(source.CAData)
	copy.CertData = slices.Clone(source.CertData)
	copy.KeyData = slices.Clone(source.KeyData)
	copy.NextProtos = slices.Clone(source.NextProtos)
	return copy
}
