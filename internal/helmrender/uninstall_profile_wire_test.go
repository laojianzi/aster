package helmrender

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	"github.com/laojianzi/aster/internal/helmrelease"
)

// Exercise the actual pinned encoder, not a hand-written two-field chart File.
// Memory storage/client-only dry-run and a reconstructed empty kubeconfig mean
// no cluster credentials or install effects. The fixture status change below is
// not evidence of an actual deployment; the dedicated kind suite supplies that.
func TestHelmUninstallProfilePinnedChartWire(t *testing.T) {
	exe := approvedContractExecutable(t)
	root := t.TempDir()
	env, err := isolatedEnvironment(root)
	if err != nil {
		t.Fatal(err)
	}
	tool := &contractTool{root: root, exe: exe, env: env}
	chart := filepath.Join(root, "chart")
	if err := os.MkdirAll(filepath.Join(chart, "templates"), 0700); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for name, text := range map[string]string{
		"Chart.yaml":            "apiVersion: v2\nname: contract\nversion: 0.1.0\n",
		"README.md":             "Harmless local fixture.\n",
		"templates/target.yaml": contractManifest("target", false),
	} {
		name = filepath.Join(chart, name)
		if err := os.WriteFile(name, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	output, err := tool.run(t, "install", contractRelease, chart, "--namespace", contractNamespace,
		"--dry-run=client", "--output=json", "--disable-openapi-validation")
	if err != nil {
		t.Fatal("isolated pinned Helm serialization failed", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &doc); err != nil {
		t.Fatal("pinned Helm release was not JSON")
	}
	var chartWire map[string]json.RawMessage
	if err := json.Unmarshal(doc["chart"], &chartWire); err != nil {
		t.Fatal("pinned chart absent")
	}
	for _, key := range []string{"templates", "files"} {
		var files []map[string]json.RawMessage
		if json.Unmarshal(chartWire[key], &files) != nil || len(files) != 1 {
			t.Fatal("expected exactly one pinned chart file", key)
		}
		var got time.Time
		if json.Unmarshal(files[0]["modtime"], &got) != nil || !got.Equal(stamp) {
			t.Fatal("pinned encoder did not include the exact file timestamp", key)
		}
	}
	var info map[string]json.RawMessage
	if err := json.Unmarshal(doc["info"], &info); err != nil {
		t.Fatal(err)
	}
	info["status"] = json.RawMessage(`"deployed"`)
	doc["info"], err = json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(payload)
	secret := contractSecret("")
	secret.Data["release"] = helmfixture.Stored(payload)
	wire, err := json.Marshal(secret)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(wire)
	row := helmrelease.Revision{Release: contractRelease, Number: 1, Status: "deployed", StorageName: secret.Name, UID: string(secret.UID), ResourceVersion: secret.ResourceVersion}
	profile, err := helmrelease.InspectUninstallProfile(context.Background(), wire, contractNamespace, row, "wire-session", time.Now())
	if err != nil || profile == nil || len(profile.Preview().Items()) != 1 || profile.Preview().Items()[0].Name != "target" {
		t.Fatal("actual pinned chart wire rejected", err)
	}
}
