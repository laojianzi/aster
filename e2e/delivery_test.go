//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/delivery"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

// The set is immutable input, not an execution queue. These scenarios explicitly
// review and submit one item at a time; the native tests cover the UI's ordering,
// approval invalidation and stopped-progress state machine.
func TestRealManifestDeliveryReviewedSetSafety(t *testing.T) {
	t.Run("create_update_and_immutable_inventory", testDeliveryApplication)
	t.Run("create_race_requires_fresh_existing_review", testDeliveryCreateRace)
	t.Run("stale_incarnation_and_version", func(t *testing.T) {
		for _, replacement := range []bool{false, true} {
			for _, duringWrite := range []bool{false, true} {
				name := fmt.Sprintf("replacement_%t_after_revalidation_%t", replacement, duringWrite)
				t.Run(name, func(t *testing.T) { testDeliveryStale(t, replacement, duringWrite) })
			}
		}
	})
	t.Run("ownership_conflict_preserves_partial_progress", testDeliveryOwnership)
	t.Run("current_identity_denial_never_becomes_create", testDeliveryRBAC)
	t.Run("committed_response_loss_and_explicit_rereview", testDeliveryResponseLoss)
}

type deliveryObservedClient struct {
	*kube.Backend
	t           *testing.T
	writes      int
	dryRuns     int
	beforeWrite func()
}

func (c *deliveryObservedClient) observe(dryRun []string, validation string) {
	c.t.Helper()
	if validation != "Strict" {
		c.t.Fatal("delivery request lost strict server validation")
	}
	if len(dryRun) != 0 {
		if !reflect.DeepEqual(dryRun, []string{metav1.DryRunAll}) {
			c.t.Fatal("delivery preview did not request server dry-run")
		}
		c.dryRuns++
		return
	}
	c.writes++
	if hook := c.beforeWrite; hook != nil {
		c.beforeWrite = nil
		hook()
	}
}

func (c *deliveryObservedClient) CreateObject(ctx context.Context, gvr schema.GroupVersionResource, ns string, obj *unstructured.Unstructured, opts metav1.CreateOptions) (*unstructured.Unstructured, error) {
	c.observe(opts.DryRun, opts.FieldValidation)
	if opts.FieldManager != "aster-desktop" {
		c.t.Fatal("delivery changed the existing POST field manager")
	}
	return c.Backend.CreateObject(ctx, gvr, ns, obj, opts)
}

func (c *deliveryObservedClient) PatchObject(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, patchType types.PatchType, body []byte, opts metav1.PatchOptions) (*unstructured.Unstructured, error) {
	c.observe(opts.DryRun, opts.FieldValidation)
	if patchType != types.ApplyPatchType || opts.FieldManager != operation.ApplyFieldManager || opts.Force == nil || *opts.Force {
		c.t.Fatal("delivery changed the existing no-force SSA contract")
	}
	return c.Backend.PatchObject(ctx, gvr, ns, name, patchType, body, opts)
}

func deliveryContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func deliveryBackend(t *testing.T, cfg *rest.Config) *deliveryObservedClient {
	t.Helper()
	b, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &deliveryObservedClient{Backend: b, t: t}
}

func importDelivery(t *testing.T, ctx context.Context, b *kube.Backend, source []byte, ns string) *delivery.Set {
	t.Helper()
	// Every import uses actual discovery through this identity's backend.
	kinds, _, err := b.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	set, err := delivery.Parse(ctx, source, "delivery-e2e", ns, kinds)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func deliveryConfig(name, key, value string) string {
	return fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\ndata:\n  %s: %s\n", name, key, value)
}

func readDelivery(t *testing.T, ctx context.Context, b *kube.Backend, item delivery.Item) *unstructured.Unstructured {
	t.Helper()
	obj, err := b.GetObject(ctx, item.Kind.GVR, item.Namespace, item.Name)
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

func absentDelivery(t *testing.T, ctx context.Context, b *kube.Backend, item delivery.Item) {
	t.Helper()
	if _, err := b.GetObject(ctx, item.Kind.GVR, item.Namespace, item.Name); !apierrors.IsNotFound(err) {
		t.Fatalf("unsubmitted object is not absent: %v", err)
	}
}

func submitDelivery(t *testing.T, ctx context.Context, ops *operation.Service, p *operation.Prepared) operation.Result {
	t.Helper()
	result, err := ops.Execute(ctx, p)
	if err != nil || result.State != "Succeeded" || result.Object == nil || result.Object.GetUID() == "" {
		t.Fatalf("reviewed delivery was not accepted: state=%s error=%v", result.State, err)
	}
	return result
}

func testDeliveryApplication(t *testing.T) {
	f := testcluster.NewManifestDelivery(t)
	ctx := deliveryContext(t)
	c := deliveryBackend(t, f.Config)
	ops := operation.NewService(c, "delivery-e2e")
	uids := make(map[string]types.UID)
	for phase, revision := range []string{"", "first-review", "second-review"} {
		source := f.RenderedApplication(revision)
		set := importDelivery(t, ctx, c.Backend, source, f.Namespace)
		if set.Len() != 3 || set.ID() == "" || set.SessionID() != "delivery-e2e" || set.Namespace() != f.Namespace {
			t.Fatal("import did not retain the reviewed set identity and target")
		}
		items := set.Items()
		for i, want := range []string{"ConfigMap", "Service", "Deployment"} {
			if items[i].Kind.Kind != want || !items[i].Kind.Namespaced || items[i].Namespace != f.Namespace {
				t.Fatal("import lost order or explicit namespace resolution")
			}
		}
		// Mutating caller buffers and returned inventory must not alter submitted
		// bytes or destination. No mutable payload is exposed by the Set API.
		source[0] = '!'
		items[0].Name = "must-not-be-created"
		items[0].Kind.GVR.Resource = "secrets"
		items = set.Items()
		for i, item := range items {
			var before *unstructured.Unstructured
			if phase == 0 {
				absentDelivery(t, ctx, c.Backend, item)
			} else {
				before = readDelivery(t, ctx, c.Backend, item)
				if p, err := delivery.Prepare(ctx, ops, set, i, false); p != nil || !errors.Is(err, delivery.ErrAcknowledgementRequired) {
					t.Fatalf("existing object bypassed omission/ownership acknowledgment: %v", err)
				}
			}
			writes, dryRuns := c.writes, c.dryRuns
			p, err := delivery.Prepare(ctx, ops, set, i, phase != 0)
			if err != nil {
				t.Fatal(err)
			}
			wantKind := "create"
			if phase != 0 {
				wantKind = "apply"
			}
			if p.Kind() != wantKind || p.Target().Name != item.Name || p.Target().Namespace != f.Namespace || c.writes != writes || c.dryRuns != dryRuns+1 {
				t.Fatal("per-object preview used a different operation/target or submitted a write")
			}
			if before == nil {
				absentDelivery(t, ctx, c.Backend, item)
			} else {
				after := readDelivery(t, ctx, c.Backend, item)
				if p.Target().UID != before.GetUID() || after.GetUID() != before.GetUID() || !reflect.DeepEqual(after.Object["spec"], before.Object["spec"]) || !reflect.DeepEqual(after.Object["data"], before.Object["data"]) || !reflect.DeepEqual(after.GetAnnotations(), before.GetAnnotations()) {
					t.Fatal("SSA preview changed live desired fields or incarnation")
				}
				// Controllers can update Deployment status/RV independently. The
				// client write counter and desired-field checks cover its dry-run.
				if item.Kind.Kind != "Deployment" && after.GetResourceVersion() != before.GetResourceVersion() {
					t.Fatal("preview persisted a ConfigMap/Service change")
				}
				a, b := p.Preview()
				ownership, err := manifest.OwnershipReview(a, b)
				if err != nil || !strings.Contains(ownership, operation.ApplyFieldManager) {
					t.Fatal("delivery omitted the actual SSA ownership review")
				}
			}
			result := submitDelivery(t, ctx, ops, p)
			if c.writes != writes+1 {
				t.Fatal("one reviewed object submitted more than one write")
			}
			actual := readDelivery(t, ctx, c.Backend, item)
			if actual.GetUID() != result.Object.GetUID() || (phase != 0 && actual.GetUID() != uids[item.Name]) {
				t.Fatal("accepted result did not pin the actual existing incarnation")
			}
			uids[item.Name] = actual.GetUID()
			assertDeliveryRevision(t, actual, revision)
			if phase == 0 {
				for _, later := range items[i+1:] {
					absentDelivery(t, ctx, c.Backend, later)
				}
			}
			if _, err := ops.Execute(ctx, p); err == nil || c.writes != writes+1 {
				t.Fatal("a delivery plan was reusable")
			}
		}
		// Let the real Deployment controller finish the zero-replica generation
		// before the next independently reviewed phase; this is not a write retry.
		if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 20*time.Second, true, func(ctx context.Context) (bool, error) {
			d, err := f.Client.AppsV1().Deployments(f.Namespace).Get(ctx, "delivery-workload", metav1.GetOptions{})
			return err == nil && d.Status.ObservedGeneration >= d.Generation, err
		}); err != nil {
			t.Fatal(err)
		}
	}
	// POST's aster-desktop ownership is deliberately not migrated or forced by
	// this feature. Changing its original field must fail a fresh SSA preview.
	conflicting := deliveryConfig("delivery-config", "message", "must-not-replace-post-owned-value")
	set := importDelivery(t, ctx, c.Backend, []byte(conflicting), f.Namespace)
	before := readDelivery(t, ctx, c.Backend, set.Items()[0])
	writes := c.writes
	if p, err := delivery.Prepare(ctx, ops, set, 0, true); p != nil || !apierrors.IsConflict(err) {
		t.Fatalf("POST-owned field was silently stolen: %v", err)
	}
	after := readDelivery(t, ctx, c.Backend, set.Items()[0])
	if c.writes != writes || before.GetResourceVersion() != after.GetResourceVersion() || !reflect.DeepEqual(before.Object["data"], after.Object["data"]) {
		t.Fatal("ownership conflict persisted a change")
	}
}

func assertDeliveryRevision(t *testing.T, obj *unstructured.Unstructured, revision string) {
	t.Helper()
	switch obj.GetKind() {
	case "ConfigMap":
		message, _, _ := unstructured.NestedString(obj.Object, "data", "message")
		reviewed, _, _ := unstructured.NestedString(obj.Object, "data", "reviewed")
		if message != "created-through-reviewed-delivery" || reviewed != revision {
			t.Fatal("ConfigMap did not retain reviewed values")
		}
	case "Service":
		clusterIP, _, _ := unstructured.NestedString(obj.Object, "spec", "clusterIP")
		if clusterIP == "" || obj.GetAnnotations()["aster.dev/delivery-review"] != revision {
			t.Fatal("Service lost its server allocation or reviewed update")
		}
	case "Deployment":
		replicas, found, err := unstructured.NestedInt64(obj.Object, "spec", "replicas")
		if err != nil || !found || replicas != 0 || obj.GetAnnotations()["aster.dev/delivery-review"] != revision {
			t.Fatal("Deployment did not preserve its reviewed desired state")
		}
	default:
		t.Fatal("unexpected delivered kind")
	}
}

func testDeliveryCreateRace(t *testing.T) {
	f := testcluster.NewManifestDelivery(t)
	ctx := deliveryContext(t)
	c := deliveryBackend(t, f.Config)
	ops := operation.NewService(c, "delivery-e2e")
	source := []byte(deliveryConfig("create-race", "managed", "reviewed") + "---\n" + deliveryConfig("later", "value", "not-submitted"))
	set := importDelivery(t, ctx, c.Backend, source, f.Namespace)
	p, err := delivery.Prepare(ctx, ops, set, 0, false)
	if err != nil || p.Kind() != "create" {
		t.Fatalf("initial create preview: %v", err)
	}
	winner, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "create-race"}, Data: map[string]string{"guard": "concurrent-creator"}}, metav1.CreateOptions{FieldManager: "fixture-owner"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ops.Execute(ctx, p)
	if !apierrors.IsAlreadyExists(err) || result.State != "Conflict" || c.writes != 1 {
		t.Fatalf("create conflict fell back or succeeded: %s %v", result.State, err)
	}
	actual := readDelivery(t, ctx, c.Backend, set.Items()[0])
	if actual.GetUID() != winner.UID || actual.Object["data"].(map[string]interface{})["guard"] != "concurrent-creator" {
		t.Fatal("concurrent creator was overwritten")
	}
	absentDelivery(t, ctx, c.Backend, set.Items()[1])
	if _, err = ops.Execute(ctx, p); err == nil || c.writes != 1 {
		t.Fatal("failed create was replayed")
	}
	// An explicit new import/review now discovers the real existing object.
	fresh := importDelivery(t, ctx, c.Backend, source, f.Namespace)
	if p, err = delivery.Prepare(ctx, ops, fresh, 0, false); p != nil || !errors.Is(err, delivery.ErrAcknowledgementRequired) {
		t.Fatal("fresh existing-resource review bypassed acknowledgment")
	}
	p, err = delivery.Prepare(ctx, ops, fresh, 0, true)
	if err != nil || p.Kind() != "apply" || p.Target().UID != winner.UID {
		t.Fatalf("fresh review did not pin concurrent object: %v", err)
	}
	submitDelivery(t, ctx, ops, p)
	absentDelivery(t, ctx, c.Backend, fresh.Items()[1])
}

func testDeliveryStale(t *testing.T, replacement, duringWrite bool) {
	f := testcluster.NewManifestDelivery(t)
	ctx := deliveryContext(t)
	c := deliveryBackend(t, f.Config)
	ops := operation.NewService(c, "delivery-e2e")
	original, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "stale"}, Data: map[string]string{"guard": "original"}}, metav1.CreateOptions{FieldManager: "fixture-owner"})
	if err != nil {
		t.Fatal(err)
	}
	set := importDelivery(t, ctx, c.Backend, []byte(deliveryConfig("stale", "managed", "must-not-land")+"---\n"+deliveryConfig("later", "value", "not-submitted")), f.Namespace)
	p, err := delivery.Prepare(ctx, ops, set, 0, true)
	if err != nil || p.Target().UID != original.UID {
		t.Fatalf("initial existing review: %v", err)
	}
	var changed *corev1.ConfigMap
	race := func() {
		if replacement {
			if err := f.Client.CoreV1().ConfigMaps(f.Namespace).Delete(ctx, "stale", metav1.DeleteOptions{}); err != nil {
				t.Fatal(err)
			}
			changed, err = f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "stale"}, Data: map[string]string{"guard": "replacement"}}, metav1.CreateOptions{FieldManager: "replacement-owner"})
			if err != nil || changed.UID == original.UID {
				t.Fatalf("replacement fixture: %v", err)
			}
		} else {
			latest, getErr := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, "stale", metav1.GetOptions{})
			if getErr != nil {
				t.Fatal(getErr)
			}
			latest.Data["guard"] = "concurrent-update"
			changed, err = f.Client.CoreV1().ConfigMaps(f.Namespace).Update(ctx, latest, metav1.UpdateOptions{FieldManager: "fixture-owner"})
			if err != nil || changed.ResourceVersion == original.ResourceVersion {
				t.Fatalf("version race fixture: %v", err)
			}
		}
	}
	if duringWrite {
		c.beforeWrite = race
	} else {
		race()
	}
	result, err := ops.Execute(ctx, p)
	if duringWrite {
		if result.State != "Conflict" || c.writes != 1 || (!apierrors.IsConflict(err) && !apierrors.IsInvalid(err)) {
			t.Fatalf("API did not reject the final GET-to-PATCH race: %s %v writes=%d", result.State, err, c.writes)
		}
	} else if result.State != "Rejected" || c.writes != 0 || !errors.Is(err, operation.ErrConflict) {
		t.Fatalf("stale review reached submission: %s %v writes=%d", result.State, err, c.writes)
	}
	actual := readDelivery(t, ctx, c.Backend, set.Items()[0])
	if changed == nil || actual.GetUID() != changed.UID || actual.GetResourceVersion() != changed.ResourceVersion || !reflect.DeepEqual(actual.Object["data"], map[string]interface{}{"guard": changed.Data["guard"]}) {
		t.Fatal("stale plan changed or recreated the concurrent object")
	}
	absentDelivery(t, ctx, c.Backend, set.Items()[1])
	writes := c.writes
	if _, err := ops.Execute(ctx, p); err == nil || c.writes != writes {
		t.Fatal("stale set plan was replayed")
	}
}

func testDeliveryOwnership(t *testing.T) {
	f := testcluster.NewManifestDelivery(t)
	ctx := deliveryContext(t)
	c := deliveryBackend(t, f.Config)
	ops := operation.NewService(c, "delivery-e2e")
	owned, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "owned"}, Data: map[string]string{"foreign": "controller-value"}}, metav1.CreateOptions{FieldManager: "fixture-owner"})
	if err != nil {
		t.Fatal(err)
	}
	source := []byte(deliveryConfig("first", "value", "accepted") + "---\n" + deliveryConfig("owned", "foreign", "must-not-steal") + "---\n" + deliveryConfig("last", "value", "not-submitted"))
	set := importDelivery(t, ctx, c.Backend, source, f.Namespace)
	first, err := delivery.Prepare(ctx, ops, set, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	accepted := submitDelivery(t, ctx, ops, first)
	if p, err := delivery.Prepare(ctx, ops, set, 1, true); p != nil || !apierrors.IsConflict(err) {
		t.Fatalf("foreign ownership was not rejected: %v", err)
	}
	if c.writes != 1 || readDelivery(t, ctx, c.Backend, set.Items()[0]).GetUID() != accepted.Object.GetUID() || readDelivery(t, ctx, c.Backend, set.Items()[1]).GetResourceVersion() != owned.ResourceVersion {
		t.Fatal("failed preview wrote or rolled back accepted progress")
	}
	absentDelivery(t, ctx, c.Backend, set.Items()[2])
	// Reconcile the existing object and import revised intent explicitly. This
	// test does not manufacture automatic rollback, continuation or an atomic set.
	freshSource := []byte(deliveryConfig("first", "value", "accepted") + "---\n" + deliveryConfig("owned", "managed", "reviewed-new-field") + "---\n" + deliveryConfig("last", "value", "not-submitted"))
	fresh := importDelivery(t, ctx, c.Backend, freshSource, f.Namespace)
	if fresh.ID() == set.ID() {
		t.Fatal("changed source retained the old set identity")
	}
	p, err := delivery.Prepare(ctx, ops, fresh, 1, true)
	if err != nil || p.Target().UID != owned.UID {
		t.Fatalf("fresh remaining review: %v", err)
	}
	submitDelivery(t, ctx, ops, p)
	actual := readDelivery(t, ctx, c.Backend, fresh.Items()[1])
	foreign, _, _ := unstructured.NestedString(actual.Object, "data", "foreign")
	managed, _, _ := unstructured.NestedString(actual.Object, "data", "managed")
	if foreign != "controller-value" || managed != "reviewed-new-field" || c.writes != 2 {
		t.Fatal("explicit remaining review stole fields or repeated completed work")
	}
	absentDelivery(t, ctx, c.Backend, fresh.Items()[2])
}

func testDeliveryRBAC(t *testing.T) {
	f := testcluster.NewManifestDelivery(t)
	ctx := deliveryContext(t)
	existing, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "readable-existing"}, Data: map[string]string{"guard": "unchanged"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	user := "aster-delivery-reader-" + f.Namespace
	_, err = f.Client.RbacV1().Roles(f.Namespace).Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "delivery-reader"}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"configmaps"}, ResourceNames: []string{"readable-existing", "readable-missing"}, Verbs: []string{"get"}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Client.RbacV1().RoleBindings(f.Namespace).Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "delivery-reader"}, Subjects: []rbacv1.Subject{{Kind: "User", APIGroup: rbacv1.GroupName, Name: user}}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "delivery-reader"}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := rest.CopyConfig(f.Config)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: []string{"system:authenticated"}}
	c := deliveryBackend(t, cfg)
	if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		_, err := c.GetObject(ctx, schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, f.Namespace, existing.Name)
		return err == nil, nil
	}); err != nil {
		t.Fatal("RBAC read fixture did not become effective", err)
	}
	ops := operation.NewService(c, "delivery-e2e")
	for _, name := range []string{"readable-existing", "readable-missing", "forbidden-lookup"} {
		set := importDelivery(t, ctx, c.Backend, []byte(deliveryConfig(name, "managed", "forbidden")), f.Namespace)
		dryRuns := c.dryRuns
		if p, err := delivery.Prepare(ctx, ops, set, 0, true); p != nil || !apierrors.IsForbidden(err) {
			t.Fatalf("current identity prepared forbidden %s: %v", name, err)
		}
		if name == "forbidden-lookup" && c.dryRuns != dryRuns {
			t.Fatal("a denied GET was treated as NotFound and attempted create")
		}
	}
	if c.writes != 0 || c.dryRuns != 2 {
		t.Fatal("RBAC denial wrote or selected the wrong operation")
	}
	// The same name in a second real namespace is outside this user's Role.
	// A cached namespace-A lookup or route must not authorize namespace B.
	other := testcluster.NewManifestDelivery(t)
	outside, err := other.Client.CoreV1().ConfigMaps(other.Namespace).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: existing.Name}, Data: map[string]string{"guard": "different-namespace"}}, metav1.CreateOptions{FieldManager: "fixture-owner"})
	if err != nil {
		t.Fatal(err)
	}
	outsideSet := importDelivery(t, ctx, c.Backend, []byte(deliveryConfig(existing.Name, "managed", "forbidden")), other.Namespace)
	dryRuns, writes := c.dryRuns, c.writes
	if p, err := delivery.Prepare(ctx, ops, outsideSet, 0, true); p != nil || !apierrors.IsForbidden(err) || c.dryRuns != dryRuns || c.writes != writes {
		t.Fatalf("namespace-A read permission reached namespace B: %v", err)
	}
	stillOutside, err := other.Client.CoreV1().ConfigMaps(other.Namespace).Get(ctx, outside.Name, metav1.GetOptions{})
	if err != nil || stillOutside.UID != outside.UID || stillOutside.ResourceVersion != outside.ResourceVersion || !reflect.DeepEqual(stillOutside.Data, outside.Data) {
		t.Fatalf("denied namespace-B lookup changed the real object: %v", err)
	}
	actual, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, existing.Name, metav1.GetOptions{})
	if err != nil || actual.ResourceVersion != existing.ResourceVersion || len(actual.Data) != 1 {
		t.Fatalf("denied existing update persisted: %v", err)
	}
	for _, name := range []string{"readable-missing", "forbidden-lookup"} {
		if _, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatalf("denied create persisted: %v", err)
		}
	}
}

func testDeliveryResponseLoss(t *testing.T) {
	f := testcluster.NewManifestDelivery(t)
	ctx := deliveryContext(t)
	var creates, lostWrites atomic.Int32
	var injected atomic.Bool
	cfg := rest.CopyConfig(f.Config)
	cfg.Wrap(func(base http.RoundTripper) http.RoundTripper {
		return faultRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodPost && req.URL.Query().Get("dryRun") == "" && strings.HasSuffix(req.URL.Path, "/configmaps") && creates.Add(1) == 2 {
				return (afterCommitFailure{base, http.MethodPost, &lostWrites, &injected}).RoundTrip(req)
			}
			return base.RoundTrip(req)
		})
	})
	c := deliveryBackend(t, cfg)
	ops := operation.NewService(c, "delivery-e2e")
	source := []byte(deliveryConfig("first", "value", "accepted") + "---\n" + deliveryConfig("uncertain", "value", "committed") + "---\n" + deliveryConfig("last", "value", "not-submitted"))
	set := importDelivery(t, ctx, c.Backend, source, f.Namespace)
	first, err := delivery.Prepare(ctx, ops, set, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	accepted := submitDelivery(t, ctx, ops, first)
	p, err := delivery.Prepare(ctx, ops, set, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ops.Execute(ctx, p)
	if err == nil || result.State != "Unknown" || creates.Load() != 2 || lostWrites.Load() != 1 || !injected.Load() || c.writes != 2 {
		t.Fatalf("committed response loss was not one Unknown write: %s %v", result.State, err)
	}
	// This independent admin client receives the real server result, not the
	// injected response. The later item remains unsubmitted and the first stays.
	uncertain, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, "uncertain", metav1.GetOptions{})
	if err != nil || uncertain.Data["value"] != "committed" {
		t.Fatalf("fault did not follow an actual commit: %v", err)
	}
	if readDelivery(t, ctx, c.Backend, set.Items()[0]).GetUID() != accepted.Object.GetUID() {
		t.Fatal("Unknown outcome rolled back earlier acceptance")
	}
	absentDelivery(t, ctx, c.Backend, set.Items()[2])
	if _, err := ops.Execute(ctx, p); err == nil || creates.Load() != 2 || c.writes != 2 {
		t.Fatal("Unknown delivery was replayed")
	}
	fresh := importDelivery(t, ctx, c.Backend, source, f.Namespace)
	if next, err := delivery.Prepare(ctx, ops, fresh, 1, false); next != nil || !errors.Is(err, delivery.ErrAcknowledgementRequired) {
		t.Fatal("reconciliation did not require existing-resource acknowledgment")
	}
	reviewed, err := delivery.Prepare(ctx, ops, fresh, 1, true)
	if err != nil || reviewed.Kind() != "apply" || reviewed.Target().UID != uncertain.UID || reviewed.ID() == p.ID() || c.writes != 2 {
		t.Fatalf("fresh reconciliation reused an approval or wrong incarnation: %v", err)
	}
	submitDelivery(t, ctx, ops, reviewed)
	absentDelivery(t, ctx, c.Backend, fresh.Items()[2])
	last, err := delivery.Prepare(ctx, ops, fresh, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	submitDelivery(t, ctx, ops, last)
	if creates.Load() != 3 || c.writes != 4 || readDelivery(t, ctx, c.Backend, fresh.Items()[1]).GetUID() != uncertain.UID {
		t.Fatal("explicit per-object continuation recreated or repeated progress")
	}
}
