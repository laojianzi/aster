// Package helmrelease projects bounded Helm Secret-driver metadata. It never
// decodes a release payload or treats editable labels as authoritative state.
package helmrelease

import (
	"errors"
	"sort"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	strictjson "sigs.k8s.io/json"
)

const (
	Accept           = "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1"
	PageSize         = 64
	MaxRevisions     = 512
	MaxPages         = 16
	MaxPageBytes     = 256 << 10
	MaxTotalBytes    = 2 << 20
	MaxContinueBytes = 4096
	ReadTimeout      = 10 * time.Second
)

var (
	ErrNamespace   = errors.New("Helm metadata requires one valid namespace.")
	ErrForbidden   = errors.New("Helm metadata is unavailable to the current identity (list secrets required).")
	ErrUnsupported = errors.New("The server did not provide metadata-only Secret results; no full-object fallback was attempted.")
	ErrInvalid     = errors.New("Helm metadata is malformed or inconsistent; no partial inventory is shown.")
	ErrBudget      = errors.New("Helm metadata exceeded the bounded read budget; no partial inventory is shown.")
	ErrUnavailable = errors.New("Helm metadata could not be read; no fallback or write was attempted.")
	ErrExpired     = errors.New("Helm metadata pagination expired; explicitly refresh to start a new snapshot.")
)

// Revision contains only validated public identity/status labels. Status is an
// observation, not a decoded Helm state, readiness, ownership or write approval.
type Revision struct {
	Release     string
	Number      int32
	Status      string
	StorageName string
}

type Snapshot struct {
	Namespace       string
	ResourceVersion string
	ObservedAt      time.Time
	Revisions       []Revision
}

type Page struct {
	ResourceVersion string
	Continue        string
	Revisions       []Revision
	UIDs            []string
}

func ValidNamespace(ns string) bool {
	return ns != "" && len(ns) <= 63 && len(validation.IsDNS1123Label(ns)) == 0
}

func knownStatus(s string) bool {
	switch s {
	case "unknown", "deployed", "uninstalled", "superseded", "failed", "uninstalling", "pending-install", "pending-upgrade", "pending-rollback":
		return true
	}
	return false
}

// DecodePage rejects duplicate JSON fields and non-metadata envelopes, including
// any unexpected Secret data. Arbitrary annotations/labels are not projected.
func DecodePage(data []byte, ns string) (Page, error) {
	if !ValidNamespace(ns) {
		return Page{}, ErrNamespace
	}
	if len(data) > MaxPageBytes {
		return Page{}, ErrBudget
	}
	var wire struct {
		metav1.TypeMeta `json:",inline"`
		Metadata        metav1.ListMeta                 `json:"metadata"`
		Items           *[]metav1.PartialObjectMetadata `json:"items"`
	}
	issues, err := strictjson.UnmarshalStrict(data, &wire)
	if err != nil || len(issues) != 0 {
		return Page{}, ErrInvalid
	}
	if wire.APIVersion != "meta.k8s.io/v1" || wire.Kind != "PartialObjectMetadataList" {
		return Page{}, ErrUnsupported
	}
	if wire.Items == nil || wire.Metadata.ResourceVersion == "" || len(wire.Metadata.ResourceVersion) > 256 {
		return Page{}, ErrInvalid
	}
	if len(wire.Metadata.Continue) > MaxContinueBytes || len(*wire.Items) > PageSize {
		return Page{}, ErrBudget
	}
	page := Page{ResourceVersion: wire.Metadata.ResourceVersion, Continue: wire.Metadata.Continue}
	for _, item := range *wire.Items {
		if (item.Kind != "" && item.Kind != "PartialObjectMetadata") || (item.APIVersion != "" && item.APIVersion != "meta.k8s.io/v1") || item.Namespace != ns || item.UID == "" || len(item.UID) > 128 || item.ResourceVersion == "" || len(item.ResourceVersion) > 256 || item.DeletionTimestamp != nil {
			return Page{}, ErrInvalid
		}
		labels := item.Labels
		name, version := labels["name"], labels["version"]
		number, err := strconv.ParseInt(version, 10, 32)
		if labels["owner"] != "helm" || len(name) == 0 || len(name) > 53 || len(validation.IsDNS1123Subdomain(name)) != 0 || err != nil || number < 1 || strconv.FormatInt(number, 10) != version || !knownStatus(labels["status"]) || item.Name != "sh.helm.release.v1."+name+".v"+version {
			return Page{}, ErrInvalid
		}
		page.Revisions = append(page.Revisions, Revision{Release: name, Number: int32(number), Status: labels["status"], StorageName: item.Name})
		page.UIDs = append(page.UIDs, string(item.UID))
	}
	return page, nil
}

// Sort is deterministic and numeric, never a lexical "v9 > v10" comparison.
func Sort(rows []Revision) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Release != rows[j].Release {
			return rows[i].Release < rows[j].Release
		}
		return rows[i].Number > rows[j].Number
	})
}
