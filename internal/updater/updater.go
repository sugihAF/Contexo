// Package updater resolves the latest published ctx release from GitHub and
// performs an atomic self-replace. The pure logic here (version comparison,
// release parsing, asset selection, checksum parsing, archive extraction) is
// unit-tested; the network fetch and running-binary swap live in github.go and
// apply.go and stay deliberately thin.
package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// GitHub coordinates for the public CLI repo. The repo is "Contexo" (capital
// C) on GitHub even though the Go module path is lowercase; asset URLs are read
// from the API response so casing never has to be reconstructed.
const (
	repoOwner = "sugihAF"
	repoName  = "Contexo"
)

// Release is the subset of the GitHub releases API we care about.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset is one uploaded file on a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Version returns the tag without a leading "v" (e.g. "1.4.0").
func (r *Release) Version() string {
	return strings.TrimPrefix(r.TagName, "v")
}

// AssetFor returns the release archive matching the given GOOS/GOARCH, using
// the naming convention produced by .goreleaser.yaml:
//
//	ctx_<version>_<os>_<arch>.tar.gz   (.zip on windows)
func (r *Release) AssetFor(goos, goarch string) (*Asset, error) {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	want := fmt.Sprintf("ctx_%s_%s_%s%s", r.Version(), goos, goarch, ext)
	for i := range r.Assets {
		if r.Assets[i].Name == want {
			return &r.Assets[i], nil
		}
	}
	return nil, fmt.Errorf("no release asset for %s/%s (looked for %q)", goos, goarch, want)
}

func parseRelease(data []byte) (*Release, error) {
	var r Release
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse release JSON: %w", err)
	}
	return &r, nil
}

// parseChecksums parses sha256sum-style lines ("<hex>  <filename>") into a
// filename -> hex map. Malformed lines are skipped.
func parseChecksums(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		out[fields[1]] = fields[0]
	}
	return out
}

// IsNewer reports whether latest is a strictly greater version than current.
// Both may carry a leading "v"; pre-release/build suffixes (after "-" or "+")
// are ignored. Unparseable input is treated as "not newer" so a bad tag never
// nags the user to update.
func IsNewer(latest, current string) bool {
	l, ok1 := parseSemver(latest)
	c, ok2 := parseSemver(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parseSemver parses "vX.Y.Z" (Y and Z optional, default 0) into [major, minor,
// patch]. A pre-release/build suffix is dropped before parsing.
func parseSemver(s string) ([3]int, bool) {
	var v [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return v, false
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// extractBinary pulls the named binary out of a release archive, picking the
// archive format from the asset's extension.
func extractBinary(data []byte, assetName, binName string) ([]byte, error) {
	if strings.HasSuffix(assetName, ".zip") {
		return extractFromZip(data, binName)
	}
	return extractFromTarGz(data, binName)
}

func extractFromTarGz(data []byte, binName string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if path.Base(hdr.Name) == binName {
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read %s from tar: %w", binName, err)
			}
			return b, nil
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", binName)
}

func extractFromZip(data []byte, binName string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	for _, f := range zr.File {
		if path.Base(f.Name) == binName {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open %s in zip: %w", binName, err)
			}
			defer rc.Close()
			b, err := io.ReadAll(rc)
			if err != nil {
				return nil, fmt.Errorf("read %s from zip: %w", binName, err)
			}
			return b, nil
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", binName)
}
