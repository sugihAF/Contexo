package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.4.0", "1.3.0", true},
		{"1.3.0", "1.3.0", false},
		{"1.3.0", "1.4.0", false},
		{"v1.4.0", "1.3.0", true},    // leading v stripped
		{"1.4.0", "v1.3.0", true},    // leading v stripped on current
		{"1.10.0", "1.9.0", true},    // numeric, not lexical
		{"2.0.0", "1.9.9", true},     // major bump
		{"1.3.1", "1.3.0", true},     // patch bump
		{"1.3", "1.3.0", false},      // 1.3 == 1.3.0
		{"1.3.1", "1.3", true},       // 1.3.1 > 1.3.0
		{"1.4.0-rc1", "1.3.0", true}, // pre-release suffix ignored, core compared
		{"garbage", "1.3.0", false},  // unparseable -> never newer
		{"1.4.0", "dev", false},      // dev current -> never (caller guards anyway)
	}
	for _, c := range cases {
		if got := IsNewer(c.latest, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

const sampleReleaseJSON = `{
  "tag_name": "v1.4.0",
  "assets": [
    {"name": "ctx_1.4.0_linux_amd64.tar.gz",   "browser_download_url": "https://example/linux_amd64",   "size": 100},
    {"name": "ctx_1.4.0_linux_arm64.tar.gz",   "browser_download_url": "https://example/linux_arm64",   "size": 101},
    {"name": "ctx_1.4.0_darwin_amd64.tar.gz",  "browser_download_url": "https://example/darwin_amd64",  "size": 102},
    {"name": "ctx_1.4.0_darwin_arm64.tar.gz",  "browser_download_url": "https://example/darwin_arm64",  "size": 103},
    {"name": "ctx_1.4.0_windows_amd64.zip",    "browser_download_url": "https://example/windows_amd64", "size": 104},
    {"name": "checksums.txt",                  "browser_download_url": "https://example/checksums",     "size": 50}
  ]
}`

func TestParseRelease(t *testing.T) {
	rel, err := parseRelease([]byte(sampleReleaseJSON))
	if err != nil {
		t.Fatalf("parseRelease error: %v", err)
	}
	if rel.TagName != "v1.4.0" {
		t.Errorf("TagName = %q, want v1.4.0", rel.TagName)
	}
	if len(rel.Assets) != 6 {
		t.Fatalf("len(Assets) = %d, want 6", len(rel.Assets))
	}
	if rel.Version() != "1.4.0" {
		t.Errorf("Version() = %q, want 1.4.0", rel.Version())
	}
}

func TestAssetFor(t *testing.T) {
	rel, err := parseRelease([]byte(sampleReleaseJSON))
	if err != nil {
		t.Fatalf("parseRelease error: %v", err)
	}

	a, err := rel.AssetFor("linux", "amd64")
	if err != nil {
		t.Fatalf("AssetFor(linux, amd64) error: %v", err)
	}
	if a.Name != "ctx_1.4.0_linux_amd64.tar.gz" {
		t.Errorf("AssetFor(linux, amd64) = %q", a.Name)
	}

	w, err := rel.AssetFor("windows", "amd64")
	if err != nil {
		t.Fatalf("AssetFor(windows, amd64) error: %v", err)
	}
	if w.Name != "ctx_1.4.0_windows_amd64.zip" {
		t.Errorf("AssetFor(windows, amd64) = %q (want the .zip)", w.Name)
	}

	if _, err := rel.AssetFor("linux", "riscv64"); err == nil {
		t.Error("AssetFor(linux, riscv64) expected error for missing asset, got nil")
	}
}

func TestParseChecksums(t *testing.T) {
	data := []byte(
		"aaaa1111  ctx_1.4.0_linux_amd64.tar.gz\n" +
			"bbbb2222  ctx_1.4.0_windows_amd64.zip\n")
	sums := parseChecksums(data)
	if got := sums["ctx_1.4.0_linux_amd64.tar.gz"]; got != "aaaa1111" {
		t.Errorf("linux sum = %q, want aaaa1111", got)
	}
	if got := sums["ctx_1.4.0_windows_amd64.zip"]; got != "bbbb2222" {
		t.Errorf("windows sum = %q, want bbbb2222", got)
	}
}

func TestExtractBinaryTarGz(t *testing.T) {
	want := []byte("ELF-LIKE-BINARY-BYTES")
	archive := makeTarGz(t, "ctx", want)

	got, err := extractBinary(archive, "ctx_1.4.0_linux_amd64.tar.gz", "ctx")
	if err != nil {
		t.Fatalf("extractBinary(tar.gz) error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extractBinary(tar.gz) = %q, want %q", got, want)
	}
}

func TestExtractBinaryZip(t *testing.T) {
	want := []byte("PE-LIKE-BINARY-BYTES")
	archive := makeZip(t, "ctx.exe", want)

	got, err := extractBinary(archive, "ctx_1.4.0_windows_amd64.zip", "ctx.exe")
	if err != nil {
		t.Fatalf("extractBinary(zip) error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extractBinary(zip) = %q, want %q", got, want)
	}
}

// --- archive fixtures ---

func makeTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
