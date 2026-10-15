package helmrelease

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func metadataItem(name string, version int) metav1.PartialObjectMetadata {
	key := "sh.helm.release.v1." + name + ".v" + strconv.Itoa(version)
	return metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: key, Namespace: "team", UID: types.UID(key), ResourceVersion: "4", Labels: map[string]string{"owner": "helm", "name": name, "version": strconv.Itoa(version), "status": "deployed"}, Annotations: map[string]string{"private": "DO_NOT_PROJECT"}}}
}
func metadataPage(items ...metav1.PartialObjectMetadata) []byte {
	if items == nil {
		items = []metav1.PartialObjectMetadata{}
	}
	b, _ := json.Marshal(metav1.PartialObjectMetadataList{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadataList"}, ListMeta: metav1.ListMeta{ResourceVersion: "snapshot"}, Items: items})
	return b
}
func TestHelmReleaseMetadataProjectionAndNumericOrdering(t *testing.T) {
	data := metadataPage(metadataItem("z", 1), metadataItem("app", 9), metadataItem("app", 10), metadataItem("app", 1))
	before := append([]byte{}, data...)
	p, err := DecodePage(data, "team")
	if err != nil {
		t.Fatal(err)
	}
	Sort(p.Revisions)
	want := []Revision{{"app", 10, "deployed", "sh.helm.release.v1.app.v10", "sh.helm.release.v1.app.v10", "4"}, {"app", 9, "deployed", "sh.helm.release.v1.app.v9", "sh.helm.release.v1.app.v9", "4"}, {"app", 1, "deployed", "sh.helm.release.v1.app.v1", "sh.helm.release.v1.app.v1", "4"}, {"z", 1, "deployed", "sh.helm.release.v1.z.v1", "sh.helm.release.v1.z.v1", "4"}}
	if !reflect.DeepEqual(p.Revisions, want) || !reflect.DeepEqual(before, data) {
		t.Fatalf("incorrect projection: %+v", p)
	}
	encoded, _ := json.Marshal(p)
	if strings.Contains(string(encoded), "DO_NOT_PROJECT") {
		t.Fatal("retained arbitrary metadata")
	}
	empty, err := DecodePage(metadataPage(), "team")
	if err != nil || len(empty.Revisions) != 0 {
		t.Fatal("empty metadata list rejected", err)
	}
	for _, status := range []string{"unknown", "deployed", "uninstalled", "superseded", "failed", "uninstalling", "pending-install", "pending-upgrade", "pending-rollback"} {
		item := metadataItem("dotted.app", 2147483647)
		item.Labels["status"] = status
		if _, err := DecodePage(metadataPage(item), "team"); err != nil {
			t.Fatal(status, err)
		}
	}
}
func TestHelmReleaseMetadataMalformedAndBudgetBoundaries(t *testing.T) {
	for name, modify := range map[string]func(*metav1.PartialObjectMetadata){
		"namespace":       func(m *metav1.PartialObjectMetadata) { m.Namespace = "other" },
		"uid":             func(m *metav1.PartialObjectMetadata) { m.UID = "" },
		"revision-object": func(m *metav1.PartialObjectMetadata) { m.ResourceVersion = "" },
		"owner":           func(m *metav1.PartialObjectMetadata) { m.Labels["owner"] = "not-helm" },
		"release":         func(m *metav1.PartialObjectMetadata) { m.Labels["name"] = "PRIVATE invalid" },
		"long-release":    func(m *metav1.PartialObjectMetadata) { m.Labels["name"] = strings.Repeat("a", 54) },
		"storage":         func(m *metav1.PartialObjectMetadata) { m.Name = "arbitrary" },
		"zero":            func(m *metav1.PartialObjectMetadata) { m.Labels["version"] = "0" },
		"negative":        func(m *metav1.PartialObjectMetadata) { m.Labels["version"] = "-1" },
		"leading-zero":    func(m *metav1.PartialObjectMetadata) { m.Labels["version"] = "01" },
		"plus":            func(m *metav1.PartialObjectMetadata) { m.Labels["version"] = "+1" },
		"overflow":        func(m *metav1.PartialObjectMetadata) { m.Labels["version"] = "2147483648" },
		"status":          func(m *metav1.PartialObjectMetadata) { m.Labels["status"] = "PRIVATE_UNKNOWN" },
		"terminating":     func(m *metav1.PartialObjectMetadata) { v := metav1.Now(); m.DeletionTimestamp = &v },
		"full-item":       func(m *metav1.PartialObjectMetadata) { m.Kind = "Secret"; m.APIVersion = "v1" },
	} {
		t.Run(name, func(t *testing.T) {
			m := metadataItem("app", 1)
			modify(&m)
			p, err := DecodePage(metadataPage(m), "team")
			if !errors.Is(err, ErrInvalid) || len(p.Revisions) != 0 {
				t.Fatalf("%+v %v", p, err)
			}
			if strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("private metadata echoed")
			}
		})
	}
	base := string(metadataPage(metadataItem("app", 1)))
	for name, body := range map[string]string{
		"duplicate-key": strings.Replace(base, `"status":"deployed"`, `"status":"deployed","status":"failed"`, 1),
		"secret-data":   strings.Replace(base, `"items":[{`, `"items":[{"data":{"release":"PRIVATE"},`, 1),
		"envelope-data": strings.Replace(base, `"items":`, `"data":{"release":"PRIVATE"},"items":`, 1),
		"trailing":      base + `{}`,
		"missing-items": `{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadataList","metadata":{"resourceVersion":"1"}}`,
		"null-items":    `{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadataList","metadata":{"resourceVersion":"1"},"items":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePage([]byte(body), "team"); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	for _, ns := range []string{"", "*", "team/../other", "TEAM", strings.Repeat("x", 64)} {
		if _, err := DecodePage([]byte(base), ns); !errors.Is(err, ErrNamespace) {
			t.Fatal(ns, err)
		}
	}
	if _, err := DecodePage([]byte(strings.Repeat("x", MaxPageBytes+1)), "team"); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	items := make([]metav1.PartialObjectMetadata, PageSize+1)
	for i := range items {
		items[i] = metadataItem("app", i+1)
	}
	if _, err := DecodePage(metadataPage(items...), "team"); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	var list map[string]any
	_ = json.Unmarshal([]byte(base), &list)
	list["metadata"].(map[string]any)["continue"] = strings.Repeat("x", MaxContinueBytes+1)
	b, _ := json.Marshal(list)
	if _, err := DecodePage(b, "team"); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
}
