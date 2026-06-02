package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"
)

const releaseAPIURL = "https://api.github.com/repos/" + repoOwner + "/" + repoName + "/releases/latest"

// LatestRelease fetches the most recent published release from GitHub. The
// caller's context controls the timeout, so the nudge can pass a tight deadline
// while `ctx update` can be patient.
func LatestRelease(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPIURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contact GitHub: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("no releases published yet")
	default:
		return nil, fmt.Errorf("GitHub returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseRelease(body)
}

// BinaryName is the on-disk name of the ctx binary for the running platform.
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "ctx.exe"
	}
	return "ctx"
}

// FetchVerifiedBinary downloads the release archive for the running platform,
// verifies it against the release's checksums.txt, and returns the extracted
// binary bytes. It refuses to return anything it could not verify.
func FetchVerifiedBinary(ctx context.Context, rel *Release) ([]byte, error) {
	asset, err := rel.AssetFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	archive, err := downloadBytes(ctx, asset.URL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if err := verifyChecksum(ctx, rel, asset.Name, archive); err != nil {
		return nil, err
	}
	return extractBinary(archive, asset.Name, BinaryName())
}

func verifyChecksum(ctx context.Context, rel *Release, assetName string, archive []byte) error {
	var sumAsset *Asset
	for i := range rel.Assets {
		if rel.Assets[i].Name == "checksums.txt" {
			sumAsset = &rel.Assets[i]
			break
		}
	}
	if sumAsset == nil {
		return fmt.Errorf("release has no checksums.txt; refusing to install an unverified binary")
	}
	sumData, err := downloadBytes(ctx, sumAsset.URL)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	want := parseChecksums(sumData)[assetName]
	if want == "" {
		return fmt.Errorf("no checksum listed for %s", assetName)
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s (got %s, want %s)", assetName, got, want)
	}
	return nil
}

func downloadBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
