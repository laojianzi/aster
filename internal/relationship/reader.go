package relationship

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/laojianzi/aster/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type scan struct {
	r      *Reader
	ctx    context.Context
	out    Snapshot
	seen   map[string]bool
	warned map[string]bool
}

func (r *Reader) Read(parent context.Context, target resource.Identity) (Snapshot, error) {
	if err := target.Validate(); err != nil {
		return Snapshot{}, err
	}
	if r.client == nil || target.UID == "" || target.Namespace == "*" {
		return Snapshot{}, errors.New("relationships require an explicit identity with UID")
	}
	ctx, cancel := context.WithTimeout(parent, r.limits.Timeout)
	defer cancel()
	s := scan{r: r, ctx: ctx, out: Snapshot{Target: target, ObservedAt: time.Now().UTC()}, seen: map[string]bool{}, warned: map[string]bool{}}
	s.out.Requests++
	root, err := r.client.GetObject(ctx, target.GVR, target.Namespace, target.Name)
	if err != nil {
		return s.out, err
	}
	if root == nil || root.GetUID() != target.UID || root.GetNamespace() != target.Namespace || root.GetName() != target.Name {
		return s.out, ErrReplaced
	}
	rootType, ok := r.types[root.GroupVersionKind()]
	if !ok {
		for _, declared := range r.types {
			if declared.GVR == target.GVR {
				return s.out, errors.New("resource kind does not match the discovered resource API")
			}
		}
		rootType = Type{target.GVR, root.GetKind(), target.Namespace != ""}
	}
	if root.GetAPIVersion() != target.GVR.GroupVersion().String() || rootType.GVR != target.GVR {
		return s.out, errors.New("resource kind does not match the relationship target")
	}
	if rootType.Namespaced && target.Namespace == "" {
		return s.out, errors.New("namespaced relationships require a namespace")
	}
	s.out.SourceVersion = root.GetResourceVersion()
	s.owners(root)
	if root.GetNamespace() != "" {
		s.dependents(root, rootType)
		if rootType.GVR == core("services") {
			s.service(root)
		}
		if rootType.GVR == core("pods") {
			s.podReferences(root)
		}
	}
	if err := ctx.Err(); err != nil {
		s.warn("Request deadline or cancellation: results are incomplete.")
	}
	sort.Slice(s.out.Links, func(i, j int) bool {
		a, b := s.out.Links[i], s.out.Links[j]
		if a.Relation != b.Relation {
			return a.Relation < b.Relation
		}
		if a.Type.Kind != b.Type.Kind {
			return a.Type.Kind < b.Type.Kind
		}
		return a.Target.Key() < b.Target.Key()
	})
	return s.out, ctx.Err()
}
func core(name string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Version: "v1", Resource: name}
}
func (s *scan) warn(text string) {
	s.out.Incomplete = true
	if !s.warned[text] {
		s.warned[text] = true
		s.out.Warnings = append(s.out.Warnings, text)
	}
}
func (s *scan) request() bool {
	if s.ctx.Err() != nil {
		return false
	}
	if s.out.Requests >= s.r.limits.Requests {
		s.warn("Request budget reached; more relationships may exist.")
		return false
	}
	s.out.Requests++
	return true
}
func (s *scan) add(link Link) {
	key := link.Relation + "|" + link.Target.Key()
	if s.seen[key] {
		return
	}
	if len(s.out.Links) >= s.r.limits.Links {
		s.warn("Relationship display limit reached; narrow the resource scope.")
		return
	}
	s.seen[key] = true
	s.out.Links = append(s.out.Links, link)
}
func errorState(err error) State {
	switch {
	case apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err):
		return Forbidden
	case apierrors.IsNotFound(err):
		return Missing
	default:
		return Unavailable
	}
}
func (s *scan) owners(root *unstructured.Unstructured) {
	owners := root.GetOwnerReferences()
	if len(owners) > 32 {
		s.warn("Owner reference limit reached.")
	}
	for _, owner := range owners[:min(len(owners), 32)] {
		gv, err := schema.ParseGroupVersion(owner.APIVersion)
		typ, ok := s.r.types[gv.WithKind(owner.Kind)]
		relation := "Owned by"
		if owner.Controller != nil && *owner.Controller {
			relation = "Controlled by"
		}
		link := Link{Type: typ, Relation: relation, Target: resource.Identity{SessionID: s.out.Target.SessionID, Name: owner.Name, UID: owner.UID}, State: Unsupported}
		if err != nil || !ok || owner.UID == "" || owner.Name == "" {
			link.Type.Kind = owner.Kind
			link.Note = "Owner kind/version or identity is not resolvable; no API plural is guessed."
			s.add(link)
			s.warn("Some owner references could not be resolved.")
			continue
		}
		link.Target.GVR = typ.GVR
		if typ.Namespaced {
			link.Target.Namespace = root.GetNamespace()
			if link.Target.Namespace == "" {
				link.Note = "A cluster-scoped object cannot have a namespaced owner."
				s.add(link)
				s.warn("Invalid owner scope.")
				continue
			}
		}
		if typ.GVR == core("secrets") || typ.GVR == core("configmaps") {
			link.State = Referenced
			link.Note = "Declared owner identity only; credential/configuration contents are not fetched."
			s.add(link)
			continue
		}
		if !s.request() {
			return
		}
		obj, err := s.r.client.GetObject(s.ctx, typ.GVR, link.Target.Namespace, owner.Name)
		switch {
		case err != nil:
			link.State = errorState(err)
			link.Note = "Owner lookup did not succeed; no access is inferred."
			s.warn("An owner is missing, denied or unavailable.")
		case obj == nil || obj.GetUID() != owner.UID || obj.GetNamespace() != link.Target.Namespace || obj.GetName() != owner.Name || obj.GroupVersionKind() != typ.GVR.GroupVersion().WithKind(typ.Kind):
			link.State = Replaced
			link.Note = "The returned object does not match the recorded owner UID."
			s.warn("An owner reference is stale.")
		default:
			link.State = Verified
			link.Note = "UID-verified owner reference; navigation rechecks identity."
		}
		s.add(link)
	}
}
func (s *scan) dependents(root *unstructured.Unstructured, typ Type) {
	var child schema.GroupVersionKind
	switch typ.GVR {
	case schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}:
		child = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "ReplicaSet"}
	case schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}:
		child = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	case schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, core("replicationcontrollers"):
		child = schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
	default:
		return
	}
	ct := s.r.types[child]
	// Labels are not an ownership index. Scan a bounded namespace collection and
	// verify the complete owner identity instead of guessing from label matches.
	s.collection(ct, root.GetNamespace(), "", func(obj *unstructured.Unstructured) {
		for _, ref := range obj.GetOwnerReferences() {
			if ref.UID == root.GetUID() && ref.Name == root.GetName() && ref.Kind == root.GetKind() && ref.APIVersion == root.GetAPIVersion() {
				s.add(s.objectLink(ct, obj, "Dependent", "Owner UID verified; this is a direct, not transitive, relationship."))
				return
			}
		}
	})
}
func (s *scan) objectLink(typ Type, obj *unstructured.Unstructured, relation, note string) Link {
	return Link{Type: typ, Relation: relation, Target: resource.Identity{SessionID: s.out.Target.SessionID, GVR: typ.GVR, Namespace: obj.GetNamespace(), Name: obj.GetName(), UID: obj.GetUID()}, State: Verified, Note: note}
}
func (s *scan) collection(typ Type, ns, selector string, visit func(*unstructured.Unstructured)) {
	if ns == "" {
		s.warn("Cluster-wide dependent scans are not performed.")
		return
	}
	continuation, version := "", ""
	tokens := map[string]bool{}
	for {
		budget := s.r.limits.Objects - s.out.Scanned
		if budget <= 0 {
			s.warn("Object scan limit reached; results are partial.")
			return
		}
		if !s.request() {
			return
		}
		pageSize := min(s.r.limits.PageSize, budget)
		page, err := s.r.client.ListPage(s.ctx, typ.GVR, ns, metav1.ListOptions{Limit: int64(pageSize), Continue: continuation, LabelSelector: selector})
		if err != nil {
			s.warn(fmt.Sprintf("%s lookup: %s. Results are partial.", typ.Kind, errorState(err)))
			return
		}
		if page == nil {
			s.warn("Invalid empty page from the API server.")
			return
		}
		if version != "" && page.GetResourceVersion() != version {
			s.warn("Collection changed between pages; refresh relationships.")
			return
		}
		version = page.GetResourceVersion()
		if len(page.Items) > pageSize {
			s.warn("API response exceeded the requested page size; results were capped.")
		}
		for i := 0; i < min(len(page.Items), pageSize); i++ {
			s.out.Scanned++
			item := &page.Items[i]
			if item.GetNamespace() != ns || item.GetUID() == "" || item.GetName() == "" || item.GroupVersionKind() != typ.GVR.GroupVersion().WithKind(typ.Kind) {
				s.warn("An invalid or out-of-scope object was ignored.")
				continue
			}
			visit(item)
		}
		continuation = page.GetContinue()
		if continuation == "" {
			return
		}
		if tokens[continuation] {
			s.warn("Repeated continuation token; collection scan stopped.")
			return
		}
		tokens[continuation] = true
	}
}
func (s *scan) service(root *unstructured.Unstructured) {
	typ := s.r.types[schema.GroupVersionKind{Group: "discovery.k8s.io", Version: "v1", Kind: "EndpointSlice"}]
	s.collection(typ, root.GetNamespace(), labels.SelectorFromSet(labels.Set{"kubernetes.io/service-name": root.GetName()}).String(), func(obj *unstructured.Unstructured) {
		if obj.GetLabels()["kubernetes.io/service-name"] != root.GetName() {
			s.warn("EndpointSlice with a different Service label was ignored.")
			return
		}
		s.add(s.objectLink(typ, obj, "EndpointSlice", "Associated by service-name label; inspect endpoint conditions for actual availability."))
	})
	selector, found, err := unstructured.NestedStringMap(root.Object, "spec", "selector")
	if err != nil {
		s.warn("Invalid Service selector; no Pod query was issued.")
		return
	}
	if !found || len(selector) == 0 {
		return
	} // A selectorless Service must not select every Pod.
	ls, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: selector})
	if err != nil {
		s.warn("Invalid Service selector; no Pod query was issued.")
		return
	}
	typ = s.r.types[schema.GroupVersionKind{Version: "v1", Kind: "Pod"}]
	s.collection(typ, root.GetNamespace(), ls.String(), func(obj *unstructured.Unstructured) {
		if !ls.Matches(labels.Set(obj.GetLabels())) {
			s.warn("Non-matching Pod was ignored.")
			return
		}
		s.add(s.objectLink(typ, obj, "Selector match", "Label membership only: not ownership, readiness, or proof that traffic reaches this Pod."))
	})
}
