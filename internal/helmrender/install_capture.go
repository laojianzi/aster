package helmrender

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/laojianzi/aster/internal/credentialexec"
	"github.com/laojianzi/aster/internal/helmrelease"
)

// InstallRequest explicitly accepts the pinned client's capability assumption,
// not a requested --kube-version override (helm install has no such flag).
type InstallRequest struct {
	HelmArchive, ChartArchive, ValuesFile    string
	Release, Namespace, CapabilityAssumption string
	Trusted                                  bool
}

// CaptureInstall obtains a complete release once through the existing approved
// archive verifier, private chart snapshot and bounded isolated process. It has
// no cluster credentials, live transport, release writes or later file reads.
// Trusted templates are NOT sandboxed by input bounds or environment isolation.
func CaptureInstall(parent context.Context, request InstallRequest) (*helmrelease.InstallCandidate, error) {
	if parent == nil {
		return nil, ErrInput
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if !request.Trusted || len(request.Release) > 53 || !label.MatchString(request.Release) ||
		len(request.Namespace) > 63 || !label.MatchString(request.Namespace) ||
		request.CapabilityAssumption != helmrelease.InstallCapabilityAssumption {
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
	if !ok || version != "v4.3.0" {
		return nil, installCaptureFailure(ctx, ErrDistribution)
	}
	chart, err := readLocal(ctx, request.ChartArchive, MaxChartArchive)
	if err != nil {
		return nil, installCaptureFailure(ctx, ErrInput)
	}
	defer clear(chart)
	files, err := chartFiles(ctx, chart)
	if err != nil {
		return nil, installCaptureFailure(ctx, ErrInput)
	}
	defer func() {
		for _, data := range files {
			clear(data)
		}
	}()
	values := []byte("{}\n")
	if request.ValuesFile != "" {
		values, err = readLocal(ctx, request.ValuesFile, MaxValuesBytes)
		if err != nil {
			return nil, installCaptureFailure(ctx, ErrInput)
		}
		defer clear(values)
		if !mapping(ctx, values, nil) {
			return nil, installCaptureFailure(ctx, ErrInput)
		}
	}
	archive, err := readLocal(ctx, request.HelmArchive, MaxDistributionBytes)
	if err != nil {
		return nil, installCaptureFailure(ctx, ErrDistribution)
	}
	defer clear(archive)
	digest := sha256.Sum256(archive)
	if hex.EncodeToString(digest[:]) != d.SHA256 {
		return nil, installCaptureFailure(ctx, ErrDistribution)
	}
	root, err := os.MkdirTemp("", "aster-helm-install-")
	if err != nil {
		return nil, installCaptureFailure(ctx, ErrRender)
	}
	defer os.RemoveAll(root)
	executable, err := unpackExecutable(ctx, archive, d.File, root)
	if err != nil {
		return nil, installCaptureFailure(ctx, ErrDistribution)
	}
	chartDir := filepath.Join(root, "chart")
	for name, data := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		target := filepath.Join(chartDir, filepath.FromSlash(name))
		if os.MkdirAll(filepath.Dir(target), 0700) != nil || os.WriteFile(target, data, 0600) != nil {
			return nil, installCaptureFailure(ctx, ErrRender)
		}
	}
	env, err := isolatedEnvironment(root)
	if err != nil {
		return nil, installCaptureFailure(ctx, ErrRender)
	}
	// No no-hooks/hide-secret/skip-crds flags: the complete chart and hooks remain
	// visible to the restrictive decoder. No render-to-install conversion.
	args := []string{"install", request.Release, chartDir, "--dry-run=client", "--namespace", request.Namespace,
		"--values", "-", "--output", "json", "--skip-schema-validation", "--disable-openapi-validation"}
	output, err := credentialexec.RunRenderHelper(ctx, executable, args, env, root, values)
	defer clear(output)
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
		return nil, installCaptureFailure(ctx, ErrRender)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// JSON is never displayed. Unlike rendered lines its encoded single line
	// uses the byte/depth/token budgets, not the UI's per-line display budget.
	if len(output) == 0 || len(output) > MaxOutputBytes || !utf8.Valid(output) {
		return nil, ErrOutput
	}
	return helmrelease.DecodeInstallCapture(ctx, output, request.Namespace, request.Release, time.Now())
}

// Preserve cancellation/deadline identity at each snapshot/provenance stage.
// A timed-out archive read must not be misreported as an unapproved archive.
func installCaptureFailure(ctx context.Context, fallback error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fallback
}
