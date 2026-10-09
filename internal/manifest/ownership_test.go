package manifest

import (
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func ownershipFixture() *unstructured.Unstructured {
	o := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "demo"}, "data": map[string]interface{}{"sensitive": "do-not-include-payload"}}}
	o.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "aster-apply", Operation: metav1.ManagedFieldsOperationApply, APIVersion: "v1", FieldsType: "FieldsV1", FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:data":{"f:z":{},"f:a":{}}}`)}}})
	return o
}
func TestOwnershipShowsManagerPathsNotResourceValues(t *testing.T) {
	o := ownershipFixture()
	got := Ownership(o)
	if !strings.Contains(got, "aster-apply · Apply · v1") || !strings.Contains(got, "/f:data/f:a") || strings.Index(got, "/f:data/f:z") < strings.Index(got, "/f:data/f:a") {
		t.Fatal(got)
	}
	if strings.Contains(got, "do-not-include-payload") {
		t.Fatal("resource content leaked into ownership")
	}
	before, err := o.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	o.SetManagedFields(nil)
	after, _ := o.MarshalJSON()
	review, err := OwnershipReview(before, after)
	if err != nil || !strings.Contains(review, "AFTER SERVER DRY-RUN") || !strings.Contains(review, "No field ownership") {
		t.Fatalf("%s %v", review, err)
	}
}
func TestOwnershipBoundsAndMalformedData(t *testing.T) {
	o := ownershipFixture()
	m := map[string]interface{}{}
	for i := 0; i < 2000; i++ {
		m[fmt.Sprintf("f:field-%04d", i)] = map[string]interface{}{}
	}
	fields := o.Object["metadata"].(map[string]interface{})["managedFields"].([]interface{})[0].(map[string]interface{})
	fields["fieldsV1"] = m
	got := Ownership(o)
	if len(got) > MaxOwnershipBytes || !strings.Contains(got, "truncated") {
		t.Fatal("unbounded field display")
	}
	fields["fieldsV1"] = map[string]interface{}{"f:x": "malformed"}
	fields["manager"] = "untrusted\x1b[31m\nmanager"
	got = Ownership(o)
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\nmanager") || !strings.Contains(got, "Malformed") {
		t.Fatal(got)
	}
	fields["fieldsType"] = "FutureFormat"
	if !strings.Contains(Ownership(o), "Unsupported") {
		t.Fatal("unsupported format presented as empty fields")
	}
	if _, err := OwnershipReview([]byte("broken"), []byte("{}")); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if Ownership(nil) == "" {
		t.Fatal("missing state")
	}
}
