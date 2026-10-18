package helmrelease

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// Helm v4.3.0 common.File includes modtime for directory-loaded templates and
// ordinary files. Timestamps are inert chart data, never freshness or authority.
func TestHelmUninstallProfileChartTimes(t *testing.T) {
	for _, scope := range []string{"chart", "schema", "templates", "files"} {
		for _, value := range []string{"omitted", "0001-01-01T00:00:00Z", "2026-10-17T12:34:56.123456789Z", "2026-10-17T20:34:56+08:00"} {
			t.Run(scope+"/"+value, func(t *testing.T) {
				d := profilePayload(profileManifest)
				chart := d["chart"].(map[string]any)
				target, key := chart, "modtime"
				if scope == "schema" {
					key = "schemamodtime"
				}
				if scope == "templates" || scope == "files" {
					target = chart[scope].([]any)[0].(map[string]any)
				}
				if value != "omitted" {
					target[key] = value
				}
				wire := profileWire(t, d, nil)
				before := bytes.Clone(wire)
				started := time.Now()
				p, err := InspectUninstallProfile(context.Background(), wire, "team", inspectionSelection(), "session", started)
				if err != nil || p == nil || len(p.Preview().Items()) != 1 {
					t.Fatal("valid pinned Helm chart timestamp rejected", err)
				}
				if !bytes.Equal(wire, before) || !p.Preview().ExpiresAt().Equal(started.Add(PreparationLifetime)) {
					t.Fatal("timestamp changed payload or preparation authority")
				}
			})
		}
	}
}

func TestHelmUninstallProfileRejectsChartTimeAmbiguity(t *testing.T) {
	for _, scope := range []string{"chart", "schema", "templates", "files"} {
		for _, mode := range []string{"null", "number", "object", "empty", "malformed", "over-budget", "alias", "unknown-field", "restricted-path"} {
			t.Run(scope+"/"+mode, func(t *testing.T) {
				d := profilePayload(profileManifest)
				chart := d["chart"].(map[string]any)
				target, key := chart, "modtime"
				if scope == "schema" {
					key = "schemamodtime"
				}
				if scope == "templates" || scope == "files" {
					target = chart[scope].([]any)[0].(map[string]any)
				}
				target[key] = "2026-10-17T12:34:56Z"
				switch mode {
				case "null":
					target[key] = nil
				case "number":
					target[key] = 123
				case "object":
					target[key] = map[string]any{}
				case "empty":
					target[key] = ""
				case "malformed":
					target[key] = "PRIVATE_INVALID_TIME"
				case "over-budget":
					target[key] = strings.Repeat("x", 129)
				case "alias":
					target[strings.ToUpper(key)] = target[key]
				case "unknown-field":
					target["future_execution"] = true
				case "restricted-path":
					chart["files"].([]any)[0].(map[string]any)["name"] = "crds/hidden.yaml"
				}
				p, err := InspectUninstallProfile(context.Background(), profileWire(t, d, nil), "team", inspectionSelection(), "session", time.Now())
				if err == nil || p != nil {
					t.Fatal("ambiguous timestamp or forbidden structure admitted")
				}
				if strings.Contains(err.Error(), "PRIVATE") {
					t.Fatal("timestamp leaked in diagnostic")
				}
			})
		}
	}
}
