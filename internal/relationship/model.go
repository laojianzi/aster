// Package relationship resolves bounded, read-only, one-hop resource links.
// Owner references, selector membership and name references are deliberately
// distinct: no relationship returned here grants permission or proves traffic.
package relationship

import (
	"context"
	"errors"
	"time"

	"github.com/laojianzi/aster/internal/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type Client interface {
	GetObject(context.Context, schema.GroupVersionResource, string, string) (*unstructured.Unstructured, error)
	ListPage(context.Context, schema.GroupVersionResource, string, metav1.ListOptions) (*unstructured.UnstructuredList, error)
}
type Type struct {
	GVR        schema.GroupVersionResource
	Kind       string
	Namespaced bool
}
type State string

const (
	Verified    State = "Verified"
	Referenced  State = "Name reference"
	Forbidden   State = "Forbidden"
	Missing     State = "Missing"
	Replaced    State = "Replaced"
	Unsupported State = "Unsupported"
	Unavailable State = "Unavailable"
)

type Link struct {
	Target   resource.Identity
	Type     Type
	Relation string
	State    State
	Note     string
}

func (l Link) Navigable() bool { return l.State == Verified && l.Target.UID != "" }

type Snapshot struct {
	Target            resource.Identity
	SourceVersion     string
	ObservedAt        time.Time
	Links             []Link
	Warnings          []string
	Incomplete        bool
	Requests, Scanned int
}

var ErrReplaced = errors.New("relationship root was replaced; refresh the resource")

type Limits struct {
	Requests, Objects, Links, PageSize int
	Timeout                            time.Duration
}

func DefaultLimits() Limits {
	return Limits{Requests: 12, Objects: 1000, Links: 200, PageSize: 200, Timeout: 15 * time.Second}
}

type Reader struct {
	client Client
	types  map[schema.GroupVersionKind]Type
	limits Limits
}

func New(client Client, types []Type, limits Limits) *Reader {
	defaults := DefaultLimits()
	if limits.Requests <= 0 || limits.Requests > defaults.Requests {
		limits.Requests = defaults.Requests
	}
	if limits.Objects <= 0 || limits.Objects > defaults.Objects {
		limits.Objects = defaults.Objects
	}
	if limits.Links <= 0 || limits.Links > defaults.Links {
		limits.Links = defaults.Links
	}
	if limits.PageSize <= 0 || limits.PageSize > defaults.PageSize {
		limits.PageSize = defaults.PageSize
	}
	if limits.Timeout <= 0 || limits.Timeout > defaults.Timeout {
		limits.Timeout = defaults.Timeout
	}
	r := &Reader{client: client, types: make(map[schema.GroupVersionKind]Type), limits: limits}
	// Built-in identities are fixed; discovery only adds explicitly known custom
	// versions. Never derive an unknown resource plural from its kind string.
	for _, typ := range append(append([]Type(nil), types...), Builtins()...) {
		r.types[typ.GVR.GroupVersion().WithKind(typ.Kind)] = typ
	}
	return r
}
func Builtins() []Type {
	return []Type{
		{schema.GroupVersionResource{Version: "v1", Resource: "pods"}, "Pod", true},
		{schema.GroupVersionResource{Version: "v1", Resource: "services"}, "Service", true},
		{schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, "ConfigMap", true},
		{schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, "Secret", true},
		{schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}, "PersistentVolumeClaim", true},
		{schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}, "ServiceAccount", true},
		{schema.GroupVersionResource{Version: "v1", Resource: "nodes"}, "Node", false},
		{schema.GroupVersionResource{Version: "v1", Resource: "replicationcontrollers"}, "ReplicationController", true},
		{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, "Deployment", true},
		{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, "ReplicaSet", true},
		{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, "StatefulSet", true},
		{schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, "DaemonSet", true},
		{schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, "Job", true},
		{schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, "CronJob", true},
		{schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}, "EndpointSlice", true},
	}
}
