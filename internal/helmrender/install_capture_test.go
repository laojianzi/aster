package helmrender

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmrelease"
)

func installCapturedPayload(t *testing.T, c *helmrelease.InstallCandidate) map[string]json.RawMessage {
	t.Helper()
	wire, err := helmrelease.BuildInstallSecret(context.Background(), c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer clear(wire)
	var s struct {
		Data map[string][]byte `json:"data"`
	}
	if json.Unmarshal(wire, &s) != nil {
		t.Fatal("bad secret")
	}
	compressed, err := base64.StdEncoding.Strict().DecodeString(string(s.Data["release"]))
	if err != nil {
		t.Fatal("bad storage")
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal("bad gzip")
	}
	defer gz.Close()
	payload, err := io.ReadAll(io.LimitReader(gz, helmrelease.MaxReleaseBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(payload)
	var out map[string]json.RawMessage
	if json.Unmarshal(payload, &out) != nil {
		t.Fatal("bad release")
	}
	return out
}

func TestHelmInstallCaptureInputConsentAndAdmission(t *testing.T) {
	for _, mode := range []string{"untrusted", "wrong-release", "wrong-namespace", "capability-override", "nil-context", "cancelled", "busy"} {
		t.Run(mode, func(t *testing.T) {
			r := InstallRequest{Release: "example", Namespace: "team", CapabilityAssumption: helmrelease.InstallCapabilityAssumption, Trusted: true}
			ctx := context.Background()
			switch mode {
			case "untrusted":
				r.Trusted = false
			case "wrong-release":
				r.Release = "../escape"
			case "wrong-namespace":
				r.Namespace = "*"
			case "capability-override":
				r.CapabilityAssumption = "v1.35.8"
			case "nil-context":
				ctx = nil
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "busy":
				admission <- struct{}{}
				defer func() { <-admission }()
			}
			c, err := CaptureInstall(ctx, r)
			if err == nil || c != nil {
				t.Fatal("unsafe capture accepted")
			}
			if mode == "cancelled" {
				if !errors.Is(err, context.Canceled) || !errors.Is(installCaptureFailure(ctx, ErrDistribution), context.Canceled) {
					t.Fatal("cancellation mislabeled", err)
				}
				if !errors.Is(installCaptureFailure(context.Background(), ErrDistribution), ErrDistribution) {
					t.Fatal("distribution failure lost")
				}
			}
		})
	}
}

// The exact archive is supplied by existing CI. Skips are not qualification;
// the added evidence contract requires this name and every child in CI.
func TestHelmInstallPinnedCaptureSingleSnapshot(t *testing.T) {
	archive := os.Getenv("ASTER_HELM_ARCHIVE")
	if archive == "" {
		t.Skip("approved Helm archive required")
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer srv.Close()
	kubeconfig := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: current\n  cluster:\n    server: %s\ncontexts:\n- name: current\n  context:\n    cluster: current\n    user: current\ncurrent-context: current\nusers:\n- name: current\n  user:\n    token: PRIVATE_CURRENT_TOKEN\n", srv.URL)
	t.Setenv("KUBECONFIG", writeTestFile(t, "private-kubeconfig", []byte(kubeconfig)))
	t.Setenv("HELM_KUBETOKEN", "PRIVATE_CURRENT_TOKEN")
	t.Setenv("HELM_MEMORY_DRIVER_DATA", "PRIVATE_DATABASE")
	entries := minimalEntries()
	entries[2].text += "  capability: {{ .Capabilities.KubeVersion.Version | quote }}\n  nonce: {{ randAlphaNum 16 | quote }}\n  missing: {{ lookup \"v1\" \"Secret\" \"team\" \"hidden\" | empty | quote }}\n"
	chart := writeTestFile(t, "chart.tgz", chartArchive(t, entries...))
	values := writeTestFile(t, "values.yaml", []byte("message: PRIVATE_CAPTURE_VALUE\n"))
	request := InstallRequest{HelmArchive: archive, ChartArchive: chart, ValuesFile: values, Release: "example", Namespace: "team",
		CapabilityAssumption: helmrelease.InstallCapabilityAssumption, Trusted: true}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := CaptureInstall(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	first := installCapturedPayload(t, c)
	var manifest string
	_ = json.Unmarshal(first["manifest"], &manifest)
	if !strings.Contains(manifest, `capability: "v1.37.0"`) || !strings.Contains(manifest, `missing: "true"`) ||
		!strings.Contains(manifest, "PRIVATE_CAPTURE_VALUE") {
		t.Fatal("wrong pinned release snapshot")
	}
	if len(first["chart"]) == 0 || len(first["config"]) == 0 {
		t.Fatal("ordinary manifest is not a full release")
	}
	_ = os.Remove(chart)
	_ = os.Remove(values)
	second := installCapturedPayload(t, c)
	for _, key := range []string{"manifest", "chart", "config"} {
		if !bytes.Equal(first[key], second[key]) {
			t.Fatal("rerender or source reread")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("capture used inherited cluster identity")
	}
	if strings.Contains(fmt.Sprintf("%#v", c), "PRIVATE") {
		t.Fatal("candidate leaked")
	}
	for _, mode := range []string{"hook", "secret", "render-error", "output-overflow"} {
		t.Run(mode, func(t *testing.T) {
			e := minimalEntries()
			switch mode {
			case "hook":
				e[2].text = strings.Replace(e[2].text, "metadata:", "metadata:\n  annotations:\n    helm.sh/hook: pre-install", 1)
			case "secret":
				e[2].text = strings.Replace(e[2].text, "kind: ConfigMap", "kind: Secret", 1)
			case "render-error":
				e[2].text = `{{ fail "PRIVATE_ERROR" }}`
			case "output-overflow":
				e[2].text = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\ndata:\n  large: {{ repeat 2100000 \"x\" | quote }}\n"
			}
			r := request
			r.ValuesFile = ""
			r.ChartArchive = writeTestFile(t, "chart.tgz", chartArchive(t, e...))
			// Each invocation retains the existing bounded child deadline.
			child, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			out, err := CaptureInstall(child, r)
			want := map[string]error{
				"hook":            helmrelease.ErrInstallCapture,
				"secret":          helmrelease.ErrPreparationProfile,
				"render-error":    ErrRender,
				"output-overflow": ErrOutput,
			}[mode]
			if !errors.Is(err, want) || out != nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("specific rejection not exercised", err)
			}
		})
	}
}
