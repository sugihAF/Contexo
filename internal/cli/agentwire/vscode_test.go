package agentwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// wp05Seed writes body to path, creating its parent dir (.vscode/).
func wp05Seed(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func wp05ReadBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// wp05Servers returns the decoded top-level "servers" object of a VS Code file.
func wp05Servers(t *testing.T, obj map[string]interface{}) map[string]interface{} {
	t.Helper()
	servers, ok := obj["servers"].(map[string]interface{})
	if !ok {
		t.Fatalf(`"servers" missing or not an object; got %v`, obj)
	}
	return servers
}

// wp05Decode unmarshals a JSON literal used as a test fixture.
func wp05Decode(t *testing.T, s string) interface{} {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode fixture %q: %v", s, err)
	}
	return v
}

const (
	wp05Inputs = `[{"type":"promptString","id":"tok","description":"Token","password":true}]`
	wp05GitHub = `{"type":"http","url":"https://api.githubcopilot.com/mcp/"}`
	wp05Entry  = `{"type":"stdio","command":"ctx","args":["mcp","--root","${workspaceFolder}"]}`
)

// wp05JSONC are files VS Code accepts (it parses JSONC) but strict JSON does
// not — Contexo must refuse to rewrite them rather than drop the comments.
var wp05JSONC = map[string]string{
	"comment":        "{\n  // mine\n  \"servers\": {}\n}",
	"trailing_comma": `{"servers": {"a": {"command": "x"},},}`,
}

func TestWireVSCodeCreatesServersKey(t *testing.T) {
	dir := t.TempDir()
	p := VSCodeMCPPath(dir)
	if want := filepath.Join(dir, ".vscode", "mcp.json"); p != want {
		t.Fatalf("VSCodeMCPPath = %q, want %q", p, want)
	}
	changed, err := WireVSCode(p)
	if err != nil {
		t.Fatalf("WireVSCode: %v", err)
	}
	if !changed {
		t.Errorf("first wire should report changed=true")
	}
	obj := readMCP(t, p)
	if got := wp05Servers(t, obj)[ServerName]; !jsonEqual(got, vscodeEntry()) {
		t.Errorf("servers.contexo = %v, want %v", got, vscodeEntry())
	}
	if _, ok := obj["mcpServers"]; ok {
		t.Errorf("VS Code config must not use the mcpServers key; got %v", obj)
	}
	if info, err := os.Stat(filepath.Join(dir, ".vscode")); err != nil || !info.IsDir() {
		t.Errorf(".vscode/ should exist as a dir; err=%v", err)
	}
}

func TestWireVSCodePreservesOtherServersAndInputs(t *testing.T) {
	p := VSCodeMCPPath(t.TempDir())
	wp05Seed(t, p, `{"inputs":`+wp05Inputs+`,"servers":{"github":`+wp05GitHub+`}}`)

	if _, err := WireVSCode(p); err != nil {
		t.Fatalf("WireVSCode: %v", err)
	}
	obj := readMCP(t, p)
	if !jsonEqual(obj["inputs"], wp05Decode(t, wp05Inputs)) {
		t.Errorf("inputs changed: %v", obj["inputs"])
	}
	servers := wp05Servers(t, obj)
	if !jsonEqual(servers["github"], wp05Decode(t, wp05GitHub)) {
		t.Errorf("servers.github changed: %v", servers["github"])
	}
	if !jsonEqual(servers[ServerName], vscodeEntry()) {
		t.Errorf("servers.contexo = %v, want %v", servers[ServerName], vscodeEntry())
	}
}

func TestWireVSCodeIdempotent(t *testing.T) {
	t.Run("rewire", func(t *testing.T) {
		p := VSCodeMCPPath(t.TempDir())
		if _, err := WireVSCode(p); err != nil {
			t.Fatalf("first wire: %v", err)
		}
		first := wp05ReadBytes(t, p)
		changed, err := WireVSCode(p)
		if err != nil {
			t.Fatalf("second wire: %v", err)
		}
		if changed {
			t.Errorf("second wire should report changed=false")
		}
		if got := wp05ReadBytes(t, p); !bytes.Equal(got, first) {
			t.Errorf("second wire rewrote the file:\nbefore %s\nafter  %s", first, got)
		}
	})
	t.Run("user_formatting_kept", func(t *testing.T) {
		// Same entry, different key order and no indentation: a no-op must
		// not reformat the user's file.
		p := VSCodeMCPPath(t.TempDir())
		seed := `{"servers":{"contexo":{"args":["mcp","--root","${workspaceFolder}"],"command":"ctx","type":"stdio"}}}`
		wp05Seed(t, p, seed)
		changed, err := WireVSCode(p)
		if err != nil {
			t.Fatalf("WireVSCode: %v", err)
		}
		if changed {
			t.Errorf("an already-present entry should report changed=false")
		}
		if got := wp05ReadBytes(t, p); string(got) != seed {
			t.Errorf("no-op wire reformatted the file:\nbefore %s\nafter  %s", seed, got)
		}
	})
}

func TestWireVSCodeRefusesJSONC(t *testing.T) {
	for name, body := range wp05JSONC {
		t.Run(name, func(t *testing.T) {
			p := VSCodeMCPPath(t.TempDir())
			wp05Seed(t, p, body)
			changed, err := WireVSCode(p)
			if !errors.Is(err, ErrUnparseableVSCodeConfig) {
				t.Errorf("err = %v, want ErrUnparseableVSCodeConfig", err)
			}
			if changed {
				t.Errorf("a refused file must report changed=false")
			}
			if got := wp05ReadBytes(t, p); string(got) != body {
				t.Errorf("JSONC file was modified:\nbefore %s\nafter  %s", body, got)
			}
		})
	}
}

func TestWireVSCodeRefusesNonObjectServers(t *testing.T) {
	for name, body := range map[string]string{
		"servers_array":   `{"servers": []}`,
		"top_level_array": `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			p := VSCodeMCPPath(t.TempDir())
			wp05Seed(t, p, body)
			if _, err := WireVSCode(p); !errors.Is(err, ErrUnparseableVSCodeConfig) {
				t.Errorf("err = %v, want ErrUnparseableVSCodeConfig", err)
			}
			if got := wp05ReadBytes(t, p); string(got) != body {
				t.Errorf("file was modified:\nbefore %s\nafter  %s", body, got)
			}
		})
	}
}

func TestWireVSCodeReadErrorIsNotUnparseable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits do not block reads on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	dir := t.TempDir()
	p := VSCodeMCPPath(dir)
	wp05Seed(t, p, `{"servers":{}}`)
	vscodeDir := filepath.Dir(p)
	if err := os.Chmod(vscodeDir, 0o000); err != nil {
		t.Fatal(err)
	}
	// Registered after t.TempDir, so it runs first and lets TempDir clean up.
	t.Cleanup(func() { _ = os.Chmod(vscodeDir, 0o755) })

	_, err := WireVSCode(p)
	if err == nil {
		t.Fatal("expected a read error for an unreadable .vscode/")
	}
	if errors.Is(err, ErrUnparseableVSCodeConfig) {
		t.Errorf("a read failure must not be reported as a JSONC file: %v", err)
	}
}

func TestUnwireVSCodeRemovesOnlyContexo(t *testing.T) {
	p := VSCodeMCPPath(t.TempDir())
	wp05Seed(t, p, `{"inputs":`+wp05Inputs+`,"servers":{"github":`+wp05GitHub+`,"contexo":`+wp05Entry+`}}`)

	removed, deleted, err := UnwireVSCode(p)
	if err != nil {
		t.Fatalf("UnwireVSCode: %v", err)
	}
	if !removed || deleted {
		t.Errorf("removed=%v deleted=%v, want true,false", removed, deleted)
	}
	obj := readMCP(t, p)
	servers := wp05Servers(t, obj)
	if _, ok := servers[ServerName]; ok {
		t.Errorf("servers.contexo should be gone; got %v", servers)
	}
	if !jsonEqual(servers["github"], wp05Decode(t, wp05GitHub)) {
		t.Errorf("servers.github changed: %v", servers["github"])
	}
	if !jsonEqual(obj["inputs"], wp05Decode(t, wp05Inputs)) {
		t.Errorf("inputs changed: %v", obj["inputs"])
	}
}

func TestUnwireVSCodeDeletesFileWhenEmpty(t *testing.T) {
	t.Run("only_entry", func(t *testing.T) {
		p := VSCodeMCPPath(t.TempDir())
		if _, err := WireVSCode(p); err != nil {
			t.Fatalf("WireVSCode: %v", err)
		}
		removed, deleted, err := UnwireVSCode(p)
		if err != nil {
			t.Fatalf("UnwireVSCode: %v", err)
		}
		if !removed || !deleted {
			t.Errorf("removed=%v deleted=%v, want true,true", removed, deleted)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("mcp.json should be deleted; stat err=%v", err)
		}
		if info, err := os.Stat(filepath.Dir(p)); err != nil || !info.IsDir() {
			t.Errorf(".vscode/ must be kept (it holds the user's other settings); err=%v", err)
		}
	})
	t.Run("inputs_kept", func(t *testing.T) {
		p := VSCodeMCPPath(t.TempDir())
		wp05Seed(t, p, `{"inputs":[],"servers":{"contexo":`+wp05Entry+`}}`)
		removed, deleted, err := UnwireVSCode(p)
		if err != nil {
			t.Fatalf("UnwireVSCode: %v", err)
		}
		if !removed || deleted {
			t.Errorf("removed=%v deleted=%v, want true,false", removed, deleted)
		}
		obj := readMCP(t, p)
		if _, ok := obj["inputs"]; !ok {
			t.Errorf("inputs should survive; got %v", obj)
		}
		if _, ok := obj["servers"]; ok {
			t.Errorf("an emptied servers key should be dropped; got %v", obj)
		}
	})
}

func TestUnwireVSCodeNoop(t *testing.T) {
	t.Run("missing_file", func(t *testing.T) {
		p := VSCodeMCPPath(t.TempDir())
		removed, deleted, err := UnwireVSCode(p)
		if removed || deleted || err != nil {
			t.Errorf("got %v,%v,%v; want false,false,nil", removed, deleted, err)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("unwire must not create the file; stat err=%v", err)
		}
	})
	t.Run("no_entry", func(t *testing.T) {
		p := VSCodeMCPPath(t.TempDir())
		seed := `{"servers":{"github":` + wp05GitHub + `}}`
		wp05Seed(t, p, seed)
		removed, deleted, err := UnwireVSCode(p)
		if removed || deleted || err != nil {
			t.Errorf("got %v,%v,%v; want false,false,nil", removed, deleted, err)
		}
		if got := wp05ReadBytes(t, p); string(got) != seed {
			t.Errorf("no-op unwire rewrote the file:\nbefore %s\nafter  %s", seed, got)
		}
	})
}

func TestUnwireVSCodeRefusesJSONC(t *testing.T) {
	p := VSCodeMCPPath(t.TempDir())
	body := "{\n  // mine\n  \"servers\": {\"contexo\": " + wp05Entry + "}\n}"
	wp05Seed(t, p, body)
	removed, deleted, err := UnwireVSCode(p)
	if !errors.Is(err, ErrUnparseableVSCodeConfig) {
		t.Errorf("err = %v, want ErrUnparseableVSCodeConfig", err)
	}
	if removed || deleted {
		t.Errorf("removed=%v deleted=%v, want false,false", removed, deleted)
	}
	if got := wp05ReadBytes(t, p); string(got) != body {
		t.Errorf("JSONC file was modified:\nbefore %s\nafter  %s", body, got)
	}
}

func TestWiredVSCode(t *testing.T) {
	p := VSCodeMCPPath(t.TempDir())
	if wired, err := WiredVSCode(p); wired || err != nil {
		t.Errorf("missing file: got %v,%v; want false,nil", wired, err)
	}
	if _, err := WireVSCode(p); err != nil {
		t.Fatalf("WireVSCode: %v", err)
	}
	if wired, err := WiredVSCode(p); !wired || err != nil {
		t.Errorf("after wire: got %v,%v; want true,nil", wired, err)
	}

	jsonc := VSCodeMCPPath(t.TempDir())
	wp05Seed(t, jsonc, wp05JSONC["comment"])
	wired, err := WiredVSCode(jsonc)
	if wired || !errors.Is(err, ErrUnparseableVSCodeConfig) {
		t.Errorf("JSONC: got %v,%v; want false,ErrUnparseableVSCodeConfig", wired, err)
	}
}

func TestVSCodeInstalledIgnoresTermProgram(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", "")
	// Cursor's integrated terminal sets this too, so it proves nothing.
	t.Setenv("TERM_PROGRAM", "vscode")
	if VSCodeInstalled() {
		t.Errorf("TERM_PROGRAM=vscode alone must not count as VS Code installed")
	}
}

func TestVSCodeInstalledDetectsHomeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", "")
	if err := os.Mkdir(filepath.Join(home, ".vscode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !VSCodeInstalled() {
		t.Errorf("~/.vscode should count as VS Code installed")
	}
}

func TestVSCodeManualSnippetMatchesEntry(t *testing.T) {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(VSCodeManualSnippet()), &obj); err != nil {
		t.Fatalf("snippet is not valid JSON: %v\n%s", err, VSCodeManualSnippet())
	}
	if len(obj) != 1 {
		t.Errorf("snippet should hold only the servers key; got %v", obj)
	}
	servers := wp05Servers(t, obj)
	if len(servers) != 1 || !jsonEqual(servers[ServerName], vscodeEntry()) {
		t.Errorf("snippet servers = %v, want only contexo = %v", servers, vscodeEntry())
	}
}
