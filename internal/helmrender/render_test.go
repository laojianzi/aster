package helmrender

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type entry struct {
	name, text string
	kind       byte
}

func chartArchive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.kind
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Mode: 0600, Size: int64(len(e.text)), Typeflag: typ}
		if typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.text)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func minimalEntries() []entry {
	return []entry{{"sample/Chart.yaml", "apiVersion: v2\nname: sample\nversion: 0.1.0\n", 0}, {"sample/values.yaml", "message: original\n", 0}, {"sample/templates/config.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\n  namespace: {{ .Release.Namespace }}\ndata:\n  message: {{ .Values.message | quote }}\n", 0}}
}
func writeTestFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHelmChartSnapshotBoundaries(t *testing.T) {
	valid := chartArchive(t, minimalEntries()...)
	before := bytes.Clone(valid)
	files, err := chartFiles(context.Background(), valid)
	if err != nil || len(files) != 3 || !bytes.Equal(before, valid) {
		t.Fatal("valid chart snapshot failed", err)
	}
	cases := map[string][]entry{}
	for _, name := range []string{"sample/../escape", "/sample/abs", `sample\windows`, "sample/C:stream", "sample/files/CON", "sample/templates/a.", "other/templates/x.yaml", "sample/.helmignore", "sample/values.schema.json", "sample/charts/sub.tgz", "sample/crds/custom.yaml", "sample/Chart.lock"} {
		cases[name] = append(minimalEntries(), entry{name, "PRIVATE_VALUE", 0})
	}
	for name, typ := range map[string]byte{"symlink": tar.TypeSymlink, "hardlink": tar.TypeLink, "fifo": tar.TypeFifo, "device": tar.TypeChar} {
		cases[name] = append(minimalEntries(), entry{"sample/files/" + name, "", typ})
	}
	cases["duplicate"] = append(minimalEntries(), minimalEntries()[0])
	cases["case-collision"] = append(minimalEntries(), entry{"sample/VALUES.yaml", "x: y", 0})
	cases["oversized-file"] = append(minimalEntries(), entry{"sample/files/large", strings.Repeat("x", MaxFileBytes+1), 0})
	tooMany := minimalEntries()
	for i := 0; i < MaxEntries; i++ {
		tooMany = append(tooMany, entry{"sample/files/" + strings.Repeat("x", i+1), "", 0})
	}
	cases["entry-budget"] = tooMany
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := chartFiles(context.Background(), chartArchive(t, entries...))
			if !errors.Is(err, ErrInput) || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("unsafe chart accepted or disclosed", err)
			}
		})
	}
	for name, mutate := range map[string]func([]byte) []byte{"crc": func(b []byte) []byte { b[len(b)-8] ^= 1; return b }, "truncated": func(b []byte) []byte { return b[:len(b)-4] }, "concatenated": func(b []byte) []byte { return append(b, valid...) }} {
		t.Run(name, func(t *testing.T) {
			if _, err := chartFiles(context.Background(), mutate(bytes.Clone(valid))); err == nil {
				t.Fatal("malformed compressed stream accepted")
			}
		})
	}
	for _, meta := range []string{"apiVersion: v1\nname: sample\nversion: 0.1.0\n", "apiVersion: v2\nname: sample\nversion: 0.1.0\ntype: library\n", "apiVersion: v2\nname: sample\nversion: 0.1.0\ndependencies: [{name: PRIVATE, repository: https://private.invalid}]\n", "apiVersion: v2\nname: sample\nname: sample\nversion: 0.1.0\n"} {
		entries := minimalEntries()
		entries[0].text = meta
		if _, err := chartFiles(context.Background(), chartArchive(t, entries...)); err == nil {
			t.Fatal("unsupported metadata accepted")
		}
	}
	for _, values := range []string{"a: &x [1]\nb: *x\n", "x: 1\nx: 2\n", "- not-a-map\n", "null\n", "x: 1\n---\nx: 2\n", "a: {<<: {x: 1}}\n"} {
		if mapping(context.Background(), []byte(values), nil) {
			t.Fatal("ambiguous values accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := chartFiles(ctx, valid); err == nil {
		t.Fatal("cancelled chart accepted")
	}
}

func TestHelmDistributionAndLocalFileBoundaries(t *testing.T) {
	p := writeTestFile(t, "input", []byte("hello"))
	if got, err := readLocal(context.Background(), p, 5); err != nil || string(got) != "hello" {
		t.Fatal(err)
	}
	for _, name := range []string{p, "relative", "https://example.invalid/chart.tgz", filepath.Dir(p)} {
		limit := int64(4)
		if name != p {
			limit = 100
		}
		if _, err := readLocal(context.Background(), name, limit); err == nil {
			t.Fatal("non-local or oversized input accepted")
		}
	}
	d, version, ok := approved()
	if !ok || version != "v4.3.0" || d.File != ApprovedArchive() {
		t.Fatal("missing current-platform pin")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Render(ctx, Request{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Render(nil, Request{}); err != ErrInput {
		t.Fatal(err)
	}
	for _, r := range []Request{{}, {Trusted: true, Release: "-x", Namespace: "team", KubeVersion: "1.37.0"}, {Trusted: true, Release: "x", Namespace: "*", KubeVersion: "1.37.0"}, {Trusted: true, Release: "x", Namespace: "team", KubeVersion: "--dry-run=server"}} {
		if _, err := Render(context.Background(), r); err != ErrInput {
			t.Fatal("invalid request accepted", err)
		}
	}
	r := Request{Trusted: true, Release: "x", Namespace: "team", KubeVersion: "1.37.0", ChartArchive: writeTestFile(t, "chart.tgz", chartArchive(t, minimalEntries()...)), HelmArchive: p}
	if _, err := Render(context.Background(), r); !errors.Is(err, ErrDistribution) {
		t.Fatal("unverified executable accepted", err)
	}
	admission <- struct{}{}
	_, err := Render(context.Background(), r)
	<-admission
	if !errors.Is(err, ErrBusy) {
		t.Fatal("global admission missing")
	}
	// Extraction itself writes one fixed destination only. Production calls it
	// only after the entire archive digest matches the embedded distribution pin.
	archive := chartArchive(t, entry{"linux-amd64/helm", "fixture-not-executed", 0})
	path, err := unpackExecutable(context.Background(), archive, "helm-v4.3.0-linux-amd64.tar.gz", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "fixture-not-executed" {
		t.Fatal("wrong extraction")
	}
	var zipBytes bytes.Buffer
	zw := zip.NewWriter(&zipBytes)
	w, _ := zw.Create("windows-amd64/helm.exe")
	w.Write([]byte("fixture-not-executed"))
	zw.Close()
	if _, err := unpackExecutable(context.Background(), zipBytes.Bytes(), "helm-v4.3.0-windows-amd64.zip", t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestHelmEnvironmentAndOutputBudgets(t *testing.T) {
	t.Setenv("AWS_SECRET_ACCESS_KEY", "PRIVATE_CREDENTIAL")
	t.Setenv("HELM_KUBETOKEN", "PRIVATE_TOKEN")
	t.Setenv("HELM_MEMORY_DRIVER_DATA", "PRIVATE_RELEASE_DATABASE")
	t.Setenv("HTTP_PROXY", "http://private.invalid")
	t.Setenv("LD_PRELOAD", "PRIVATE_LOADER")
	root := t.TempDir()
	env, err := isolatedEnvironment(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range env {
		if strings.Contains(e, "PRIVATE") || strings.HasPrefix(e, "HTTP_PROXY=") {
			t.Fatal("inherited credentials/environment")
		}
	}
	joined := strings.Join(env, "\n")
	for _, key := range []string{"HOME=", "KUBECONFIG=", "HELM_PLUGINS=", "HELM_DRIVER=memory", "HELM_MEMORY_DRIVER_DATA=" + filepath.Join(root, "memory-releases.yaml"), "GOMEMLIMIT=256MiB"} {
		if !strings.Contains(joined, key) {
			t.Fatal("missing isolation", key)
		}
	}
	memoryData, err := os.ReadFile(filepath.Join(root, "memory-releases.yaml"))
	if err != nil || string(memoryData) != "[]\n" {
		t.Fatal("memory driver must start from a private empty release list", err)
	}
	for _, data := range [][]byte{nil, []byte(" "), {0xff}, {'a', 0}, []byte(strings.Repeat("a", MaxOutputLine+1)), []byte(strings.Repeat("\n", MaxOutputNewlines+1) + "x"), []byte(strings.Repeat("x\n", MaxOutputBytes/2+1))} {
		if outputWithinBudget(data) {
			t.Fatal("unsafe output accepted")
		}
	}
	if !outputWithinBudget([]byte("x\n")) || !outputWithinBudget([]byte(strings.Repeat("x", MaxOutputLine))) {
		t.Fatal("valid exact output bound rejected")
	}
}

// CI supplies the hash-pinned archive. Skipping locally is explicit; the
// mandatory evidence gate rejects missing/skipped integration tests in CI.
func TestHelmRealTemplateSnapshotAndFailure(t *testing.T) {
	archive := os.Getenv("ASTER_HELM_ARCHIVE")
	if archive == "" {
		t.Skip("requires approved Helm archive; CI makes this test mandatory")
	}
	// The renderer must replace hostile inherited driver/kube paths, not repair
	// the startup failure by using the caller's release database or credentials.
	t.Setenv("HELM_MEMORY_DRIVER_DATA", writeTestFile(t, "private-releases.yaml", []byte("PRIVATE_RELEASE_DATABASE: invalid\n")))
	t.Setenv("KUBECONFIG", writeTestFile(t, "private-kubeconfig", []byte("PRIVATE_CLUSTER_CREDENTIAL: invalid\n")))
	entries := minimalEntries()
	entries[2].text += "  missing: {{ lookup \"v1\" \"Secret\" \"team\" \"PRIVATE_NAME\" | empty | quote }}\n"
	chart := writeTestFile(t, "chart.tgz", chartArchive(t, entries...))
	values := writeTestFile(t, "values.yaml", []byte("message: '你好 🚢'\n"))
	request := Request{archive, chart, values, "rendered", "team", "1.37.0", true}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first, err := Render(ctx, request)
	if err != nil {
		t.Fatal("real Helm render failed", err)
	}
	if !strings.Contains(first.Manifest(), "你好 🚢") || !strings.Contains(first.Manifest(), "name: rendered") || !strings.Contains(first.Manifest(), `missing: "true"`) {
		t.Fatal("did not run Helm values/release/offline lookup")
	}
	captured := first.Manifest()
	os.WriteFile(values, []byte("message: changed\n"), 0600)
	os.WriteFile(chart, []byte("changed"), 0600)
	if first.Manifest() != captured || first.Namespace() != "team" {
		t.Fatal("captured output changed with source files")
	}
	for name, template := range map[string]string{"error": `{{ fail "PRIVATE_RENDERER_MESSAGE" }}`, "display-overflow": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: overflow\ndata:\n  large: {{ repeat 8193 \"x\" | quote }}\n", "stdout-overflow": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: overflow\ndata:\n  large: {{ repeat 2100000 \"x\" | quote }}\n"} {
		t.Run(name, func(t *testing.T) {
			entries := minimalEntries()
			entries[2].text = template
			request.ChartArchive = writeTestFile(t, "chart.tgz", chartArchive(t, entries...))
			r, err := Render(ctx, request)
			if err == nil || r != nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("partial/error render accepted or disclosed")
			}
			if name != "error" && !errors.Is(err, ErrOutput) {
				t.Fatalf("did not exercise actual output limits: %v", err)
			}
		})
	}
	if !reflect.DeepEqual([]byte(captured), []byte(first.Manifest())) {
		t.Fatal("result no longer immutable")
	}
}
