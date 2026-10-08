// Package resourcemetrics reads recent resource usage, not a monitoring history.
// Missing, partial and stale samples are never converted into zero usage.
package resourcemetrics

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"

	identity "github.com/laojianzi/aster/internal/resource"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	quantity "k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type State string

const (
	Ready        State = "Available"
	Partial      State = "Partial sample"
	Stale        State = "Stale sample"
	NotInstalled State = "Metrics API not installed"
	NoSample     State = "No sample yet"
	Forbidden    State = "Metrics access denied"
	Unavailable  State = "Metrics unavailable"
	Invalid      State = "Invalid metrics sample"
	Replaced     State = "Resource replaced"
	Unsupported  State = "Unsupported resource"
	Canceled     State = "Stopped"
)

var (
	ErrUnsupported  = errors.New("unsupported metrics API version")
	ErrNotInstalled = errors.New("metrics API not installed")
	ErrNoSample     = errors.New("metrics sample not found")
	ErrForbidden    = errors.New("metrics access denied")
	ErrUnavailable  = errors.New("metrics service unavailable")
	ErrInvalid      = errors.New("invalid metrics response")
)

type Client interface {
	GetObject(context.Context, schema.GroupVersionResource, string, string) (*unstructured.Unstructured, error)
	MetricsObject(context.Context, schema.GroupVersionResource, string, string) (*unstructured.Unstructured, error)
}
type Usage struct {
	Name                  string
	CPUCores, MemoryBytes float64
}
type Snapshot struct {
	Target                identity.Identity
	State                 State
	Timestamp, ReceivedAt time.Time
	Window                time.Duration
	APIVersion            string
	Entries               []Usage
	Total                 Usage
}

func Supported(gvr schema.GroupVersionResource) bool {
	return gvr.Group == "" && gvr.Version == "v1" && (gvr.Resource == "pods" || gvr.Resource == "nodes")
}
func stateFor(err error) State {
	switch {
	case errors.Is(err, context.Canceled):
		return Canceled
	case errors.Is(err, ErrUnsupported):
		return Unsupported
	case errors.Is(err, ErrNotInstalled):
		return NotInstalled
	case errors.Is(err, ErrNoSample), apierrors.IsNotFound(err):
		return NoSample
	case errors.Is(err, ErrForbidden), apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return Forbidden
	case errors.Is(err, ErrInvalid):
		return Invalid
	default:
		return Unavailable
	}
}
func identityMatches(obj *unstructured.Unstructured, target identity.Identity) bool {
	kind := "Pod"
	if target.GVR.Resource == "nodes" {
		kind = "Node"
	}
	return obj != nil && obj.GetUID() == target.UID && obj.GetName() == target.Name && obj.GetNamespace() == target.Namespace && obj.GetAPIVersion() == "v1" && obj.GetKind() == kind
}

// Read has a shared ten-second deadline for identity checks, API discovery and
// one named metrics GET. No collection scan or credential fallback is performed.
func Read(parent context.Context, client Client, target identity.Identity) Snapshot {
	out := Snapshot{Target: target, State: Unsupported}
	if target.Validate() != nil || target.UID == "" || !Supported(target.GVR) || (target.GVR.Resource == "pods" && (target.Namespace == "" || target.Namespace == "*")) || (target.GVR.Resource == "nodes" && target.Namespace != "") || client == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	root, err := client.GetObject(ctx, target.GVR, target.Namespace, target.Name)
	if err != nil {
		out.State = stateFor(err)
		return out
	}
	if !identityMatches(root, target) {
		out.State = Replaced
		return out
	}
	obj, err := client.MetricsObject(ctx, target.GVR, target.Namespace, target.Name)
	if err != nil {
		out.State = stateFor(err)
		return out
	}
	out = Decode(target, root, obj, time.Now().UTC())
	if out.State == Invalid || out.State == Replaced {
		return out
	}
	current, err := client.GetObject(ctx, target.GVR, target.Namespace, target.Name)
	if err != nil {
		return Snapshot{Target: target, State: stateFor(err)}
	}
	if !identityMatches(current, target) {
		return Snapshot{Target: target, State: Replaced}
	}
	return out
}

// Decode validates an untrusted sample; limits bound the retained projection.
// Metrics servers often omit UID. The caller therefore checks live identity on
// both sides of the metrics read. This is not an atomic cross-API snapshot.
func Decode(target identity.Identity, root, obj *unstructured.Unstructured, now time.Time) Snapshot {
	bad := Snapshot{Target: target, State: Invalid, ReceivedAt: now}
	if !Supported(target.GVR) || !identityMatches(root, target) || obj == nil {
		return bad
	}
	kind := "PodMetrics"
	if target.GVR.Resource == "nodes" {
		kind = "NodeMetrics"
	}
	version := obj.GetAPIVersion()
	if (version != "metrics.k8s.io/v1" && version != "metrics.k8s.io/v1beta1") || obj.GetKind() != kind || obj.GetName() != target.Name || obj.GetNamespace() != target.Namespace {
		return bad
	}
	if obj.GetUID() != "" && obj.GetUID() != target.UID {
		bad.State = Replaced
		return bad
	}
	stamp, _, err := unstructured.NestedString(obj.Object, "timestamp")
	if err != nil || len(stamp) > 64 {
		return bad
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || at.IsZero() {
		return bad
	}
	window, _, err := unstructured.NestedString(obj.Object, "window")
	if err != nil || len(window) > 32 {
		return bad
	}
	duration, err := time.ParseDuration(window)
	if err != nil || duration <= 0 || duration > 10*time.Minute || at.After(now.Add(30*time.Second)) {
		return bad
	}
	if birth := root.GetCreationTimestamp(); !birth.IsZero() && at.Before(birth.Time) {
		return bad
	}
	out := Snapshot{Target: target, State: Ready, Timestamp: at, ReceivedAt: now, Window: duration, APIVersion: version}
	if kind == "NodeMetrics" {
		usage, ok := decodeUsage(obj.Object, "Node")
		if !ok {
			return bad
		}
		out.Entries = []Usage{usage}
	} else {
		expected := map[string]bool{}
		allowed := map[string]bool{}
		for _, field := range []string{"containers", "initContainers", "ephemeralContainers"} {
			items, _, err := unstructured.NestedSlice(root.Object, "spec", field)
			if err != nil || len(items) > 256 {
				return bad
			}
			for _, item := range items {
				m, ok := item.(map[string]interface{})
				if !ok {
					return bad
				}
				name, ok := m["name"].(string)
				if !ok || name == "" {
					return bad
				}
				allowed[name] = true
				if field == "containers" {
					expected[name] = true
				}
			}
		}
		containers, found, err := unstructured.NestedSlice(obj.Object, "containers")
		if err != nil || !found || len(containers) == 0 || len(containers) > 256 {
			return bad
		}
		seen := map[string]bool{}
		for _, item := range containers {
			m, ok := item.(map[string]interface{})
			if !ok {
				return bad
			}
			name, ok := m["name"].(string)
			if !ok || name == "" || len(name) > 253 || !allowed[name] || seen[name] {
				return bad
			}
			usage, ok := decodeUsage(m, name)
			if !ok {
				return bad
			}
			seen[name] = true
			out.Entries = append(out.Entries, usage)
		}
		for name := range expected {
			if !seen[name] {
				out.State = Partial
			}
		}
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Name < out.Entries[j].Name })
	for _, v := range out.Entries {
		out.Total.CPUCores += v.CPUCores
		out.Total.MemoryBytes += v.MemoryBytes
	}
	if now.Sub(at) > 2*time.Minute {
		out.State = Stale
	}
	return out
}
func decodeUsage(obj map[string]interface{}, name string) (Usage, bool) {
	values, found, err := unstructured.NestedStringMap(obj, "usage")
	if err != nil || !found {
		return Usage{}, false
	}
	cpu, ok := number(values["cpu"], 1e6)
	if !ok {
		return Usage{}, false
	}
	memory, ok := number(values["memory"], float64(uint64(1)<<60))
	if !ok {
		return Usage{}, false
	}
	return Usage{Name: name, CPUCores: cpu, MemoryBytes: memory}, true
}
func number(text string, maximum float64) (float64, bool) {
	if text == "" || len(text) > 128 {
		return 0, false
	}
	q, err := quantity.ParseQuantity(text)
	if err != nil || q.Sign() < 0 {
		return 0, false
	}
	n := q.AsApproximateFloat64()
	return n, !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= maximum
}

const MaxPoints = 60

type Point struct {
	At                    time.Time
	CPUCores, MemoryBytes float64
	Valid                 bool
}
type History struct {
	Points     []Point
	lastSample time.Time
}

func (h *History) Add(s Snapshot) {
	if s.ReceivedAt.IsZero() {
		s.ReceivedAt = time.Now().UTC()
	}
	valid := s.State == Ready
	if valid && !s.Timestamp.After(h.lastSample) {
		return
	}
	if valid {
		h.lastSample = s.Timestamp
	}
	h.Points = append(h.Points, Point{At: s.ReceivedAt, CPUCores: s.Total.CPUCores, MemoryBytes: s.Total.MemoryBytes, Valid: valid})
	if len(h.Points) > MaxPoints {
		copy(h.Points, h.Points[len(h.Points)-MaxPoints:])
		h.Points = h.Points[:MaxPoints]
	}
}
