package resourcemetrics

import (
	"context"
	"testing"
	"time"

	identity "github.com/laojianzi/aster/internal/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var testNow = time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)

func fixture() (identity.Identity, *unstructured.Unstructured, *unstructured.Unstructured) {
	target := identity.Identity{SessionID: "test", GVR: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Namespace: "team", Name: "pod", UID: "uid"}
	root := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]interface{}{"name": "pod", "namespace": "team", "uid": "uid", "creationTimestamp": testNow.Add(-time.Hour).Format(time.RFC3339)}, "spec": map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "app"}}}}}
	sample := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "metrics.k8s.io/v1beta1", "kind": "PodMetrics", "metadata": map[string]interface{}{"name": "pod", "namespace": "team"}, "timestamp": testNow.Add(-15 * time.Second).Format(time.RFC3339), "window": "15s", "containers": []interface{}{map[string]interface{}{"name": "app", "usage": map[string]interface{}{"cpu": "125000000n", "memory": "64Mi"}}}}}
	return target, root, sample
}
func TestMetricsDecodeQuantitiesAndMissingIsNotZero(t *testing.T) {
	target, root, sample := fixture()
	got := Decode(target, root, sample, testNow)
	if got.State != Ready || got.Total.CPUCores != 0.125 || got.Total.MemoryBytes != 64<<20 || len(got.Entries) != 1 {
		t.Fatalf("%+v", got)
	}
	for _, version := range []string{"metrics.k8s.io/v1", "metrics.k8s.io/v1beta1"} {
		sample.SetAPIVersion(version)
		if Decode(target, root, sample, testNow).State != Ready {
			t.Fatal(version)
		}
	}
	delete(sample.Object["containers"].([]interface{})[0].(map[string]interface{})["usage"].(map[string]interface{}), "cpu")
	got = Decode(target, root, sample, testNow)
	if got.State != Invalid || len(got.Entries) != 0 {
		t.Fatal("missing CPU became zero usage")
	}
}
func TestMetricsRejectInvalidIdentityAndSamples(t *testing.T) {
	cases := []struct {
		name   string
		change func(*unstructured.Unstructured)
		state  State
	}{
		{"wrong name", func(o *unstructured.Unstructured) { o.SetName("other") }, Invalid},
		{"wrong namespace", func(o *unstructured.Unstructured) { o.SetNamespace("other") }, Invalid},
		{"wrong kind", func(o *unstructured.Unstructured) { o.SetKind("NodeMetrics") }, Invalid},
		{"wrong UID", func(o *unstructured.Unstructured) { o.SetUID("other") }, Replaced},
		{"unknown API", func(o *unstructured.Unstructured) { o.SetAPIVersion("metrics.k8s.io/v99") }, Invalid},
		{"stale", func(o *unstructured.Unstructured) {
			o.Object["timestamp"] = testNow.Add(-3 * time.Minute).Format(time.RFC3339)
		}, Stale},
		{"future", func(o *unstructured.Unstructured) {
			o.Object["timestamp"] = testNow.Add(time.Minute).Format(time.RFC3339)
		}, Invalid},
		{"predates pod", func(o *unstructured.Unstructured) {
			o.Object["timestamp"] = testNow.Add(-2 * time.Hour).Format(time.RFC3339)
		}, Invalid},
		{"zero window", func(o *unstructured.Unstructured) { o.Object["window"] = "0s" }, Invalid},
		{"excessive window", func(o *unstructured.Unstructured) { o.Object["window"] = "1h" }, Invalid},
		{"invalid time", func(o *unstructured.Unstructured) { o.Object["timestamp"] = "bad" }, Invalid},
		{"empty containers", func(o *unstructured.Unstructured) { o.Object["containers"] = []interface{}{} }, Invalid},
		{"duplicate containers", func(o *unstructured.Unstructured) {
			a := o.Object["containers"].([]interface{})
			o.Object["containers"] = append(a, a[0])
		}, Invalid},
		{"unknown container", func(o *unstructured.Unstructured) {
			o.Object["containers"].([]interface{})[0].(map[string]interface{})["name"] = "other"
		}, Invalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, root, sample := fixture()
			tc.change(sample)
			if got := Decode(target, root, sample, testNow); got.State != tc.state {
				t.Fatalf("got %s, want %s", got.State, tc.state)
			}
		})
	}
	for _, bad := range []string{"-1m", "garbage", "1e999", "", "NaN"} {
		t.Run("quantity_"+bad, func(t *testing.T) {
			target, root, sample := fixture()
			sample.Object["containers"].([]interface{})[0].(map[string]interface{})["usage"].(map[string]interface{})["cpu"] = bad
			if Decode(target, root, sample, testNow).State != Invalid {
				t.Fatal("accepted invalid quantity")
			}
		})
	}
	target, root, sample := fixture()
	items, _, _ := unstructured.NestedSlice(root.Object, "spec", "containers")
	items = append(items, map[string]interface{}{"name": "missing"})
	_ = unstructured.SetNestedSlice(root.Object, items, "spec", "containers")
	if got := Decode(target, root, sample, testNow); got.State != Partial || got.Total.MemoryBytes != 64<<20 {
		t.Fatal("partial container sample not labeled")
	}
}
func TestNodeMetricsAndHistoryBounds(t *testing.T) {
	target, root, sample := fixture()
	target.GVR.Resource = "nodes"
	target.Namespace = ""
	root.SetKind("Node")
	root.SetNamespace("")
	sample.SetKind("NodeMetrics")
	sample.SetNamespace("")
	sample.Object["usage"] = map[string]interface{}{"cpu": "2", "memory": "1Gi"}
	delete(sample.Object, "containers")
	got := Decode(target, root, sample, testNow)
	if got.State != Ready || got.Total.CPUCores != 2 || got.Total.MemoryBytes != 1<<30 {
		t.Fatalf("%+v", got)
	}
	var h History
	for i := 0; i < 200; i++ {
		s := got
		s.Timestamp = testNow.Add(time.Duration(i) * time.Second)
		s.ReceivedAt = s.Timestamp
		h.Add(s)
		h.Add(s)
	}
	if len(h.Points) != MaxPoints {
		t.Fatal("history is not bounded or duplicated samples")
	}
	h.Add(Snapshot{State: Forbidden, ReceivedAt: testNow.Add(201 * time.Second)})
	if h.Points[len(h.Points)-1].Valid {
		t.Fatal("a failed sample became a zero usage point")
	}
}

type stubClient struct {
	root, sample *unstructured.Unstructured
	gets         int
	replace      bool
	err          error
}

func (c *stubClient) GetObject(ctx context.Context, _ schema.GroupVersionResource, _, _ string) (*unstructured.Unstructured, error) {
	c.gets++
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	obj := c.root.DeepCopy()
	if c.replace && c.gets == 2 {
		obj.SetUID("new")
	}
	return obj, nil
}
func (c *stubClient) MetricsObject(context.Context, schema.GroupVersionResource, string, string) (*unstructured.Unstructured, error) {
	return c.sample, c.err
}
func TestMetricsReadRechecksLiveUIDAndClassifiesErrors(t *testing.T) {
	target, root, sample := fixture()
	sample.Object["timestamp"] = time.Now().UTC().Format(time.RFC3339)
	root.Object["metadata"].(map[string]interface{})["creationTimestamp"] = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	c := &stubClient{root: root, sample: sample, replace: true}
	if got := Read(context.Background(), c, target); got.State != Replaced || len(got.Entries) != 0 || c.gets != 2 {
		t.Fatalf("%+v gets=%d", got, c.gets)
	}
	for _, tc := range []struct {
		err   error
		state State
	}{{ErrNotInstalled, NotInstalled}, {ErrNoSample, NoSample}, {ErrForbidden, Forbidden}, {ErrInvalid, Invalid}, {ErrUnavailable, Unavailable}} {
		c := &stubClient{root: root, err: tc.err}
		if got := Read(context.Background(), c, target); got.State != tc.state || len(got.Entries) != 0 {
			t.Fatalf("%+v", got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if Read(ctx, &stubClient{root: root}, target).State != Canceled {
		t.Fatal("cancellation ignored")
	}
}
