package helmrelease

import (
	"bytes"
	"encoding/json"
	"errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	strictjson "sigs.k8s.io/json"
)

// DecodeInstallMetadataPage reuses the strict metadata decoder and additionally
// accepts the API server's terminal empty-list representation, items:null. The
// metadata transform builds a nil slice when no Secret matches owner=helm.
// Only install preparation needs to establish this empty-history observation;
// the existing inventory decoder's stricter contract is unchanged.
func DecodeInstallMetadataPage(data []byte, ns string) (Page, error) {
	page, err := DecodePage(data, ns)
	if !errors.Is(err, ErrInvalid) {
		return page, err
	}
	// Do not repair arbitrary invalid metadata, omitted items, partial pages,
	// duplicates, full Secret objects or contradictory remaining-item counts.
	var wire struct {
		metav1.TypeMeta `json:",inline"`
		Metadata        metav1.ListMeta `json:"metadata"`
		Items           json.RawMessage `json:"items"`
	}
	issues, decodeErr := strictjson.UnmarshalStrict(data, &wire)
	if decodeErr != nil || len(issues) != 0 || !bytes.Equal(bytes.TrimSpace(wire.Items), []byte("null")) ||
		wire.Metadata.Continue != "" || wire.Metadata.RemainingItemCount != nil && *wire.Metadata.RemainingItemCount != 0 {
		return Page{}, ErrInvalid
	}
	wire.Items = json.RawMessage("[]")
	canonical, marshalErr := json.Marshal(wire)
	if marshalErr != nil {
		return Page{}, ErrInvalid
	}
	// The original decoder still validates the envelope, namespace, snapshot
	// resourceVersion and every budget; no content/identity is synthesized.
	return DecodePage(canonical, ns)
}
