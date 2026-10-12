package delivery_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/laojianzi/aster/internal/delivery"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/operation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var configMaps = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
var deployments = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

func kinds() []kube.ResourceKind {
	return []kube.ResourceKind{{GVR: configMaps, Kind: "ConfigMap", Namespaced: true}, {GVR: deployments, Kind: "Deployment", Namespaced: true}}
}

func object(name string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]interface{}{"name": name, "namespace": "team"},
		"data":     map[string]interface{}{"value": "original-🦉-é"},
	}
}

func encode(t *testing.T, obj interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parse(t *testing.T, source []byte) *delivery.Set {
	t.Helper()
	s, err := delivery.Parse(context.Background(), source, "session-a", "team", kinds())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestManifestDeliveryParseResolvesOrderedImmutableTargets(t *testing.T) {
	first := object("settings")
	delete(first["metadata"].(map[string]interface{}), "namespace")
	second := object("app")
	second["apiVersion"], second["kind"] = "apps/v1", "Deployment"
	delete(second, "data")
	second["spec"] = map[string]interface{}{"replicas": int64(1)}
	text := append([]byte("# rendered source\n--- # first\n# Source: chart/templates/config.yaml\n"), encode(t, first)...)
	text = append(text, []byte("\n---\n# empty section\n---\n")...)
	text = append(text, encode(t, second)...)
	text = append(text, []byte("\n---\n# trailing empty document\n")...)
	original := bytes.Clone(text)
	discovery := kinds()
	s, err := delivery.Parse(context.Background(), text, "session-a", "team", discovery)
	if err != nil || s.Len() != 2 || s.Namespace() != "team" || s.SessionID() != "session-a" || len(s.ID()) != 64 {
		t.Fatalf("ordered import: set=%v err=%v", s, err)
	}
	if !bytes.Equal(text, original) {
		t.Fatal("import changed source bytes")
	}
	items := s.Items()
	if items[0].Name != "settings" || items[1].Name != "app" || items[0].Namespace != "team" || items[1].Kind.GVR != deployments {
		t.Fatalf("wrong inventory: %+v", items)
	}
	id := s.ID()
	items[0].Name, items[0].Kind.GVR.Resource = "changed", "secrets"
	for i := range text {
		text[i] = 'x'
	}
	discovery[0].GVR.Resource = "secrets"
	if s.Items()[0].Name != "settings" || s.Items()[0].Kind.GVR != configMaps || s.ID() != id {
		t.Fatal("caller mutation changed imported targets or identity")
	}
	f := newClient()
	plan, err := delivery.Prepare(context.Background(), operation.NewService(f, "session-a"), s, 0, false)
	if err != nil || plan.Target().Namespace != "team" || plan.Target().Name != "settings" || plan.Target().GVR != configMaps {
		t.Fatalf("immutable payload or injected namespace lost: %v", err)
	}
	var sent unstructured.Unstructured
	if err := sent.UnmarshalJSON(f.calls[0].body); err != nil || sent.GetNamespace() != "team" || sent.Object["data"].(map[string]interface{})["value"] != "original-🦉-é" {
		t.Fatal("caller mutation changed prepared payload")
	}
	if again := parse(t, original); again.ID() != id {
		t.Fatal("identical import identity is nondeterministic")
	}
	for _, tc := range []struct {
		name, session, namespace string
		data                     []byte
		kinds                    []kube.ResourceKind
	}{
		{"session", "session-b", "team", original, kinds()},
		{"source", "session-a", "team", append(bytes.Clone(original), '#'), kinds()},
		{"routing", "session-a", "team", original, []kube.ResourceKind{{GVR: configMaps.GroupVersion().WithResource("alternateconfigmaps"), Kind: "ConfigMap", Namespaced: true}, kinds()[1]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other, err := delivery.Parse(context.Background(), tc.data, tc.session, tc.namespace, tc.kinds)
			if err != nil || other.ID() == id {
				t.Fatalf("identity failed to bind %s: %v", tc.name, err)
			}
		})
	}
	withoutNS := encode(t, first)
	a, err := delivery.Parse(context.Background(), withoutNS, "session-a", "team", kinds())
	if err != nil {
		t.Fatal(err)
	}
	b, err := delivery.Parse(context.Background(), withoutNS, "session-a", "other", kinds())
	if err != nil || a.ID() == b.ID() || b.Items()[0].Namespace != "other" {
		t.Fatal("resolved namespace is not part of immutable identity")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				copy := s.Items()
				copy[0].Name = "private-copy"
				if s.ID() != id || s.Items()[0].Name != "settings" {
					t.Error("concurrent descriptor reads changed immutable input")
				}
			}
		}()
	}
	wg.Wait()
	for _, source := range []string{
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: block\ndata:\n  text: |\n    ---\n    # literal text\n",
		"apiVersion: v1\r\nkind: ConfigMap\r\nmetadata:\r\n  name: crlf\r\n",
	} {
		if got := parse(t, []byte(source)); got.Len() != 1 {
			t.Fatal("literal or CRLF input was split")
		}
	}
	var empty *delivery.Set
	if empty.Len() != 0 || empty.ID() != "" || empty.SessionID() != "" || empty.Namespace() != "" || empty.Items() != nil {
		t.Fatal("nil set accessors are not empty")
	}
}

func TestManifestDeliveryParseRejectsAmbiguousOrUnsafeInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]interface{})
	}{
		{"status", func(o map[string]interface{}) { o["status"] = nil }},
		{"uid", func(o map[string]interface{}) { o["metadata"].(map[string]interface{})["uid"] = "server-owned" }},
		{"version", func(o map[string]interface{}) { o["metadata"].(map[string]interface{})["resourceVersion"] = "10" }},
		{"managed-fields", func(o map[string]interface{}) {
			o["metadata"].(map[string]interface{})["managedFields"] = []interface{}{}
		}},
		{"owners", func(o map[string]interface{}) {
			o["metadata"].(map[string]interface{})["ownerReferences"] = []interface{}{}
		}},
		{"finalizers", func(o map[string]interface{}) { o["metadata"].(map[string]interface{})["finalizers"] = []interface{}{} }},
		{"generate-name", func(o map[string]interface{}) { o["metadata"].(map[string]interface{})["generateName"] = "new-" }},
		{"foreign-namespace", func(o map[string]interface{}) { o["metadata"].(map[string]interface{})["namespace"] = "other" }},
		{"empty-namespace", func(o map[string]interface{}) { o["metadata"].(map[string]interface{})["namespace"] = "" }},
		{"null-namespace", func(o map[string]interface{}) { o["metadata"].(map[string]interface{})["namespace"] = nil }},
		{"numeric-name", func(o map[string]interface{}) { o["metadata"].(map[string]interface{})["name"] = int64(12) }},
		{"path-name", func(o map[string]interface{}) { o["metadata"].(map[string]interface{})["name"] = "../other" }},
		{"label-type", func(o map[string]interface{}) {
			o["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{"app": true}
		}},
		{"annotation-type", func(o map[string]interface{}) {
			o["metadata"].(map[string]interface{})["annotations"] = []interface{}{}
		}},
		{"secret", func(o map[string]interface{}) { o["kind"] = "Secret" }},
		{"list", func(o map[string]interface{}) { o["kind"] = "List"; o["items"] = []interface{}{} }},
		{"unknown", func(o map[string]interface{}) { o["kind"] = "UnknownKind" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := object("demo")
			tc.change(o)
			if s, err := delivery.Parse(context.Background(), encode(t, o), "session-a", "team", kinds()); s != nil || err == nil {
				t.Fatal("unsafe import was accepted")
			}
		})
	}
	for _, annotation := range []string{
		"helm.sh/hook", "helm.sh/hook-weight", "helm.sh/hook-delete-policy", "helm.sh/resource-policy",
		"meta.helm.sh/release-name", "meta.helm.sh/release-namespace",
		"argocd.argoproj.io/hook", "argocd.argoproj.io/sync-wave", "argocd.argoproj.io/sync-options", "argocd.argoproj.io/tracking-id",
		"kustomize.toolkit.fluxcd.io/ssa", "kustomize.toolkit.fluxcd.io/prune", "helm.toolkit.fluxcd.io/driftDetection",
		"reconcile.fluxcd.io/requestedAt", "fluxcd.io/automated",
		"kubectl.kubernetes.io/last-applied-configuration",
	} {
		t.Run(annotation, func(t *testing.T) {
			o := object("demo")
			o["metadata"].(map[string]interface{})["annotations"] = map[string]interface{}{annotation: "private-value"}
			if s, err := delivery.Parse(context.Background(), encode(t, o), "session-a", "team", kinds()); s != nil || err == nil {
				t.Fatal("lifecycle annotation was accepted")
			}
		})
	}
	ordinary := object("ordinary")
	ordinary["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{"helm.sh/chart": "demo-1.0", "app.kubernetes.io/managed-by": "Helm"}
	ordinary["metadata"].(map[string]interface{})["annotations"] = map[string]interface{}{"example.test/note": "literal helm.sh/hook: text is data"}
	parse(t, encode(t, ordinary))
	for _, tc := range []struct {
		name string
		list []kube.ResourceKind
	}{
		{"none", nil},
		{"duplicate-discovery", append(kinds(), kinds()[0])},
		{"ambiguous-route", append(kinds(), kube.ResourceKind{GVR: configMaps.GroupVersion().WithResource("otherconfigs"), Kind: "ConfigMap", Namespaced: true})},
		{"cluster-scope", []kube.ResourceKind{{GVR: configMaps, Kind: "ConfigMap", Namespaced: false}}},
		{"secret-route", []kube.ResourceKind{{GVR: configMaps.GroupVersion().WithResource("secrets"), Kind: "ConfigMap", Namespaced: true}}},
		{"subresource-route", []kube.ResourceKind{{GVR: configMaps.GroupVersion().WithResource("configmaps/status"), Kind: "ConfigMap", Namespaced: true}}},
		{"empty-resource", []kube.ResourceKind{{GVR: configMaps.GroupVersion().WithResource(""), Kind: "ConfigMap", Namespaced: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if s, err := delivery.Parse(context.Background(), encode(t, object("demo")), "session-a", "team", tc.list); s != nil || err == nil {
				t.Fatal("ambiguous or unsafe discovery was accepted")
			}
		})
	}
	first, second := object("same"), object("same")
	first["apiVersion"], first["kind"] = "apps/v1", "Deployment"
	second["apiVersion"], second["kind"] = "apps/v1beta1", "Deployment"
	input := append(append(encode(t, first), []byte("\n---\n")...), encode(t, second)...)
	versions := append(kinds(), kube.ResourceKind{GVR: schema.GroupVersionResource{Group: "apps", Version: "v1beta1", Resource: "deployments"}, Kind: "Deployment", Namespaced: true})
	if s, err := delivery.Parse(context.Background(), input, "session-a", "team", versions); s != nil || err == nil || !strings.Contains(err.Error(), "document 2") {
		t.Fatal("cross-version duplicate escaped resource-identity check")
	}
	input = append(append(encode(t, object("same")), []byte("\n---\n")...), encode(t, object("same"))...)
	if s, err := delivery.Parse(context.Background(), input, "session-a", "team", kinds()); s != nil || err == nil {
		t.Fatal("duplicate document escaped")
	}
}

type cancelDuringInspection struct {
	context.Context
	cancel context.CancelFunc
	checks atomic.Int32
}

func (c *cancelDuringInspection) Err() error {
	if c.checks.Add(1) > 100 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestManifestDeliveryParseBudgetsCancellationAndPrivacy(t *testing.T) {
	const private = "do-not-return-this-value"
	base := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: bounded\n"
	bad := []string{
		"apiVersion: v1\nkind: ConfigMap\nmetadata: {name: " + private + ", name: duplicate}\n",
		base + "data: [" + private + "\n",
		base + "data: {value: &anchor " + private + ", alias: *anchor}\n",
		base + "data: {base: &anchor {value: " + private + "}, merged: {<<: *anchor}}\n",
		base + "data: {true: " + private + "}\n",
		base + "spec: " + strings.Repeat("[", 70) + "0" + strings.Repeat("]", 70) + "\n",
		base + "spec: [" + strings.Repeat("0,", manifest.MaxSourceNodes) + "0]\n",
		"--- " + private + "\n" + base,
		"\ufeff" + base,
		strings.ReplaceAll(base, "\n", "\r"),
		base + "# unsupported\u2028" + private,
		"[" + private + "]",
		"null",
		"# comment only\n---\n# another comment",
		string([]byte{0xff, 0xfe}),
	}
	for i, input := range bad {
		t.Run(fmt.Sprintf("unsafe-form-%d", i), func(t *testing.T) {
			s, err := delivery.Parse(context.Background(), []byte(input), "session-a", "team", kinds())
			if s != nil || err == nil || strings.Contains(err.Error(), private) || len(err.Error()) > 200 {
				t.Fatalf("unsafe parse leaked or succeeded: %v", err)
			}
		})
	}
	var documents [][]byte
	for i := range delivery.MaxObjects {
		documents = append(documents, encode(t, object(fmt.Sprintf("item-%d", i))))
	}
	if got := parse(t, bytes.Join(documents, []byte("\n---\n"))); got.Len() != delivery.MaxObjects {
		t.Fatal("32-object boundary was truncated")
	}
	documents = append(documents, encode(t, object("item-33")))
	if s, err := delivery.Parse(context.Background(), bytes.Join(documents, []byte("\n---\n")), "session-a", "team", kinds()); s != nil || err == nil {
		t.Fatal("33 objects were accepted or truncated")
	}
	// Fill eight independently small JSON objects with insignificant spaces so
	// the total source boundary is exercised without normalized-object overflow.
	var exact bytes.Buffer
	remaining := delivery.MaxBytes - 7*len("\n---\n")
	for i := range 8 {
		n := remaining / (8 - i)
		body := encode(t, object(fmt.Sprintf("size-%d", i)))
		exact.Write(body)
		exact.WriteString(strings.Repeat(" ", n-len(body)))
		remaining -= n
		if i != 7 {
			exact.WriteString("\n---\n")
		}
	}
	if exact.Len() != delivery.MaxBytes || parse(t, exact.Bytes()).Len() != 8 {
		t.Fatal("exact total byte budget did not retain all objects")
	}
	if s, err := delivery.Parse(context.Background(), append(bytes.Clone(exact.Bytes()), ' '), "session-a", "team", kinds()); s != nil || err == nil {
		t.Fatal("total byte overflow accepted")
	}
	var lineParts [][]byte
	for i := range delivery.MaxObjects {
		n := 4999
		if i == delivery.MaxObjects-1 {
			n++
		}
		header := fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: lines-%d\n", i)
		part := []byte(header + strings.Repeat("#\n", n-strings.Count(header, "\n")))
		lineParts = append(lineParts, part)
	}
	allLines := bytes.Join(lineParts, []byte("---\n"))
	if bytes.Count(allLines, []byte{'\n'}) != delivery.MaxNewlines || parse(t, allLines).Len() != delivery.MaxObjects {
		t.Fatal("exact aggregate line boundary was truncated")
	}
	if s, err := delivery.Parse(context.Background(), append(bytes.Clone(allLines), '\n'), "session-a", "team", kinds()); s != nil || err == nil || !strings.Contains(err.Error(), "total line budget") {
		t.Fatal("aggregate line limit was not enforced")
	}
	lines := base + strings.Repeat("#\n", 5000-strings.Count(base, "\n"))
	parse(t, []byte(lines))
	if s, err := delivery.Parse(context.Background(), []byte(lines+"#\n"), "session-a", "team", kinds()); s != nil || err == nil {
		t.Fatal("per-object line budget overflow accepted")
	}
	if s, err := delivery.Parse(context.Background(), []byte(base+"#"+strings.Repeat("x", manifest.MaxBytes)), "session-a", "team", kinds()); s != nil || err == nil {
		t.Fatal("per-object byte budget overflow accepted")
	}
	large := object("large")
	delete(large["metadata"].(map[string]interface{}), "namespace")
	large["data"] = map[string]interface{}{"padding": ""}
	large["data"].(map[string]interface{})["padding"] = strings.Repeat("x", manifest.MaxBytes-1-len(encode(t, large)))
	if len(encode(t, large)) != manifest.MaxBytes-1 {
		t.Fatal("bad namespace-expansion fixture")
	}
	if s, err := delivery.Parse(context.Background(), encode(t, large), "session-a", strings.Repeat("n", 63), kinds()); s != nil || err == nil || !strings.Contains(err.Error(), "resolved object") {
		t.Fatalf("namespace injection escaped output byte limit: %v", err)
	}
	parse(t, []byte(strings.Repeat("---\n", delivery.MaxSegments-1)+base))
	if s, err := delivery.Parse(context.Background(), []byte(strings.Repeat("---\n", delivery.MaxSegments)+base), "session-a", "team", kinds()); s != nil || err == nil {
		t.Fatal("empty segments bypassed work budget")
	}
	if s, err := delivery.Parse(context.Background(), []byte(base), "session-a", "team", make([]kube.ResourceKind, delivery.MaxKinds+1)); s != nil || err == nil {
		t.Fatal("unbounded discovery input accepted")
	}
	for _, ns := range []string{"", "Other", "../team", strings.Repeat("n", 64)} {
		if s, err := delivery.Parse(context.Background(), []byte(base), "session-a", ns, kinds()); s != nil || err == nil {
			t.Fatal("invalid explicit namespace accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s, err := delivery.Parse(ctx, []byte(base), "session-a", "team", kinds()); s != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled import continued")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	during := &cancelDuringInspection{Context: ctx, cancel: cancel}
	if s, err := delivery.Parse(during, exact.Bytes(), "session-a", "team", kinds()); s != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("import ignored cancellation while inspecting input")
	}
}

type request struct {
	verb     string
	gvr      schema.GroupVersionResource
	ns, name string
	body     []byte
	create   metav1.CreateOptions
	patch    metav1.PatchOptions
}

type client struct {
	objects        map[string]*unstructured.Unstructured
	gets           int
	calls          []request
	get            func(int, schema.GroupVersionResource, string, string) (*unstructured.Unstructured, error)
	createErr      error
	patchErr       error
	writeErr       error
	cancelOnDryRun context.CancelFunc
	createResponse func(*unstructured.Unstructured) *unstructured.Unstructured
}

func newClient() *client { return &client{objects: map[string]*unstructured.Unstructured{}} }
func key(gvr schema.GroupVersionResource, ns, name string) string {
	return gvr.String() + "|" + ns + "|" + name
}
func (f *client) GetObject(_ context.Context, gvr schema.GroupVersionResource, ns, name string) (*unstructured.Unstructured, error) {
	f.gets++
	if f.get != nil {
		return f.get(f.gets, gvr, ns, name)
	}
	if o := f.objects[key(gvr, ns, name)]; o != nil {
		return o, nil // Borrowed deliberately: Lookup is responsible for the copy.
	}
	return nil, apierrors.NewNotFound(gvr.GroupResource(), name)
}
func (f *client) CreateObject(_ context.Context, gvr schema.GroupVersionResource, ns string, obj *unstructured.Unstructured, opts metav1.CreateOptions) (*unstructured.Unstructured, error) {
	body, _ := obj.MarshalJSON()
	f.calls = append(f.calls, request{verb: "POST", gvr: gvr, ns: ns, name: obj.GetName(), body: bytes.Clone(body), create: *opts.DeepCopy()})
	if f.createErr != nil {
		return nil, f.createErr
	}
	out := obj.DeepCopy()
	out.SetUID(types.UID("new-" + obj.GetName()))
	out.SetResourceVersion("1")
	if len(opts.DryRun) != 0 {
		if f.cancelOnDryRun != nil {
			f.cancelOnDryRun()
		}
		if f.createResponse != nil {
			out = f.createResponse(out)
		}
		return out, nil
	}
	f.objects[key(gvr, ns, obj.GetName())] = out
	return out.DeepCopy(), f.writeErr
}
func (f *client) PatchObject(_ context.Context, gvr schema.GroupVersionResource, ns, name string, typ types.PatchType, body []byte, opts metav1.PatchOptions) (*unstructured.Unstructured, error) {
	if typ != types.ApplyPatchType {
		return nil, errors.New("unexpected non-apply patch")
	}
	f.calls = append(f.calls, request{verb: "PATCH", gvr: gvr, ns: ns, name: name, body: bytes.Clone(body), patch: *opts.DeepCopy()})
	if f.patchErr != nil {
		return nil, f.patchErr
	}
	out := &unstructured.Unstructured{}
	if err := out.UnmarshalJSON(body); err != nil {
		return nil, err
	}
	if len(opts.DryRun) != 0 {
		if f.cancelOnDryRun != nil {
			f.cancelOnDryRun()
		}
		return out, nil
	}
	out.SetResourceVersion("next")
	f.objects[key(gvr, ns, name)] = out
	return out.DeepCopy(), f.writeErr
}
func (f *client) DeleteObject(context.Context, schema.GroupVersionResource, string, string, metav1.DeleteOptions) error {
	return errors.New("delivery must not delete")
}
func (f *client) add(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	o := &unstructured.Unstructured{}
	if err := o.UnmarshalJSON(encode(t, object(name))); err != nil {
		t.Fatal(err)
	}
	o.SetUID("original-uid")
	o.SetResourceVersion("10")
	f.objects[key(configMaps, "team", name)] = o
	return o
}

func TestManifestDeliveryPrepareUsesSameServiceCreateAndApply(t *testing.T) {
	s := parse(t, encode(t, object("demo")))
	f := newClient()
	ops := operation.NewService(f, "session-a")
	p, err := delivery.Prepare(context.Background(), ops, s, 0, false)
	if err != nil || p.Kind() != "create" || p.Target().UID != "" || f.gets != 1 || len(f.calls) != 1 || len(f.objects) != 0 {
		t.Fatalf("create preparation: %v", err)
	}
	if call := f.calls[0]; call.verb != "POST" || call.create.FieldManager != "aster-desktop" || call.create.FieldValidation != "Strict" || len(call.create.DryRun) != 1 || call.create.DryRun[0] != metav1.DryRunAll {
		t.Fatalf("unsafe create preview options: %+v", call)
	}
	_, preview := p.Preview()
	preview[0] = 'X'
	r, err := ops.Execute(context.Background(), p)
	if err != nil || r.State != "Succeeded" || r.Object.GetUID() != "new-demo" || len(f.calls) != 2 || !bytes.Equal(f.calls[0].body, f.calls[1].body) || len(f.calls[1].create.DryRun) != 0 {
		t.Fatalf("explicit create lost reviewed payload: result=%+v err=%v", r, err)
	}
	f = newClient()
	f.add(t, "demo")
	ops = operation.NewService(f, "session-a")
	if p, err = delivery.Prepare(context.Background(), ops, s, 0, false); p != nil || !errors.Is(err, delivery.ErrAcknowledgementRequired) || f.gets != 1 || len(f.calls) != 0 {
		t.Fatal("existing-object apply bypassed omission acknowledgement")
	}
	p, err = delivery.Prepare(context.Background(), ops, s, 0, true)
	if err != nil || p.Kind() != "apply" || p.Target().UID != "original-uid" || f.gets != 3 || len(f.calls) != 1 {
		t.Fatalf("existing-object preview: %v", err)
	}
	var desired unstructured.Unstructured
	if err := desired.UnmarshalJSON(f.calls[0].body); err != nil || desired.GetUID() != "original-uid" || desired.GetResourceVersion() != "10" {
		t.Fatal("reviewed identity/version were not pinned")
	}
	r, err = ops.Execute(context.Background(), p)
	if err != nil || r.State != "Succeeded" || len(f.calls) != 2 || !bytes.Equal(f.calls[0].body, f.calls[1].body) {
		t.Fatalf("explicit apply changed reviewed bytes: %+v %v", r, err)
	}
	for i, call := range f.calls {
		if call.verb != "PATCH" || call.patch.FieldManager != operation.ApplyFieldManager || call.patch.FieldValidation != "Strict" || call.patch.Force == nil || *call.patch.Force || (i == 0 && len(call.patch.DryRun) != 1) || (i == 1 && len(call.patch.DryRun) != 0) {
			t.Fatalf("unsafe apply options: %+v", call)
		}
	}
}

func TestManifestDeliveryPrepareRejectsReadFailuresAndChangedIdentity(t *testing.T) {
	s := parse(t, encode(t, object("demo")))
	for _, tc := range []struct {
		name string
		edit func(*unstructured.Unstructured) *unstructured.Unstructured
	}{
		{"nil", func(*unstructured.Unstructured) *unstructured.Unstructured { return nil }},
		{"name", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetName("other"); return o }},
		{"namespace", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetNamespace("other"); return o }},
		{"kind", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetKind("Other"); return o }},
		{"version", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetAPIVersion("v2"); return o }},
		{"oversized", func(o *unstructured.Unstructured) *unstructured.Unstructured {
			o.Object["data"] = map[string]interface{}{"oversized": strings.Repeat("private-preview", manifest.MaxBytes)}
			return o
		}},
	} {
		t.Run("nonconforming-create-preview-"+tc.name, func(t *testing.T) {
			f := newClient()
			f.createResponse = tc.edit
			if p, err := delivery.Prepare(context.Background(), operation.NewService(f, "session-a"), s, 0, false); p != nil || err == nil || strings.Contains(err.Error(), "private-preview") || len(f.calls) != 1 || len(f.calls[0].create.DryRun) != 1 || len(f.objects) != 0 {
				t.Fatalf("unreviewable server preview was published: %v", err)
			}
		})
	}
	for _, failure := range []error{
		apierrors.NewForbidden(configMaps.GroupResource(), "demo", errors.New("denied")),
		apierrors.NewUnauthorized("denied"), apierrors.NewTimeoutError("timed out", 1),
		context.DeadlineExceeded, errors.New("NotFound is only text"),
	} {
		f := newClient()
		f.get = func(int, schema.GroupVersionResource, string, string) (*unstructured.Unstructured, error) {
			return nil, failure
		}
		if p, err := delivery.Prepare(context.Background(), operation.NewService(f, "session-a"), s, 0, true); p != nil || err != failure || len(f.calls) != 0 {
			t.Fatalf("read error triggered fallback or lost classification: %v", err)
		}
	}
	for _, tc := range []struct {
		name string
		edit func(*unstructured.Unstructured) *unstructured.Unstructured
	}{
		{"nil", func(*unstructured.Unstructured) *unstructured.Unstructured { return nil }},
		{"name", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetName("other"); return o }},
		{"namespace", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetNamespace("other"); return o }},
		{"kind", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetKind("Other"); return o }},
		{"version", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetAPIVersion("v2"); return o }},
		{"uid", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetUID(""); return o }},
		{"resource-version", func(o *unstructured.Unstructured) *unstructured.Unstructured { o.SetResourceVersion(""); return o }},
		{"terminating", func(o *unstructured.Unstructured) *unstructured.Unstructured {
			now := metav1.Now()
			o.SetDeletionTimestamp(&now)
			return o
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newClient()
			o := tc.edit(f.add(t, "demo"))
			f.get = func(int, schema.GroupVersionResource, string, string) (*unstructured.Unstructured, error) {
				return o, nil
			}
			if p, err := delivery.Prepare(context.Background(), operation.NewService(f, "session-a"), s, 0, true); p != nil || !errors.Is(err, operation.ErrConflict) || len(f.calls) != 0 {
				t.Fatalf("untrusted lookup identity reached a mutation: %v", err)
			}
		})
	}
	t.Run("replacement-between-lookup-and-preview", func(t *testing.T) {
		f := newClient()
		o := f.add(t, "demo")
		f.get = func(n int, _ schema.GroupVersionResource, _, _ string) (*unstructured.Unstructured, error) {
			if n == 2 {
				o.SetUID("replacement")
			}
			return o, nil
		}
		if p, err := delivery.Prepare(context.Background(), operation.NewService(f, "session-a"), s, 0, true); p != nil || !errors.Is(err, operation.ErrConflict) || len(f.calls) != 0 {
			t.Fatalf("replacement got an apply plan: %v", err)
		}
	})
	t.Run("dry-run-conflict-never-falls-back", func(t *testing.T) {
		f := newClient()
		f.add(t, "demo")
		f.patchErr = apierrors.NewConflict(configMaps.GroupResource(), "demo", errors.New("owned by aster-desktop"))
		if p, err := delivery.Prepare(context.Background(), operation.NewService(f, "session-a"), s, 0, true); p != nil || err != f.patchErr || len(f.calls) != 1 || f.calls[0].verb != "PATCH" {
			t.Fatalf("ownership conflict was hidden or retried: %v", err)
		}
		if f.calls[0].patch.Force == nil || *f.calls[0].patch.Force {
			t.Fatal("conflict used forced ownership")
		}
	})
	t.Run("create-race-never-upserts", func(t *testing.T) {
		f := newClient()
		f.createErr = apierrors.NewAlreadyExists(configMaps.GroupResource(), "demo")
		if p, err := delivery.Prepare(context.Background(), operation.NewService(f, "session-a"), s, 0, true); p != nil || err != f.createErr || len(f.calls) != 1 || f.calls[0].verb != "POST" {
			t.Fatalf("create race became an update: %v", err)
		}
	})
	t.Run("cancelled-not-found-cannot-create", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f := newClient()
		f.get = func(int, schema.GroupVersionResource, string, string) (*unstructured.Unstructured, error) {
			cancel()
			return nil, apierrors.NewNotFound(configMaps.GroupResource(), "demo")
		}
		if p, err := delivery.Prepare(ctx, operation.NewService(f, "session-a"), s, 0, false); p != nil || !errors.Is(err, context.Canceled) || len(f.calls) != 0 {
			t.Fatal("cancelled lookup continued to create")
		}
	})
	t.Run("cancelled-dry-run-is-not-published", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f := newClient()
		f.cancelOnDryRun = cancel
		if p, err := delivery.Prepare(ctx, operation.NewService(f, "session-a"), s, 0, false); p != nil || !errors.Is(err, context.Canceled) || len(f.calls) != 1 || len(f.calls[0].create.DryRun) != 1 {
			t.Fatal("cancelled preview was published or executed")
		}
	})
}

func TestManifestDeliveryPreparedOperationsRemainBoundAndSingleUse(t *testing.T) {
	s := parse(t, encode(t, object("demo")))
	f := newClient()
	if p, err := delivery.Prepare(context.Background(), operation.NewService(f, "other-session"), s, 0, false); p != nil || err == nil || f.gets != 0 || len(f.calls) != 0 {
		t.Fatal("set crossed a connection session")
	}
	ops := operation.NewService(f, "session-a")
	for _, i := range []int{-1, 1} {
		if p, err := delivery.Prepare(context.Background(), ops, s, i, true); p != nil || err == nil || f.gets != 0 {
			t.Fatal("invalid index reached client")
		}
	}
	for _, invalid := range []*delivery.Set{nil, {}} {
		if p, err := delivery.Prepare(context.Background(), ops, invalid, 0, true); p != nil || err == nil {
			t.Fatal("invalid set accepted")
		}
	}
	p, err := delivery.Prepare(context.Background(), ops, s, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operation.NewService(f, "session-a").Execute(context.Background(), p); err == nil || len(f.calls) != 1 {
		t.Fatal("plan crossed operation issuer")
	}
	f.writeErr = context.DeadlineExceeded
	r, err := ops.Execute(context.Background(), p)
	if err == nil || r.State != "Unknown" || len(f.calls) != 2 || f.objects[key(configMaps, "team", "demo")] == nil {
		t.Fatalf("lost committed response was not Unknown: %+v %v", r, err)
	}
	if _, err := ops.Execute(context.Background(), p); err == nil || len(f.calls) != 2 {
		t.Fatal("Unknown plan was replayed")
	}
	// Fresh review must re-read the actual object; this now takes the acknowledged
	// existing-object path. Set itself does not promise a mutation queue or retry.
	f.writeErr = nil
	if p, err = delivery.Prepare(context.Background(), ops, s, 0, false); p != nil || !errors.Is(err, delivery.ErrAcknowledgementRequired) {
		t.Fatal("fresh review assumed the pre-Unknown object was absent")
	}
	p, err = delivery.Prepare(context.Background(), ops, s, 0, true)
	if err != nil || p.Kind() != "apply" {
		t.Fatalf("explicit re-review failed: %v", err)
	}
	calls := len(f.calls)
	f.objects[key(configMaps, "team", "demo")].SetResourceVersion("changed-after-review")
	if _, err := ops.Execute(context.Background(), p); !errors.Is(err, operation.ErrConflict) || len(f.calls) != calls {
		t.Fatal("changed resourceVersion reached PATCH")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reads := f.gets
	if p, err := delivery.Prepare(ctx, ops, s, 0, true); p != nil || !errors.Is(err, context.Canceled) || f.gets != reads {
		t.Fatal("cancelled prepare performed a lookup")
	}
}
