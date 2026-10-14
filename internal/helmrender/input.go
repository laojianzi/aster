package helmrender

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/laojianzi/aster/internal/manifest"
	yaml "go.yaml.in/yaml/v3"
)

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func readLocal(ctx context.Context, name string, limit int64) ([]byte, error) {
	if len(name) > 4096 || !filepath.IsAbs(name) || strings.HasPrefix(name, `\\`) || strings.HasPrefix(name, "//") || strings.ContainsRune(name, 0) {
		return nil, ErrInput
	}
	st, err := os.Lstat(name)
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, ErrInput
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, ErrInput
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(st, actual) {
		return nil, ErrInput
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, f}, limit+1))
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		return nil, ErrInput
	}
	return data, nil
}

// Reuse bounded YAML source preflight before decoding. Alias expansion, merge
// keys, ambiguous keys, multi-document streams and partial indexes are refused.
func mapping(ctx context.Context, data []byte, result *map[string]any) bool {
	index, err := manifest.IndexSource(ctx, data)
	if err != nil || index.Partial() {
		return false
	}
	var m map[string]any
	if yaml.Unmarshal(data, &m) != nil || m == nil {
		return false
	}
	if result != nil {
		*result = m
	}
	return ctx.Err() == nil
}

func safeMember(name string) bool {
	if name == "" || len(name) > 256 || path.Clean(name) != name || strings.Count(name, "/") > 8 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") {
			return false
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9' {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return false
			}
		}
	}
	return true
}

func chartFiles(ctx context.Context, data []byte) (map[string][]byte, error) {
	if len(data) == 0 || len(data) > MaxChartArchive {
		return nil, ErrInput
	}
	compressed := bytes.NewReader(data)
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, ErrInput
	}
	defer gz.Close()
	gz.Multistream(false)
	expanded := &io.LimitedReader{R: contextReader{ctx, gz}, N: MaxChartBytes + (1 << 20) + 1}
	tr := tar.NewReader(expanded)
	files := map[string][]byte{}
	seen := map[string]bool{}
	total, entries := 0, 0
	root := ""
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrInput
		}
		entries++
		if entries > MaxEntries {
			return nil, ErrInput
		}
		name := h.Name
		if h.Typeflag == tar.TypeDir {
			if h.Size != 0 {
				return nil, ErrInput
			}
			name = strings.TrimSuffix(name, "/")
		}
		if !safeMember(name) || seen[strings.ToLower(name)] || h.Size < 0 || h.Size > MaxFileBytes || h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			return nil, ErrInput
		}
		seen[strings.ToLower(name)] = true
		parts := strings.SplitN(name, "/", 2)
		if root == "" {
			root = parts[0]
		}
		if root != parts[0] {
			return nil, ErrInput
		}
		if len(parts) == 1 {
			if h.Typeflag != tar.TypeDir {
				return nil, ErrInput
			}
			continue
		}
		relative := parts[1]
		first := strings.SplitN(relative, "/", 2)[0]
		if first == "charts" || first == "crds" || first == "values.schema.json" || first == "Chart.lock" || first == ".helmignore" {
			return nil, ErrInput
		}
		if h.Typeflag == tar.TypeDir {
			if first != "templates" && first != "files" {
				return nil, ErrInput
			}
			continue
		}
		if relative != "Chart.yaml" && relative != "values.yaml" && relative != "README.md" && relative != "LICENSE" && !strings.HasPrefix(relative, "templates/") && !strings.HasPrefix(relative, "files/") {
			return nil, ErrInput
		}
		total += int(h.Size)
		if total > MaxChartBytes {
			return nil, ErrInput
		}
		b, err := io.ReadAll(tr)
		if err != nil || int64(len(b)) != h.Size {
			return nil, ErrInput
		}
		files[relative] = b
	}
	// Force gzip CRC/trailer validation and reject concatenated archives or
	// nonzero trailing tar content. The decompression budget includes headers.
	tail, err := io.ReadAll(expanded)
	if err != nil || expanded.N == 0 || compressed.Len() != 0 {
		return nil, ErrInput
	}
	for _, b := range tail {
		if b != 0 {
			return nil, ErrInput
		}
	}
	var metadata map[string]any
	if !mapping(ctx, files["Chart.yaml"], &metadata) || metadata["apiVersion"] != "v2" || metadata["name"] != root {
		return nil, ErrInput
	}
	version, ok := metadata["version"].(string)
	if !ok || len(version) == 0 || len(version) > 128 {
		return nil, ErrInput
	}
	if t, ok := metadata["type"]; ok && t != "application" {
		return nil, ErrInput
	}
	if deps, ok := metadata["dependencies"]; ok {
		a, valid := deps.([]any)
		if !valid || len(a) != 0 {
			return nil, ErrInput
		}
	}
	if values, ok := files["values.yaml"]; ok && !mapping(ctx, values, nil) {
		return nil, ErrInput
	}
	for name := range files {
		if strings.HasPrefix(name, "templates/") {
			return files, nil
		}
	}
	return nil, ErrInput
}

// The caller verified the entire distribution against the embedded official
// digest. Extract only its one executable into a private newly-created folder.
func unpackExecutable(ctx context.Context, data []byte, filename, root string) (string, error) {
	platform := strings.TrimPrefix(filename, "helm-v4.3.0-")
	platform = strings.TrimSuffix(strings.TrimSuffix(platform, ".tar.gz"), ".zip")
	targetName := "helm"
	if strings.HasPrefix(platform, "windows-") {
		targetName += ".exe"
	}
	member := platform + "/" + targetName
	dest := filepath.Join(root, targetName)
	copyExecutable := func(r io.Reader, size int64) error {
		if size <= 0 || size > 256<<20 {
			return ErrDistribution
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			return err
		}
		n, err := io.Copy(f, io.LimitReader(contextReader{ctx, r}, size+1))
		closeErr := f.Close()
		if err != nil || closeErr != nil || n != size {
			return ErrDistribution
		}
		return nil
	}
	found := false
	if strings.HasSuffix(filename, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil || len(zr.File) > 32 {
			return "", ErrDistribution
		}
		for _, f := range zr.File {
			if f.Name != member {
				continue
			}
			if found || !f.Mode().IsRegular() || f.UncompressedSize64 > 256<<20 {
				return "", ErrDistribution
			}
			r, err := f.Open()
			if err != nil {
				return "", ErrDistribution
			}
			err = copyExecutable(r, int64(f.UncompressedSize64))
			r.Close()
			if err != nil {
				return "", err
			}
			found = true
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return "", ErrDistribution
		}
		defer gz.Close()
		tr := tar.NewReader(io.LimitReader(contextReader{ctx, gz}, 300<<20))
		for count := 0; ; count++ {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil || count >= 32 {
				return "", ErrDistribution
			}
			if h.Name != member {
				continue
			}
			if found || h.Typeflag != tar.TypeReg {
				return "", ErrDistribution
			}
			if err := copyExecutable(tr, h.Size); err != nil {
				return "", err
			}
			found = true
		}
	}
	if !found {
		return "", ErrDistribution
	}
	return dest, nil
}
