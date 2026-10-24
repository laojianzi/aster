package helmrelease

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHelmInstallMetadataEmptyPage(t *testing.T) {
	// This is the actual type and zero Items initialization used by the API
	// server's asPartialObjectMetadataList transform, not a hand-written [].
	value := metav1.PartialObjectMetadataList{
		TypeMeta: metav1.TypeMeta{Kind: "PartialObjectMetadataList", APIVersion: "meta.k8s.io/v1"},
		ListMeta: metav1.ListMeta{ResourceVersion: "snapshot"},
	}
	data, err := json.Marshal(value)
	if err != nil || !bytes.Contains(data, []byte(`"items":null`)) {
		t.Fatal("server empty-list shape changed", err)
	}
	base := string(data)
	cases := map[string]struct {
		body string
		ok   bool
	}{
		"server-null":        {base, true},
		"empty-array":        {strings.Replace(base, `"items":null`, `"items":[]`, 1), true},
		"zero-remaining":     {strings.Replace(base, `"resourceVersion":"snapshot"`, `"resourceVersion":"snapshot","remainingItemCount":0`, 1), true},
		"missing-items":      {strings.Replace(base, `,"items":null`, "", 1), false},
		"partial":            {strings.Replace(base, `"resourceVersion":"snapshot"`, `"resourceVersion":"snapshot","continue":"next"`, 1), false},
		"positive-remaining": {strings.Replace(base, `"resourceVersion":"snapshot"`, `"resourceVersion":"snapshot","remainingItemCount":1`, 1), false},
		"negative-remaining": {strings.Replace(base, `"resourceVersion":"snapshot"`, `"resourceVersion":"snapshot","remainingItemCount":-1`, 1), false},
		"missing-rv":         {strings.Replace(base, `"snapshot"`, `""`, 1), false},
		"duplicate-items":    {strings.Replace(base, `"items":null`, `"items":[],"items":null`, 1), false},
		"duplicate-rv":       {strings.Replace(base, `"resourceVersion":"snapshot"`, `"resourceVersion":"old","resourceVersion":"snapshot"`, 1), false},
		"payload":            {strings.Replace(base, `"items":null`, `"data":{"release":"PRIVATE"},"items":null`, 1), false},
		"wrong-kind":         {strings.Replace(base, `"PartialObjectMetadataList"`, `"SecretList"`, 1), false},
		"wrong-version":      {strings.Replace(base, `"meta.k8s.io/v1"`, `"v1"`, 1), false},
		"trailing":           {base + `{}`, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			input := []byte(tc.body)
			before := append([]byte(nil), input...)
			page, err := DecodeInstallMetadataPage(input, "team")
			if (err == nil) != tc.ok || !bytes.Equal(input, before) {
				t.Fatal("wrong disposition or changed input", err)
			}
			if tc.ok && (page.ResourceVersion != "snapshot" || page.Continue != "" || len(page.Revisions) != 0 || len(page.UIDs) != 0) {
				t.Fatal("not an exact empty terminal observation")
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("private content disclosed")
			}
		})
	}
	if _, err := DecodeInstallMetadataPage(data, "other/namespace"); !errors.Is(err, ErrNamespace) {
		t.Fatal("namespace validation bypass", err)
	}
	if _, err := DecodeInstallMetadataPage(bytes.Repeat([]byte(" "), MaxPageBytes+1), "team"); !errors.Is(err, ErrBudget) {
		t.Fatal("budget bypass", err)
	}
	if _, err := DecodePage(data, "team"); !errors.Is(err, ErrInvalid) {
		t.Fatal("legacy decoder contract changed", err)
	}
	ordinary := metadataPage(metadataItem("example", 1))
	old, err := DecodePage(ordinary, "team")
	got, gotErr := DecodeInstallMetadataPage(ordinary, "team")
	if err != nil || gotErr != nil || !reflect.DeepEqual(old, got) {
		t.Fatal("nonempty classification was not reused", err, gotErr)
	}
}
