package helmrelease

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/laojianzi/aster/internal/helmfixture"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func inspectionSelection() Revision {
	return Revision{Release: "example", Number: 2, Status: "deployed", StorageName: "sh.helm.release.v1.example.v2", UID: "uid2", ResourceVersion: "7"}
}
func inspectionWire(s *corev1.Secret) []byte {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return b
}
func inspectionSecret() *corev1.Secret { return helmfixture.Secret("team", "example", "deployed", 2) }

func TestHelmInspectionBoundIdentityAndPrivateProjection(t *testing.T) {
	data := inspectionWire(inspectionSecret())
	original := bytes.Clone(data)
	s, err := InspectSecret(context.Background(), data, "team", inspectionSelection())
	if err != nil || s.ChartName != "example-chart" || s.ChartVersion != "1.2.3" || s.ManifestBytes == 0 || s.HookCount != 1 || s.SuppliedValues != 2 {
		t.Fatalf("summary: %+v err=%v", s, err)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "PRIVATE") || !bytes.Equal(data, original) {
		t.Fatal("payload escaped or input was changed")
	}
	for _, status := range []string{"unknown", "deployed", "superseded", "failed", "pending-install", "pending-upgrade", "pending-rollback", "uninstalling", "uninstalled"} {
		row := inspectionSelection()
		row.Status = status
		if _, err := InspectSecret(context.Background(), inspectionWire(helmfixture.Secret("team", "example", status, 2)), "team", row); err != nil {
			t.Fatal(status, err)
		}
	}
}
func TestHelmInspectionMalformedAndAmbiguousPayloads(t *testing.T) {
	original := helmfixture.Payload("team", "example", "deployed", 2)
	for name, body := range map[string][]byte{
		"wrong-name":             bytes.Replace(original, []byte(`"name":"example"`), []byte(`"name":"other"`), 1),
		"wrong-namespace":        bytes.Replace(original, []byte(`"namespace":"team"`), []byte(`"namespace":"other"`), 1),
		"wrong-version":          bytes.Replace(original, []byte(`"version":2`), []byte(`"version":1`), 1),
		"float-version":          bytes.Replace(original, []byte(`"version":2`), []byte(`"version":2.0`), 1),
		"wrong-status":           bytes.Replace(original, []byte(`"status":"deployed"`), []byte(`"status":"failed"`), 1),
		"duplicate-identity":     bytes.Replace(original, []byte(`"version":2`), []byte(`"version":2,"version":2`), 1),
		"late-unknown-duplicate": append(bytes.Clone(original[:len(original)-1]), []byte(`,"future":{"private":1,"private":2}}`)...),
		"case-alias":             append(bytes.Clone(original[:len(original)-1]), []byte(`,"Name":"different"}`)...),
		"null-info":              bytes.Replace(original, []byte(`"info":{`), []byte(`"info":null,"ignored":{`), 1),
		"bad-chart-display":      bytes.Replace(original, []byte("example-chart"), []byte(`PRIVATE\nESCAPE`), 1),
		"wrong-config-type":      bytes.Replace(original, []byte(`"config":{`), []byte(`"config":[],"ignored":{`), 1),
		"null-hook":              bytes.Replace(original, []byte(`"hooks":[{`), []byte(`"hooks":[null,{`), 1),
		"trailing-json":          append(bytes.Clone(original), []byte(`{}`)...),
		"invalid-utf8":           append(bytes.Clone(original), 0xff),
	} {
		t.Run(name, func(t *testing.T) {
			s := inspectionSecret()
			s.Data["release"] = helmfixture.Stored(body)
			out, err := InspectSecret(context.Background(), inspectionWire(s), "team", inspectionSelection())
			if err == nil || out != (Inspection{}) {
				t.Fatal("invalid payload admitted")
			}
			if strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("private error")
			}
		})
	}
	for name, modify := range map[string]func(*corev1.Secret){
		"uid": func(s *corev1.Secret) { s.UID = "new" }, "resource-version": func(s *corev1.Secret) { s.ResourceVersion = "8" },
		"namespace": func(s *corev1.Secret) { s.Namespace = "other" }, "name": func(s *corev1.Secret) { s.Name = "other" },
		"owner": func(s *corev1.Secret) { s.Labels["owner"] = "other" }, "status": func(s *corev1.Secret) { s.Labels["status"] = "failed" },
		"deleting": func(s *corev1.Secret) { now := metav1.Now(); s.DeletionTimestamp = &now },
	} {
		t.Run(name, func(t *testing.T) {
			s := inspectionSecret()
			modify(s)
			out, err := InspectSecret(context.Background(), inspectionWire(s), "team", inspectionSelection())
			if !errors.Is(err, ErrInspectionStale) || out != (Inspection{}) {
				t.Fatal(err)
			}
		})
	}
	for _, modify := range []func(*corev1.Secret){func(s *corev1.Secret) { s.Type = "Opaque" }, func(s *corev1.Secret) { s.Kind = "PartialObjectMetadata" }} {
		s := inspectionSecret()
		modify(s)
		if _, err := InspectSecret(context.Background(), inspectionWire(s), "team", inspectionSelection()); !errors.Is(err, ErrInspectionUnsupported) {
			t.Fatal(err)
		}
	}
	wire := inspectionWire(inspectionSecret())
	for _, body := range [][]byte{bytes.Replace(wire, []byte(`"kind":"Secret"`), []byte(`"kind":"Secret","kind":"Secret"`), 1), append(bytes.Clone(wire), []byte(`{}`)...)} {
		if _, err := InspectSecret(context.Background(), body, "team", inspectionSelection()); err == nil {
			t.Fatal("ambiguous Secret")
		}
	}
}
func TestHelmInspectionEncodingBudgetsAndCancellation(t *testing.T) {
	compressed, _ := base64.StdEncoding.DecodeString(string(inspectionSecret().Data["release"]))
	corrupt := bytes.Clone(compressed)
	corrupt[len(corrupt)-5] ^= 1
	for name, encoded := range map[string][]byte{
		"legacy":           []byte(base64.StdEncoding.EncodeToString([]byte(`{"name":"PRIVATE"}`))),
		"bad-inner-base64": []byte("PRIVATE!"), "newline-base64": append(bytes.Clone(inspectionSecret().Data["release"]), '\n'),
		"truncated":        []byte(base64.StdEncoding.EncodeToString(compressed[:len(compressed)-3])),
		"corrupt-crc":      []byte(base64.StdEncoding.EncodeToString(corrupt)),
		"trailing":         []byte(base64.StdEncoding.EncodeToString(append(bytes.Clone(compressed), 0))),
		"multiple-members": []byte(base64.StdEncoding.EncodeToString(append(bytes.Clone(compressed), compressed...))),
	} {
		t.Run(name, func(t *testing.T) {
			s := inspectionSecret()
			s.Data["release"] = encoded
			if out, err := InspectSecret(context.Background(), inspectionWire(s), "team", inspectionSelection()); err == nil || out != (Inspection{}) {
				t.Fatal("invalid encoding")
			}
		})
	}
	for name, payload := range map[string][]byte{
		"decompression": []byte(strings.Repeat(" ", MaxReleaseBytes+1)),
		"depth":         []byte(strings.Repeat("[", MaxJSONDepth+2) + "0" + strings.Repeat("]", MaxJSONDepth+2)),
		"tokens":        []byte("[" + strings.Repeat("0,", MaxJSONTokens) + "0]"),
	} {
		t.Run(name, func(t *testing.T) {
			s := inspectionSecret()
			s.Data["release"] = helmfixture.Stored(payload)
			out, err := InspectSecret(context.Background(), inspectionWire(s), "team", inspectionSelection())
			if !errors.Is(err, ErrInspectionBudget) || out != (Inspection{}) {
				t.Fatal(err)
			}
		})
	}
	if _, err := InspectSecret(context.Background(), make([]byte, MaxSecretBytes+1), "team", inspectionSelection()); !errors.Is(err, ErrInspectionBudget) {
		t.Fatal(err)
	}
	if _, err := decodeBase64(strings.Repeat("A", base64.StdEncoding.EncodedLen(MaxStoredBytes)+4), MaxStoredBytes); !errors.Is(err, ErrInspectionBudget) {
		t.Fatal(err)
	}
	for _, text := range []string{"Zh==", "Zg==\n", "Zg=", "!!!!"} {
		if _, err := decodeBase64(text, 32); err == nil {
			t.Fatal("noncanonical base64")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := InspectSecret(ctx, inspectionWire(inspectionSecret()), "team", inspectionSelection())
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, Inspection{}) {
		t.Fatal(err)
	}
	for _, row := range []Revision{{}, {Release: "../example", Number: 2, Status: "deployed", StorageName: "../example", UID: "id", ResourceVersion: "1"}} {
		if _, err := InspectSecret(context.Background(), nil, "team", row); !errors.Is(err, ErrInspectionSelection) {
			t.Fatal(err)
		}
	}
}

// Context that deterministically cancels during work, without a timing race.
type inspectionCancelContext struct {
	context.Context
	left int
}

func (c *inspectionCancelContext) Err() error {
	c.left--
	if c.left <= 0 {
		return context.Canceled
	}
	return nil
}
func TestHelmInspectionLateBudgetsAndInFlightCancellation(t *testing.T) {
	base := helmfixture.Payload("team", "example", "deployed", 2)
	for _, field := range []string{"manifest", "hooks", "config"} {
		t.Run(field, func(t *testing.T) {
			for _, extra := range []int{0, 1} {
				var doc map[string]any
				if err := json.Unmarshal(base, &doc); err != nil {
					t.Fatal(err)
				}
				switch field {
				case "manifest":
					doc[field] = strings.Repeat("x", MaxManifestBytes+extra)
				case "hooks":
					items := make([]any, MaxHooks+extra)
					for i := range items {
						items[i] = map[string]any{"name": "withheld"}
					}
					doc[field] = items
				case "config":
					items := map[string]any{}
					for i := 0; i < MaxValuesKeys+extra; i++ {
						items[strconv.Itoa(i)] = true
					}
					doc[field] = items
				}
				payload, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				s := inspectionSecret()
				s.Data["release"] = helmfixture.Stored(payload)
				out, err := InspectSecret(context.Background(), inspectionWire(s), "team", inspectionSelection())
				if extra == 0 && err != nil {
					t.Fatal("valid exact boundary", err)
				}
				if extra == 1 && (!errors.Is(err, ErrInspectionBudget) || out != (Inspection{})) {
					t.Fatal("late budget", err)
				}
			}
		})
	}
	if err := checkJSON(&inspectionCancelContext{Context: context.Background(), left: 5}, base); !errors.Is(err, context.Canceled) {
		t.Fatal("JSON walk did not cancel", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := contextBytes{ctx: ctx, reader: bytes.NewReader([]byte("x"))}
	if _, err := source.ReadByte(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := source.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func FuzzHelmInspectionBoundedProjection(f *testing.F) {
	f.Add(inspectionWire(inspectionSecret()))
	f.Add([]byte(`{"data":{"release":"PRIVATE"}}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxSecretBytes+1 {
			return
		}
		original := bytes.Clone(data)
		out, err := InspectSecret(context.Background(), data, "team", inspectionSelection())
		if !bytes.Equal(data, original) {
			t.Fatal("input mutation")
		}
		if err != nil && out != (Inspection{}) {
			t.Fatal("partial projection on failure")
		}
	})
}
