package gitstore

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// wp25Pages are page bytes a host's line-ending conversion would rewrite:
// CRLF with no final line break, CRLF behind a UTF-8 BOM, and mixed line
// ends (which core.safecrlf=true refuses to convert at all).
var wp25Pages = []struct{ path, body string }{
	{"wiki/crlf.md", "# CRLF\r\n\r\nLine one.\r\nLine two."},
	{"wiki/bom.md", "\xef\xbb\xbf# BOM\r\n\r\nBody.\r\n"},
	{"wiki/mixed.md", "# Mixed\r\nlf line\ncrlf line\r\n"},
}

// wp25HostConfig is a host git setup that converts line ends on add.
type wp25HostConfig struct {
	// autocrlf sets core.autocrlf=true (GitHub's Windows runners and Git for
	// Windows' default), with core.safecrlf=true and core.eol=crlf.
	autocrlf bool
	// globalAttrs points core.attributesFile at a file listing "* text=auto".
	globalAttrs bool
	// templateAttrs points init.templateDir at a template whose
	// info/attributes lists "* text=auto", which git init copies into every
	// new repo.
	templateAttrs bool
}

// wp25UseHostConfig points git at a temporary global config built from h
// (GIT_CONFIG_GLOBAL) and keeps the machine's system config out
// (GIT_CONFIG_NOSYSTEM).
func wp25UseHostConfig(t *testing.T, h wp25HostConfig) {
	t.Helper()
	dir := t.TempDir()
	var cfg strings.Builder
	cfg.WriteString("[core]\n")
	if h.autocrlf {
		cfg.WriteString("\tautocrlf = true\n\tsafecrlf = true\n\teol = crlf\n")
	}
	if h.globalAttrs {
		attrs := filepath.Join(dir, "attributes")
		if err := os.WriteFile(attrs, []byte("* text=auto\n"), 0o644); err != nil {
			t.Fatalf("write global attributes: %v", err)
		}
		fmt.Fprintf(&cfg, "\tattributesFile = %s\n", filepath.ToSlash(attrs))
	}
	if h.templateAttrs {
		tpl := filepath.Join(dir, "template")
		if err := os.MkdirAll(filepath.Join(tpl, "info"), 0o755); err != nil {
			t.Fatalf("mkdir template: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tpl, "info", "attributes"), []byte("* text=auto\n"), 0o644); err != nil {
			t.Fatalf("write template attributes: %v", err)
		}
		fmt.Fprintf(&cfg, "[init]\n\ttemplateDir = %s\n", filepath.ToSlash(tpl))
	}
	global := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(global, []byte(cfg.String()), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// TestPageBytesSurviveHostEOLConfig: the store keeps page bytes exactly,
// whatever line-ending conversion the host's git config asks for. CI run
// 39822198643 (windows-latest, core.autocrlf=true) stored a CRLF page as LF,
// so `ctx show --at server` returned other bytes than were pushed.
func TestPageBytesSurviveHostEOLConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		host wp25HostConfig
	}{
		{"autocrlf", wp25HostConfig{autocrlf: true}},
		{"global_attributes", wp25HostConfig{globalAttrs: true}},
		{"template_attributes", wp25HostConfig{templateAttrs: true}},
		{"all", wp25HostConfig{autocrlf: true, globalAttrs: true, templateAttrs: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wp25UseHostConfig(t, tc.host)
			s := newStore(t)
			if err := s.Init("r"); err != nil {
				t.Fatalf("init: %v", err)
			}
			for _, p := range wp25Pages {
				wp25CheckPage(t, s, p.path, []byte(p.body))
			}
			wp25CheckNoAttributes(t, s)
		})
	}
}

// wp25CheckPage writes body and checks that every read path returns it
// byte for byte, that an identical rewrite is a no-op on the same path sha,
// and that the LF variant of body is a change, not a no-op.
func wp25CheckPage(t *testing.T, s *Store, path string, body []byte) {
	t.Helper()
	res, conflict, err := s.WriteWithOptions("r", path, body, wp02Opts(""))
	if err != nil || conflict != nil {
		t.Errorf("write %s: err=%v conflict=%+v", path, err, conflict)
		return
	}
	sha := res.SHA
	wp25CheckReads(t, s, path, sha, body)

	again, conflict, err := s.WriteWithOptions("r", path, body, wp02Opts(sha))
	if err != nil || conflict != nil {
		t.Errorf("rewrite %s: err=%v conflict=%+v", path, err, conflict)
		return
	}
	if again.Changed || again.SHA != sha {
		t.Errorf("identical rewrite of %s = %+v, want {SHA:%s Changed:false}", path, again, sha)
	}
	if got, err := s.HeadSHAForPath("r", path); err != nil || got != sha {
		t.Errorf("HeadSHAForPath(%s) after the no-op = %s, %v; want %s", path, got, err, sha)
	}

	lf := bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	toLF, conflict, err := s.WriteWithOptions("r", path, lf, wp02Opts(sha))
	if err != nil || conflict != nil {
		t.Errorf("write LF variant of %s: err=%v conflict=%+v", path, err, conflict)
		return
	}
	if !toLF.Changed || toLF.SHA == sha {
		t.Errorf("LF variant of %s = %+v, want a new commit (CRLF -> LF is a change)", path, toLF)
	}
	wp25CheckReads(t, s, path, toLF.SHA, lf)

	// The first commit still holds the CRLF bytes.
	if got, err := s.ReadAtSha("r", path, sha); err != nil || !bytes.Equal(got, body) {
		t.Errorf("ReadAtSha(%s, %s) after the LF write = %q, %v; want %q", path, sha, got, err, body)
	}
}

// wp25CheckReads checks Read, ReadAtSha and ReadPageAt (full and abbreviated
// sha, the ?at=<sha> read) all return want for path at sha.
func wp25CheckReads(t *testing.T, s *Store, path, sha string, want []byte) {
	t.Helper()
	if got, gotSHA, err := s.Read("r", path); err != nil || !bytes.Equal(got, want) || gotSHA != sha {
		t.Errorf("Read(%s) = %q, %s, %v; want %q, %s", path, got, gotSHA, err, want, sha)
	}
	if got, err := s.ReadAtSha("r", path, sha); err != nil || !bytes.Equal(got, want) {
		t.Errorf("ReadAtSha(%s, %s) = %q, %v; want %q", path, sha, got, err, want)
	}
	for _, at := range []string{sha, sha[:7]} {
		got, pathSHA, err := s.ReadPageAt("r", path, at)
		if err != nil || !bytes.Equal(got, want) || pathSHA != sha {
			t.Errorf("ReadPageAt(%s, %s) = %q, %s, %v; want %q, %s", path, at, got, pathSHA, err, want, sha)
		}
	}
}

// wp25CheckNoAttributes checks that no gitattributes reach the data repo's
// git: none in its tree or working tree, no info/attributes from a template,
// and none from the host's attribute files as git itself reports them.
func wp25CheckNoAttributes(t *testing.T, s *Store) {
	t.Helper()
	dir := s.repoDir("r")
	for _, f := range []string{".gitattributes", filepath.Join(".git", "info", "attributes")} {
		if _, err := os.Lstat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("data repo has %s (lstat err=%v), want none", f, err)
		}
	}
	if out, err := s.git(dir, "ls-tree", "-r", "--name-only", "HEAD"); err != nil || strings.Contains(out, ".gitattributes") {
		t.Errorf("ls-tree HEAD = %q, %v; want no .gitattributes", out, err)
	}
	if out, err := s.git(dir, "check-attr", "-a", "--", wp25Pages[0].path); err != nil || out != "" {
		t.Errorf("check-attr -a %s = %q, %v; want no attributes", wp25Pages[0].path, out, err)
	}
	for key, want := range map[string]string{"core.autocrlf": "false", "core.safecrlf": "false", "core.eol": "lf"} {
		if out, err := s.git(dir, "config", "--get", key); err != nil || strings.TrimSpace(out) != want {
			t.Errorf("git config %s = %q, %v; want %q", key, out, err, want)
		}
	}
}

// wp25ForcedConfig are the -c settings gitCmd must put on every git call.
var wp25ForcedConfig = []string{
	"core.quotePath=false",
	"core.autocrlf=false",
	"core.eol=lf",
	"core.safecrlf=false",
	"core.attributesFile=" + os.DevNull,
	"init.templateDir=",
}

// wp25SplitGitArgs splits git's arguments into the leading "-c key=value"
// settings and the rest (the subcommand and its arguments).
func wp25SplitGitArgs(args []string) (settings, rest []string) {
	for len(args) >= 2 && args[0] == "-c" {
		settings = append(settings, args[1])
		args = args[2:]
	}
	return settings, args
}

// TestGitCmdForcesByteExactConfig pins gitCmd's arguments and environment:
// the conversion settings above, GIT_ATTR_NOSYSTEM=1 (the system
// gitattributes file cannot be redirected from a test, so this is checked
// here), and the existing LC_ALL=C and GIT_LITERAL_PATHSPECS=1, each winning
// over a host value.
func TestGitCmdForcesByteExactConfig(t *testing.T) {
	t.Setenv("GIT_ATTR_NOSYSTEM", "0")
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("GIT_LITERAL_PATHSPECS", "0")
	cmd := gitCmd(t.TempDir(), "status", "--short")
	settings, rest := wp25SplitGitArgs(cmd.Args[1:])
	for _, want := range wp25ForcedConfig {
		if !wp25Contains(settings, want) {
			t.Errorf("gitCmd settings %q lack -c %s", settings, want)
		}
	}
	if strings.Join(rest, " ") != "status --short" {
		t.Errorf("gitCmd args after the settings = %q, want the caller's [status --short]", rest)
	}
	env := map[string]string{}
	for _, kv := range cmd.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[strings.ToUpper(k)] = v
		}
	}
	for k, want := range map[string]string{"GIT_ATTR_NOSYSTEM": "1", "LC_ALL": "C", "GIT_LITERAL_PATHSPECS": "1"} {
		if got := env[k]; got != want {
			t.Errorf("gitCmd env %s=%q, want %q", k, got, want)
		}
	}
}

// TestEveryGitCallCarriesByteExactConfig logs the git processes behind a
// write, an unchanged rewrite and the reads: each carries every forced
// setting, and the unchanged rewrite runs no `git commit` (the check
// TestWriteNoopIsLocaleIndependent makes by argument position, which the
// extra settings move).
func TestEveryGitCallCarriesByteExactConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("logs git calls through a /bin/sh wrapper")
	}
	wp25UseHostConfig(t, wp25HostConfig{autocrlf: true, globalAttrs: true, templateAttrs: true})
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("look up git: %v", err)
	}
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls.log")
	// Each argument ends in NUL and each call in RS (0x1e), so arguments
	// keep their exact values (a commit message may hold spaces).
	wrapper := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\000' \"$a\"; done >> \"$WP25_GIT_CALLS\"\nprintf '\\036' >> \"$WP25_GIT_CALLS\"\nexec \"$WP25_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatalf("write git wrapper: %v", err)
	}
	t.Setenv("WP25_GIT_CALLS", calls)
	t.Setenv("WP25_REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := newStore(t)
	if err := s.Init("r"); err != nil {
		t.Fatalf("init: %v", err)
	}
	p := wp25Pages[0]
	res, conflict, err := s.WriteWithOptions("r", p.path, []byte(p.body), wp02Opts(""))
	if err != nil || conflict != nil {
		t.Fatalf("write: err=%v conflict=%+v", err, conflict)
	}
	wp25CheckReads(t, s, p.path, res.SHA, []byte(p.body))
	all := wp25ReadCalls(t, calls)
	if len(all) == 0 {
		t.Fatal("no git calls logged")
	}
	for _, args := range all {
		settings, _ := wp25SplitGitArgs(args)
		for _, want := range wp25ForcedConfig {
			if !wp25Contains(settings, want) {
				t.Errorf("git %q lacks -c %s", args, want)
			}
		}
	}

	if err := os.Remove(calls); err != nil {
		t.Fatalf("reset git call log: %v", err)
	}
	again, conflict, err := s.WriteWithOptions("r", p.path, []byte(p.body), wp02Opts(res.SHA))
	if err != nil || conflict != nil || again.Changed || again.SHA != res.SHA {
		t.Fatalf("rewrite = %+v, err=%v conflict=%+v; want {SHA:%s Changed:false}", again, err, conflict, res.SHA)
	}
	for _, args := range wp25ReadCalls(t, calls) {
		if _, rest := wp25SplitGitArgs(args); len(rest) > 0 && rest[0] == "commit" {
			t.Errorf("an unchanged write ran git %q", args)
		}
	}
}

// wp25ReadCalls parses the wrapper's log into one argument list per call.
func wp25ReadCalls(t *testing.T, calls string) [][]string {
	t.Helper()
	log, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("read git call log: %v", err)
	}
	var all [][]string
	for _, rec := range strings.Split(string(log), "\x1e") {
		if rec != "" {
			all = append(all, strings.Split(strings.TrimSuffix(rec, "\x00"), "\x00"))
		}
	}
	return all
}

func wp25Contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
