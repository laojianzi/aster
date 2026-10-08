package relationship

import (
	"github.com/laojianzi/aster/internal/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Name references are displayed without fetching their targets. In particular,
// opening Related must never read Secret or ConfigMap contents as a side effect.
func (s *scan) podReferences(pod *unstructured.Unstructured) {
	add := func(kind, name string) {
		if name == "" {
			return
		}
		typ := s.r.types[schema.GroupVersionKind{Version: "v1", Kind: kind}]
		ns := pod.GetNamespace()
		if !typ.Namespaced {
			ns = ""
		}
		s.add(Link{Type: typ, Target: resource.Identity{SessionID: s.out.Target.SessionID, GVR: typ.GVR, Namespace: ns, Name: name}, Relation: "References", State: Referenced, Note: "Declared name reference only; the target and any credential data have not been read."})
	}
	name := func(m map[string]interface{}, path ...string) string {
		x, _, _ := unstructured.NestedString(m, path...)
		return x
	}
	if x := name(pod.Object, "spec", "serviceAccountName"); x != "" {
		add("ServiceAccount", x)
	}
	add("Node", name(pod.Object, "spec", "nodeName"))
	volumes, _, _ := unstructured.NestedSlice(pod.Object, "spec", "volumes")
	for _, v := range volumes[:min(len(volumes), 200)] {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		add("ConfigMap", name(m, "configMap", "name"))
		add("Secret", name(m, "secret", "secretName"))
		add("PersistentVolumeClaim", name(m, "persistentVolumeClaim", "claimName"))
		projections, _, _ := unstructured.NestedSlice(m, "projected", "sources")
		for _, p := range projections[:min(len(projections), 200)] {
			if m, ok := p.(map[string]interface{}); ok {
				add("ConfigMap", name(m, "configMap", "name"))
				add("Secret", name(m, "secret", "name"))
			}
		}
		if len(projections) > 200 {
			s.warn("Projected references were capped.")
		}
	}
	if len(volumes) > 200 {
		s.warn("Volume references were capped.")
	}
	for _, field := range []string{"containers", "initContainers", "ephemeralContainers"} {
		containers, _, _ := unstructured.NestedSlice(pod.Object, "spec", field)
		for _, v := range containers[:min(len(containers), 200)] {
			m, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			env, _, _ := unstructured.NestedSlice(m, "env")
			for _, v := range env[:min(len(env), 200)] {
				if m, ok := v.(map[string]interface{}); ok {
					add("ConfigMap", name(m, "valueFrom", "configMapKeyRef", "name"))
					add("Secret", name(m, "valueFrom", "secretKeyRef", "name"))
				}
			}
			from, _, _ := unstructured.NestedSlice(m, "envFrom")
			for _, v := range from[:min(len(from), 200)] {
				if m, ok := v.(map[string]interface{}); ok {
					add("ConfigMap", name(m, "configMapRef", "name"))
					add("Secret", name(m, "secretRef", "name"))
				}
			}
			if len(env) > 200 || len(from) > 200 {
				s.warn("Environment references were capped.")
			}
		}
		if len(containers) > 200 {
			s.warn("Container references were capped.")
		}
	}
	pulls, _, _ := unstructured.NestedSlice(pod.Object, "spec", "imagePullSecrets")
	for _, v := range pulls[:min(len(pulls), 200)] {
		if m, ok := v.(map[string]interface{}); ok {
			add("Secret", name(m, "name"))
		}
	}
	if len(pulls) > 200 {
		s.warn("Image pull references were capped.")
	}
}
