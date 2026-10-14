//go:build e2e

package e2e

import (
	"errors"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type integerSetServerCause struct {
	kind  metav1.CauseType
	field string
}

// The pinned three-version matrix differs only in this characterized fractional
// input response. Keep exact type/path multiplicities; do not ignore arbitrary
// extra causes, accept a successful write, or relax the local integer domain.
func integerSetFractionCauses(serverVersion string) ([]integerSetServerCause, error) {
	causes := []integerSetServerCause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}}
	switch serverVersion {
	case "v1.35.8":
		return causes, nil
	case "v1.36.4", "v1.37.0":
		return append(causes, integerSetServerCause{metav1.CauseTypeFieldValueInvalid, "<nil>"}), nil
	default:
		return nil, errors.New("unreviewed Kubernetes server version for integer-set cause oracle")
	}
}

func TestIntegerSetFractionCauseOraclePinnedVersions(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    []integerSetServerCause
	}{
		{"v1.35.8", []integerSetServerCause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}}},
		{"v1.36.4", []integerSetServerCause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}, {metav1.CauseTypeFieldValueInvalid, "<nil>"}}},
		{"v1.37.0", []integerSetServerCause{{metav1.CauseTypeTypeInvalid, "spec.values[0]"}, {metav1.CauseTypeFieldValueInvalid, "<nil>"}}},
	} {
		t.Run(tc.version, func(t *testing.T) {
			got, err := integerSetFractionCauses(tc.version)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatal("pinned fractional rejection cause oracle changed")
			}
			got[0].field = "mutated"
			again, err := integerSetFractionCauses(tc.version)
			if err != nil || !reflect.DeepEqual(again, tc.want) {
				t.Fatal("caller mutation changed the expected cause oracle")
			}
		})
	}
	for _, version := range []string{"", "v1.35.9", "v1.36.5", "v1.38.0", "v1.37.0-unreviewed"} {
		if got, err := integerSetFractionCauses(version); err == nil || got != nil {
			t.Fatal("unreviewed version silently reused a pinned oracle")
		}
	}
}
