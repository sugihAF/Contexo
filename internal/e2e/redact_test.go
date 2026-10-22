package e2e

import (
	"bytes"
	"strings"
	"testing"
)

// Unit tests of the golden redactor (golden_redact_test.go). They need no
// ctx binary, so they also run under -short.

func TestRedact_SHAPlaceholdersByFirstAppearance(t *testing.T) {
	a := strings.Repeat("ab", 20)
	b := strings.Repeat("cd", 20)
	in := `{"sha":"` + a + `","base_sha":"` + b + `","server_sha":"` + a + `"}`
	got := string(newRedactor().redact([]byte(in)))
	want := `{"sha":"0000000000000000000000000000000000000001",` +
		`"base_sha":"0000000000000000000000000000000000000002",` +
		`"server_sha":"0000000000000000000000000000000000000001"}`
	if got != want {
		t.Fatalf("redact:\n got %s\nwant %s", got, want)
	}
}

func TestRedact_PlaceholdersArePerCall(t *testing.T) {
	r := newRedactor()
	b := strings.Repeat("cd", 20)
	_ = r.redact([]byte(`"` + strings.Repeat("ab", 20) + `"`))
	got := string(r.redact([]byte(`"` + b + `"`)))
	if got != `"0000000000000000000000000000000000000001"` {
		t.Fatalf("second file must restart numbering at 1, got %s", got)
	}
}

func TestRedact_SessionSharesNumbering(t *testing.T) {
	a := strings.Repeat("ab", 20)
	b := strings.Repeat("cd", 20)
	s := newRedactor().session()
	_ = s.redact([]byte(`"` + a + `"`))
	got := string(s.redact([]byte(`["show","p.md","--at","` + b + `","` + a + `"]`)))
	want := `["show","p.md","--at","0000000000000000000000000000000000000002","0000000000000000000000000000000000000001"]`
	if got != want {
		t.Fatalf("a file's command must continue the file's numbering:\n got %s\nwant %s", got, want)
	}
}

func TestRedact_ContentHashesAndOtherHexKept(t *testing.T) {
	h64 := "sha256:" + strings.Repeat("0123456789abcdef", 4)
	h41 := strings.Repeat("a", 41)
	upper := strings.Repeat("AB", 20)
	in := `{"hash":"` + h64 + `","x":"` + h41 + `","y":"` + upper + `"}`
	if got := string(newRedactor().redact([]byte(in))); got != in {
		t.Fatalf("64-hex hashes, 41-hex runs and upper-case hex must stay:\n got %s\nwant %s", got, in)
	}
}

func TestRedact_WholeSecondTimesOnly(t *testing.T) {
	in := `{"a":"2026-10-22T08:09:10Z","b":"2026-10-22T08:09:10.5Z","c":"2026-10-22T15:09:10+07:00"}`
	want := `{"a":"2000-01-01T00:00:00Z","b":"2026-10-22T08:09:10.5Z","c":"2026-10-22T15:09:10+07:00"}`
	if got := string(newRedactor().redact([]byte(in))); got != want {
		t.Fatalf("redact:\n got %s\nwant %s", got, want)
	}
}

func TestRedact_ServerURLsAndBareHosts(t *testing.T) {
	r := newRedactor()
	r.addURL("http://127.0.0.1:54321")
	r.addURL("http://127.0.0.1:5")
	in := `{"url":"http://127.0.0.1:54321","m":"Get \"http://127.0.0.1:54321/v1/repos/x/pages\": dial tcp 127.0.0.1:54321: connect: connection refused","o":"http://127.0.0.1:5"}`
	want := `{"url":"http://contexo-e2e.invalid","m":"Get \"http://contexo-e2e.invalid/v1/repos/x/pages\": dial tcp contexo-e2e.invalid: connect: connection refused","o":"http://contexo-e2e.invalid"}`
	if got := string(r.redact([]byte(in))); got != want {
		t.Fatalf("redact:\n got %s\nwant %s", got, want)
	}
}

func TestRedact_WindowsDialErrorNormalized(t *testing.T) {
	r := newRedactor()
	r.addURL("http://127.0.0.1:6000")
	in := `"dial tcp 127.0.0.1:6000: connectex: No connection could be made because the target machine actively refused it."`
	want := `"dial tcp contexo-e2e.invalid: connect: connection refused"`
	if got := string(r.redact([]byte(in))); got != want {
		t.Fatalf("redact:\n got %s\nwant %s", got, want)
	}
}

func TestRedact_UnixRoots(t *testing.T) {
	r := newRedactor()
	r.addRoot("/private/var/folders/x/T/TestGolden001/002", "alice")
	in := `{"root":"/private/var/folders/x/T/TestGolden001/002","contexo_dir":"/private/var/folders/x/T/TestGolden001/002/.contexo","m":"no .contexo/ directory in /private/var/folders/x/T/TestGolden001/002"}`
	want := `{"root":"/e2e/alice","contexo_dir":"/e2e/alice/.contexo","m":"no .contexo/ directory in /e2e/alice"}`
	if got := string(r.redact([]byte(in))); got != want {
		t.Fatalf("redact:\n got %s\nwant %s", got, want)
	}
}

func TestRedact_WindowsRootsRawAndEscaped(t *testing.T) {
	r := newRedactor()
	r.addRoot(`C:\Users\runner\AppData\Local\Temp\TestGolden1\002`, "bob")
	in := `{"root":"C:\\Users\\runner\\AppData\\Local\\Temp\\TestGolden1\\002","contexo_dir":"C:\\Users\\runner\\AppData\\Local\\Temp\\TestGolden1\\002\\.contexo"}` +
		"\nraw C:\\Users\\runner\\AppData\\Local\\Temp\\TestGolden1\\002\\.contexo\\wiki"
	want := `{"root":"/e2e/bob","contexo_dir":"/e2e/bob/.contexo"}` + "\nraw /e2e/bob/.contexo/wiki"
	if got := string(r.redact([]byte(in))); got != want {
		t.Fatalf("redact:\n got %s\nwant %s", got, want)
	}
}

func TestRedact_LongestRootFirst(t *testing.T) {
	r := newRedactor()
	r.addRoot("/tmp/e2e/a", "alice")
	r.addRoot("/tmp/e2e/a2", "carol")
	in := `"/tmp/e2e/a2/.contexo" "/tmp/e2e/a/.contexo"`
	want := `"/e2e/carol/.contexo" "/e2e/alice/.contexo"`
	if got := string(r.redact([]byte(in))); got != want {
		t.Fatalf("redact:\n got %s\nwant %s", got, want)
	}
}

func TestRedact_PidAndHost(t *testing.T) {
	r := newRedactor()
	r.host = "runner-17.local"
	in := `{"message":"push: another ctx process is syncing (e2e, pid 98765 on runner-17.local, since 2026-10-22T08:09:10Z)","details":{"pid":98765,"host":"runner-17.local","command":"e2e"}}`
	want := `{"message":"push: another ctx process is syncing (e2e, pid 4242 on e2e-host, since 2000-01-01T00:00:00Z)","details":{"pid":4242,"host":"e2e-host","command":"e2e"}}`
	if got := string(r.redact([]byte(in))); got != want {
		t.Fatalf("redact:\n got %s\nwant %s", got, want)
	}
}

func TestRedact_BackupTimestamp(t *testing.T) {
	in := `"backup_file": ".sync/backup/wiki/concepts/p.md.20261022T080910Z",`
	want := `"backup_file": ".sync/backup/wiki/concepts/p.md.20000101T000000Z",`
	if got := string(newRedactor().redact([]byte(in))); got != want {
		t.Fatalf("redact:\n got %s\nwant %s", got, want)
	}
}

func TestRedact_VersionOSArch(t *testing.T) {
	r := newRedactor()
	r.goos, r.goarch = "darwin", "arm64"
	in := "{\n  \"os\": \"darwin\",\n  \"arch\": \"arm64\",\n  \"x\": \"darwin\"\n}\n"
	want := "{\n  \"os\": \"e2e-os\",\n  \"arch\": \"e2e-arch\",\n  \"x\": \"darwin\"\n}\n"
	if got := string(r.redact([]byte(in))); got != want {
		t.Fatalf("redact:\n got %q\nwant %q", got, want)
	}
}

func TestRedact_PreservesLayoutAndIsIdempotent(t *testing.T) {
	r := newRedactor()
	r.addRoot("/tmp/e2e/root", "alice")
	r.addURL("http://127.0.0.1:7000")
	in := []byte("{\n  \"schema\": \"ctx.status.v1\",\n  \"root\": \"/tmp/e2e/root\",\n" +
		"  \"server\": {\n    \"url\": \"http://127.0.0.1:7000\"\n  },\n" +
		"  \"head\": \"" + strings.Repeat("9f", 20) + "\",\n" +
		"  \"generated_at\": \"2026-10-22T08:09:10Z\",\n  \"pages\": []\n}\n")
	once := r.redact(in)
	if bytes.Count(once, []byte("\n")) != bytes.Count(in, []byte("\n")) {
		t.Fatalf("line structure changed:\n%s", once)
	}
	for _, key := range []string{"schema", "root", "server", "url", "head", "generated_at", "pages"} {
		if bytes.Index(once, []byte(`"`+key+`"`)) < 0 {
			t.Fatalf("key %q lost:\n%s", key, once)
		}
	}
	if twice := newRedactor().redact(once); !bytes.Equal(twice, once) {
		t.Fatalf("redaction is not idempotent:\n%s\n---\n%s", once, twice)
	}
	if plain := []byte("{\n  \"a\": [],\n  \"b\": 1\n}\n"); !bytes.Equal(newRedactor().redact(plain), plain) {
		t.Fatal("a document with nothing to redact must be returned unchanged")
	}
}
