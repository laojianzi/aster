package helmrelease

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var statusTime = time.Date(2026, 10, 17, 10, 0, 0, 123456789, time.UTC)

func statusPayload(t *testing.T, wire []byte) []byte {
	t.Helper()
	var secret corev1.Secret
	if err := json.Unmarshal(wire, &secret); err != nil {
		t.Fatal(err)
	}
	compressed, err := base64.StdEncoding.DecodeString(string(secret.Data["release"]))
	if err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func applyStatusPatch(t *testing.T, wire, patch []byte) []byte {
	t.Helper()
	p, err := jsonpatch.DecodePatch(patch)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Apply(wire)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func statusFixture(payload []byte) []byte {
	s := inspectionSecret()
	s.Data["release"] = helmfixture.Stored(payload)
	s.Annotations = map[string]string{"user.example/private": "PRIVATE_ANNOTATION"}
	s.Labels["custom"] = "unchanged"
	return inspectionWire(s)
}

func TestHelmStatusTransitionPreservesPayloadAndConditions(t *testing.T) {
	// Lexical oracle deliberately includes unknown exact numbers, unusual
	// whitespace, escaped member names and mixed manifest newline encodings.
	body := []byte(` { "name":"example", "namespace":"team", "version":2,
"info": { "status" : "deployed", "description":"old", "deleted":null,
 "notes":"PRIVATE_NOTES", "future":{"n":9007199254740993123456789,"s":"\\r\\n"} },
"chart":{"metadata":{"name":"sample","version":"1.2.3"},"future": [1e+07, -0.0000]},
"manifest":"apiVersion: v1\r\nkind: ConfigMap\nmetadata:\n  name: sample\n",
"config":{"n":123456789123456789,"private":"PRIVATE_VALUES"}, "hooks":[],
"future" : { "exact": 1.00000000000000000001, "string":"PRIVATE_FUTURE" } } `)
	wire := statusFixture(body)
	before := bytes.Clone(wire)
	patch, err := BuildUninstallStatusPatch(context.Background(), wire, "team", inspectionSelection(), BeginUninstallStatus, statusTime)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(patch)
	if !bytes.Equal(before, wire) {
		t.Fatal("caller input changed")
	}
	var ops []statusPatchOperation
	if json.Unmarshal(patch, &ops) != nil || len(ops) != 8 {
		t.Fatal("unexpected patch shape")
	}
	for i, want := range []statusPatchOperation{
		{"test", "/metadata/name", inspectionSelection().StorageName}, {"test", "/metadata/namespace", "team"},
		{"test", "/metadata/uid", "uid2"}, {"test", "/metadata/resourceVersion", "7"}, {"test", "/metadata/labels/status", "deployed"},
	} {
		if ops[i] != want {
			t.Fatal("identity precondition changed", i)
		}
	}
	out := applyStatusPatch(t, wire, patch)
	expected := bytes.Replace(body, []byte(`"status" : "deployed"`), []byte(`"status" : "uninstalling"`), 1)
	expected = bytes.Replace(expected, []byte(`"description":"old"`), []byte(`"description":"Deletion in progress (or silently failed)"`), 1)
	expected = bytes.Replace(expected, []byte(`"deleted":null`), []byte(`"deleted":"2026-10-17T10:00:00.123456789Z"`), 1)
	if !bytes.Equal(statusPayload(t, out), expected) {
		t.Fatal("unrelated payload bytes were normalized or lost")
	}
	row := inspectionSelection()
	row.Status = "uninstalling"
	if _, err := InspectSecret(context.Background(), out, "team", row); err != nil {
		t.Fatal(err)
	}
	var original, updated corev1.Secret
	_ = json.Unmarshal(wire, &original)
	_ = json.Unmarshal(out, &updated)
	if updated.Labels["modifiedAt"] != "1792231200" {
		t.Fatal("wrong storage clock", updated.Labels["modifiedAt"])
	}
	updated.Data = original.Data
	updated.Labels = original.Labels
	if !reflect.DeepEqual(updated, original) {
		t.Fatal("unrelated Secret metadata changed")
	}
	// A real server supplies a new opaque RV after the first patch. The second
	// patch must be built from that newly read storage, never the old input.
	_ = json.Unmarshal(out, &updated)
	updated.ResourceVersion = "next-opaque"
	out = inspectionWire(&updated)
	row.ResourceVersion = updated.ResourceVersion
	finish, err := BuildUninstallStatusPatch(context.Background(), out, "team", row, FinishUninstallStatus, statusTime.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(finish)
	expected = bytes.Replace(expected, []byte(`"status" : "uninstalling"`), []byte(`"status" : "uninstalled"`), 1)
	expected = bytes.Replace(expected, []byte(`"description":"Deletion in progress (or silently failed)"`), []byte(`"description":"Uninstallation complete"`), 1)
	finished := applyStatusPatch(t, out, finish)
	if !bytes.Equal(statusPayload(t, finished), expected) {
		t.Fatal("finish changed unrelated payload or deletion timestamp")
	}
	row.Status = "uninstalled"
	if _, err := InspectSecret(context.Background(), finished, "team", row); err != nil {
		t.Fatal(err)
	}
	// Deterministic representation; returned storage belongs to the caller.
	again, err := BuildUninstallStatusPatch(context.Background(), wire, "team", inspectionSelection(), BeginUninstallStatus, statusTime)
	if err != nil || !bytes.Equal(again, patch) {
		t.Fatal("unstable encoding")
	}
	patch[0] = '!'
	if again[0] != '[' {
		t.Fatal("returned patch aliases another output")
	}
}

func TestHelmStatusTransitionRejectsStaleAndInvalid(t *testing.T) {
	base := helmfixture.Payload("team", "example", "deployed", 2)
	for name, mutate := range map[string]func(*corev1.Secret){
		"uid": func(s *corev1.Secret) { s.UID = "other" }, "rv": func(s *corev1.Secret) { s.ResourceVersion = "8" },
		"namespace": func(s *corev1.Secret) { s.Namespace = "other" }, "name": func(s *corev1.Secret) { s.Name = "other" },
		"status":    func(s *corev1.Secret) { s.Labels["status"] = "failed" },
		"immutable": func(s *corev1.Secret) { v := true; s.Immutable = &v },
		"finalizer": func(s *corev1.Secret) { s.Finalizers = []string{"example/hold"} },
		"owner":     func(s *corev1.Secret) { s.OwnerReferences = []metav1.OwnerReference{{Name: "owner"}} },
		"deleting":  func(s *corev1.Secret) { now := metav1.NewTime(statusTime); s.DeletionTimestamp = &now },
	} {
		t.Run(name, func(t *testing.T) {
			s := inspectionSecret()
			mutate(s)
			p, e := BuildUninstallStatusPatch(context.Background(), inspectionWire(s), "team", inspectionSelection(), BeginUninstallStatus, statusTime)
			if e == nil || p != nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
	for name, extra := range map[string]string{
		"duplicate": `,"future":{"x":1,"x":2}`, "status-alias": `,"Status":"deployed"`,
		"description-alias": `,"Description":"PRIVATE"`, "deleted-alias": `,"Deleted":null`,
		"description-null": `,"description":null`, "description-object": `,"description":{}`,
		"deleted-bool": `,"deleted":true`, "already-deleted": `,"deleted":"2026-10-17T09:00:00Z"`,
	} {
		t.Run(name, func(t *testing.T) {
			b := bytes.Replace(base, []byte(`"status":"deployed"`), []byte(`"status":"deployed"`+extra), 1)
			p, e := BuildUninstallStatusPatch(context.Background(), statusFixture(b), "team", inspectionSelection(), BeginUninstallStatus, statusTime)
			if e == nil || p != nil {
				t.Fatal("ambiguous lifecycle accepted")
			}
			if strings.Contains(e.Error(), "PRIVATE") {
				t.Fatal("private error")
			}
		})
	}
	for _, status := range []string{"failed", "pending-install", "uninstalled", "uninstalling"} {
		t.Run("begin-from-"+status, func(t *testing.T) {
			row := inspectionSelection()
			row.Status = status
			p, e := BuildUninstallStatusPatch(context.Background(), inspectionWire(helmfixture.Secret("team", "example", status, 2)), "team", row, BeginUninstallStatus, statusTime)
			if e == nil || p != nil {
				t.Fatal("invalid lifecycle")
			}
		})
	}
	for name, deleted := range map[string]string{"missing": "", "null": `,"deleted":null`, "zero": `,"deleted":"0001-01-01T00:00:00Z"`, "future": `,"deleted":"2027-01-01T00:00:00Z"`} {
		t.Run("finish-"+name, func(t *testing.T) {
			s := helmfixture.Secret("team", "example", "uninstalling", 2)
			b := helmfixture.Payload("team", "example", "uninstalling", 2)
			b = bytes.Replace(b, []byte(`"status":"uninstalling"`), []byte(`"status":"uninstalling"`+deleted), 1)
			s.Data["release"] = helmfixture.Stored(b)
			row := inspectionSelection()
			row.Status = "uninstalling"
			p, e := BuildUninstallStatusPatch(context.Background(), inspectionWire(s), "team", row, FinishUninstallStatus, statusTime)
			if e == nil || p != nil {
				t.Fatal("invalid finishing timestamp")
			}
		})
	}
	for _, now := range []time.Time{{}, time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if p, e := BuildUninstallStatusPatch(context.Background(), inspectionWire(inspectionSecret()), "team", inspectionSelection(), BeginUninstallStatus, now); e == nil || p != nil {
			t.Fatal("invalid clock")
		}
	}
	if p, e := BuildUninstallStatusPatch(context.Background(), inspectionWire(inspectionSecret()), "team", inspectionSelection(), StatusStep(255), statusTime); e == nil || p != nil {
		t.Fatal("arbitrary transition")
	}
}

func TestHelmStatusTransitionPatchPreconditions(t *testing.T) {
	wire := inspectionWire(inspectionSecret())
	raw, e := BuildUninstallStatusPatch(context.Background(), wire, "team", inspectionSelection(), BeginUninstallStatus, statusTime)
	if e != nil {
		t.Fatal(e)
	}
	p, e := jsonpatch.DecodePatch(raw)
	if e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"name", "namespace", "uid", "resourceVersion", "status"} {
		t.Run(key, func(t *testing.T) {
			var s map[string]any
			_ = json.Unmarshal(wire, &s)
			m := s["metadata"].(map[string]any)
			if key == "status" {
				m["labels"].(map[string]any)[key] = "failed"
			} else {
				m[key] = "other"
			}
			changed, _ := json.Marshal(s)
			if _, e := p.Apply(changed); e == nil {
				t.Fatal("precondition not enforced")
			}
		})
	}
	if _, e := p.Apply([]byte(`{}`)); e == nil {
		t.Fatal("patch created missing Secret")
	}
}

type statusCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	calls, at int
}

func (c *statusCancelContext) Err() error {
	c.calls++
	if c.at > 0 && c.calls == c.at {
		c.cancel()
	}
	return c.Context.Err()
}
func TestHelmStatusTransitionBudgetsAndCancellation(t *testing.T) {
	wire := inspectionWire(inspectionSecret())
	ctx, cancel := context.WithCancel(context.Background())
	probe := &statusCancelContext{Context: ctx, cancel: cancel}
	defer cancel()
	if _, e := BuildUninstallStatusPatch(probe, wire, "team", inspectionSelection(), BeginUninstallStatus, statusTime); e != nil {
		t.Fatal(e)
	}
	for _, at := range []int{1, 10, probe.calls / 2, probe.calls - 1, probe.calls} {
		ctx, cancel := context.WithCancel(context.Background())
		c := &statusCancelContext{Context: ctx, cancel: cancel, at: at}
		p, e := BuildUninstallStatusPatch(c, wire, "team", inspectionSelection(), BeginUninstallStatus, statusTime)
		cancel()
		if !errors.Is(e, context.Canceled) || p != nil {
			t.Fatal("late cancellation accepted", at)
		}
	}
	if p, e := BuildUninstallStatusPatch(context.Background(), make([]byte, MaxSecretBytes+1), "team", inspectionSelection(), BeginUninstallStatus, statusTime); !errors.Is(e, ErrInspectionBudget) || p != nil {
		t.Fatal("wire budget")
	}
	for name, payload := range map[string][]byte{"expanded": bytes.Repeat([]byte(" "), MaxReleaseBytes+1), "late-duplicate": []byte(`{"private":1,"private":2}`)} {
		t.Run(name, func(t *testing.T) {
			p, e := BuildUninstallStatusPatch(context.Background(), statusFixture(payload), "team", inspectionSelection(), BeginUninstallStatus, statusTime)
			if e == nil || p != nil {
				t.Fatal("malformed/budget accepted")
			}
		})
	}
	noise := make([]byte, MaxCompressedBytes+65536)
	_, _ = rand.New(rand.NewSource(1)).Read(noise)
	if p, e := encodeStatusPayload(context.Background(), noise); !errors.Is(e, ErrInspectionBudget) || p != nil {
		t.Fatal("compressed output budget", e)
	}
	near := append([]byte(`{"info":0,"pad":"`), bytes.Repeat([]byte("x"), MaxReleaseBytes-22)...)
	near = append(near, []byte(`"}`)...)
	if p, e := replaceStatusMember(context.Background(), near, "info", bytes.Repeat([]byte(" "), 100)); !errors.Is(e, ErrInspectionBudget) || p != nil {
		t.Fatal("rewrite growth budget", e)
	}
}

func TestHelmStatusTransitionSpliceOracle(t *testing.T) {
	for _, object := range []string{`{}`, ` { "keep" : [9007199254740993,1e+02,-0.00] } `, `{"status":"old","keep":{"x":true}}`, `{"\u0073tatus":"old"}`, `{"keep":"} , : \\\"","status":"old"}`} {
		t.Run(object, func(t *testing.T) {
			out, e := replaceStatusMember(context.Background(), []byte(object), "status", []byte(`"new"`))
			if e != nil {
				t.Fatal(e)
			}
			var before, after map[string]json.RawMessage
			_ = json.Unmarshal([]byte(object), &before)
			if json.Unmarshal(out, &after) != nil || string(after["status"]) != `"new"` {
				t.Fatal("bad splice")
			}
			delete(before, "status")
			delete(after, "status")
			if !reflect.DeepEqual(before, after) {
				t.Fatal("unrelated raw tokens changed")
			}
		})
	}
	for _, object := range []string{`[]`, `null`, `{"status":0,"status":1}`, `{} {}`, `{"status":}`} {
		if p, e := replaceStatusMember(context.Background(), []byte(object), "status", []byte(`"new"`)); e == nil || p != nil {
			t.Fatal("invalid splice object", object)
		}
	}
}
