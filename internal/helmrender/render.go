// Package helmrender renders a trusted local chart with a hash-pinned Helm
// binary. It is not a release manager, authorization boundary or OS sandbox.
package helmrender

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/laojianzi/aster/internal/credentialexec"
)

const (
	MaxChartArchive      = 1 << 20
	MaxChartBytes        = 4 << 20
	MaxFileBytes         = 256 << 10
	MaxEntries           = 128
	MaxValuesBytes       = 256 << 10
	MaxDistributionBytes = 64 << 20
	MaxOutputBytes       = 2 << 20
	MaxOutputNewlines    = 160000
	MaxOutputLine        = 8192
	Timeout              = 15 * time.Second
)

var (
	ErrInput        = errors.New("Helm input is invalid, unsupported or exceeds the local input budget")
	ErrDistribution = errors.New("Helm requires the exact approved distribution archive for this platform")
	ErrRender       = errors.New("Helm rendering failed; no result was accepted (renderer details withheld)")
	ErrOutput       = errors.New("Helm output exceeds the byte, line or display budget, or is not valid UTF-8")
	ErrBusy         = errors.New("another Helm render is still running or being reclaimed")
	// Admission is process-wide, not per workspace. Cancellation does not release
	// it until the renderer has joined and private temporary input is removed.
	admission   = make(chan struct{}, 1)
	label       = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	kubeVersion = regexp.MustCompile(`^1\.(35|36|37)\.[0-9]{1,3}$`)
)

//go:embed distributions.json
var distributionData []byte

type distribution struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}
type distributionManifest struct {
	Version   string                  `json:"version"`
	Platforms map[string]distribution `json:"platforms"`
}

// Request selects local regular files, not URLs, command lines or PATH entries.
// Trust is an explicit user acknowledgement: limits do not sandbox templates.
type Request struct {
	HelmArchive, ChartArchive, ValuesFile string
	Release, Namespace, KubeVersion       string
	Trusted                               bool
}

// Result owns the one captured output. Accepting it never reruns Helm or reads
// the source files again. No mutable bytes or release capability are exported.
type Result struct{ manifest, namespace, release, version, kubeVersion, chartDigest string }

func (r *Result) Manifest() string {
	if r == nil {
		return ""
	}
	return r.manifest
}
func (r *Result) Namespace() string {
	if r == nil {
		return ""
	}
	return r.namespace
}
func (r *Result) Summary() string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("Helm %s · %s · namespace %s · Kubernetes capability assumption %s · %d captured bytes · chart SHA256 %s. No Helm release or cluster write.", r.version, r.release, r.namespace, r.kubeVersion, len(r.manifest), r.chartDigest)
}

func ApprovedArchive() string {
	d, _, ok := approved()
	if !ok {
		return "unsupported platform"
	}
	return d.File
}
func approved() (distribution, string, bool) {
	var m distributionManifest
	if json.Unmarshal(distributionData, &m) != nil {
		return distribution{}, "", false
	}
	d, ok := m.Platforms[runtime.GOOS+"-"+runtime.GOARCH]
	return d, m.Version, ok && len(d.SHA256) == 64
}

func Render(parent context.Context, request Request) (*Result, error) {
	if parent == nil {
		return nil, ErrInput
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if !request.Trusted || len(request.Release) > 53 || !label.MatchString(request.Release) || len(request.Namespace) > 63 || !label.MatchString(request.Namespace) || !kubeVersion.MatchString(request.KubeVersion) {
		return nil, ErrInput
	}
	select {
	case admission <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	defer func() { <-admission }()
	ctx, cancel := context.WithTimeout(parent, Timeout)
	defer cancel()
	d, version, ok := approved()
	if !ok {
		return nil, ErrDistribution
	}
	chart, err := readLocal(ctx, request.ChartArchive, MaxChartArchive)
	if err != nil {
		return nil, ErrInput
	}
	files, err := chartFiles(ctx, chart)
	if err != nil {
		return nil, ErrInput
	}
	values := []byte("{}\n")
	if request.ValuesFile != "" {
		values, err = readLocal(ctx, request.ValuesFile, MaxValuesBytes)
		if err != nil || !mapping(ctx, values, nil) {
			return nil, ErrInput
		}
	}
	archive, err := readLocal(ctx, request.HelmArchive, MaxDistributionBytes)
	if err != nil {
		return nil, ErrDistribution
	}
	digest := sha256.Sum256(archive)
	if hex.EncodeToString(digest[:]) != d.SHA256 {
		return nil, ErrDistribution
	}
	root, err := os.MkdirTemp("", "aster-helm-")
	if err != nil {
		return nil, ErrRender
	}
	defer os.RemoveAll(root)
	executable, err := unpackExecutable(ctx, archive, d.File, root)
	if err != nil {
		return nil, ErrDistribution
	}
	chartDir := filepath.Join(root, "chart")
	for name, data := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		target := filepath.Join(chartDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return nil, ErrRender
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			return nil, ErrRender
		}
	}
	env, err := isolatedEnvironment(root)
	if err != nil {
		return nil, ErrRender
	}
	// Preserve ALL rendered objects, including hooks. The existing delivery parser
	// rejects lifecycle annotations/Secrets/cluster objects; do not silently omit.
	args := []string{"template", request.Release, chartDir, "--dry-run=client", "--namespace", request.Namespace, "--kube-version", request.KubeVersion, "--values", "-", "--include-crds", "--skip-schema-validation", "--disable-openapi-validation"}
	output, err := credentialexec.RunRenderHelper(ctx, executable, args, env, root, values)
	if err != nil {
		if parent.Err() != nil {
			return nil, parent.Err()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, credentialexec.ErrOutputLimit) {
			return nil, ErrOutput
		}
		return nil, ErrRender
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !outputWithinBudget(output) {
		return nil, ErrOutput
	}
	chartHash := sha256.Sum256(chart)
	return &Result{manifest: string(output), namespace: request.Namespace, release: request.Release, version: version, kubeVersion: request.KubeVersion, chartDigest: hex.EncodeToString(chartHash[:])}, nil
}

func outputWithinBudget(data []byte) bool {
	if len(data) == 0 || len(data) > MaxOutputBytes || !utf8.Valid(data) || len(bytes.TrimSpace(data)) == 0 {
		return false
	}
	lines, width := 0, 0
	for _, c := range data {
		if c == 0 {
			return false
		}
		if c == '\n' {
			lines++
			width = 0
		} else {
			width++
		}
		if lines > MaxOutputNewlines || width > MaxOutputLine {
			return false
		}
	}
	return true
}

func isolatedEnvironment(root string) ([]string, error) {
	// No inherited HOME/PATH, cloud credentials, proxy, Helm, loader or runtime
	// overrides. Registry/config/plugin locations are fresh and empty each time.
	dirs := []string{"config", "cache", "data", "plugins", "repositories", "tmp"}
	for _, dir := range dirs {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			return nil, err
		}
	}
	files := map[string]string{"kubeconfig": "apiVersion: v1\nkind: Config\nclusters: []\ncontexts: []\nusers: []\n", "registry.json": "{\"auths\":{}}", "repositories.yaml": "repositories: []\n"}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			return nil, err
		}
	}
	env := []string{"HOME=" + root, "USERPROFILE=" + root, "PATH=" + root, "LANG=C.UTF-8", "HELM_DRIVER=memory", "HELM_NO_PLUGINS=1", "GOMEMLIMIT=256MiB", "GOMAXPROCS=2", "GOTRACEBACK=none"}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		env = append(env, key+"="+filepath.Join(root, "tmp"))
	}
	for key, name := range map[string]string{"HELM_CONFIG_HOME": "config", "HELM_CACHE_HOME": "cache", "HELM_DATA_HOME": "data", "HELM_PLUGINS": "plugins", "HELM_REPOSITORY_CACHE": "repositories", "HELM_REGISTRY_CONFIG": "registry.json", "HELM_REPOSITORY_CONFIG": "repositories.yaml", "KUBECONFIG": "kubeconfig", "XDG_CONFIG_HOME": "config", "XDG_CACHE_HOME": "cache", "XDG_DATA_HOME": "data", "APPDATA": "config", "LOCALAPPDATA": "data"} {
		env = append(env, key+"="+filepath.Join(root, name))
	}
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SystemRoot", "WINDIR"} {
			value := os.Getenv(key)
			if len(value) > 4096 || strings.ContainsRune(value, 0) {
				return nil, ErrInput
			}
			if value != "" {
				env = append(env, key+"="+value)
			}
		}
	}
	return env, nil
}
