package e2e

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// The golden redactor works on raw bytes with regular expressions, never
// through a JSON round-trip, so key order and indentation (the wire
// contract) survive. Every replacement keeps the value's format: a sha stays
// 40-hex, a time stays a whole-second UTC RFC 3339 string, a path stays
// absolute. Unit tests: redact_test.go.

const (
	redactedURL  = "http://contexo-e2e.invalid"
	redactedHost = "contexo-e2e.invalid"
	redactedTime = "2000-01-01T00:00:00Z"
	redactedPID  = "4242"
	redactedNode = "e2e-host"
	redactedOS   = "e2e-os"
	redactedArch = "e2e-arch"
)

var (
	reSHA        = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	reTime       = regexp.MustCompile(`\b\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ\b`)
	reBackupTS   = regexp.MustCompile(`(\.sync/backup/[^"\s]*?\.)\d{8}T\d{6}Z`)
	rePIDKey     = regexp.MustCompile(`("pid"\s*:\s*)\d+`)
	rePIDText    = regexp.MustCompile(`\bpid \d+`)
	reHostKey    = regexp.MustCompile(`("host"\s*:\s*)"[^"]*"`)
	reWinConnect = regexp.MustCompile(`connectex: No connection could be made because the target machine actively refused it\.`)
)

// redactRoot is an absolute path written to the goldens as /e2e/<user>.
type redactRoot struct {
	path, user string
}

// redactor redacts one golden file. Sha placeholders are numbered per call
// of redact, by first appearance.
type redactor struct {
	roots  []redactRoot
	urls   []string
	host   string // lock host name, redacted to e2e-host
	goos   string // VersionDoc os, redacted to e2e-os
	goarch string // VersionDoc arch, redacted to e2e-arch
}

func newRedactor() *redactor {
	h, _ := os.Hostname()
	return &redactor{host: h, goos: runtime.GOOS, goarch: runtime.GOARCH}
}

// addRoot registers an absolute path (a project root or a HOME) that is
// written as /e2e/<user>, raw or JSON-escaped, with the separators of any
// path below it turned into '/'.
func (r *redactor) addRoot(path, user string) {
	if path != "" {
		r.roots = append(r.roots, redactRoot{path, user})
	}
}

// addURL registers a server URL (http://host:port) that is written as
// http://contexo-e2e.invalid; its bare host:port becomes contexo-e2e.invalid.
func (r *redactor) addURL(u string) {
	if u = strings.TrimSuffix(u, "/"); u != "" {
		r.urls = append(r.urls, u)
	}
}

func (r *redactor) redact(in []byte) []byte {
	b := append([]byte(nil), in...)
	b = r.redactRoots(b)
	b = r.redactURLs(b)
	b = reWinConnect.ReplaceAll(b, []byte("connect: connection refused"))
	b = reBackupTS.ReplaceAll(b, []byte("${1}20000101T000000Z"))
	b = reTime.ReplaceAll(b, []byte(redactedTime))
	b = rePIDKey.ReplaceAll(b, []byte("${1}"+redactedPID))
	b = rePIDText.ReplaceAll(b, []byte("pid "+redactedPID))
	b = reHostKey.ReplaceAll(b, []byte(`${1}"`+redactedNode+`"`))
	if r.host != "" && r.host != redactedNode {
		b = replaceBounded(b, r.host, redactedNode, isHostByte)
	}
	if r.goos != "" {
		b = regexp.MustCompile(`("os"\s*:\s*)"`+regexp.QuoteMeta(r.goos)+`"`).
			ReplaceAll(b, []byte(`${1}"`+redactedOS+`"`))
	}
	if r.goarch != "" {
		b = regexp.MustCompile(`("arch"\s*:\s*)"`+regexp.QuoteMeta(r.goarch)+`"`).
			ReplaceAll(b, []byte(`${1}"`+redactedArch+`"`))
	}
	seen := map[string]string{}
	b = reSHA.ReplaceAllFunc(b, func(m []byte) []byte {
		k := string(m)
		if p, ok := seen[k]; ok {
			return []byte(p)
		}
		p := fmt.Sprintf("%040x", len(seen)+1)
		seen[k] = p
		return []byte(p)
	})
	return b
}

// redactRoots replaces every registered root, longest first, in its
// JSON-escaped and its raw form.
func (r *redactor) redactRoots(b []byte) []byte {
	roots := append([]redactRoot(nil), r.roots...)
	sort.SliceStable(roots, func(i, j int) bool { return len(roots[i].path) > len(roots[j].path) })
	for _, rt := range roots {
		escaped := strings.ReplaceAll(rt.path, `\`, `\\`)
		b = replaceRoot(b, escaped, `\\`, "/e2e/"+rt.user)
		if escaped != rt.path {
			b = replaceRoot(b, rt.path, `\`, "/e2e/"+rt.user)
		}
	}
	return b
}

// replaceRoot replaces each bounded occurrence of root with repl, followed
// by the path segments below it joined with '/' (sep or '/' separated).
func replaceRoot(b []byte, root, sep, repl string) []byte {
	var out bytes.Buffer
	pat := []byte(root)
	for {
		i := bytes.Index(b, pat)
		if i < 0 {
			out.Write(b)
			return out.Bytes()
		}
		end := i + len(pat)
		if end < len(b) && isSegmentByte(b[end]) {
			out.Write(b[:end])
			b = b[end:]
			continue
		}
		out.Write(b[:i])
		out.WriteString(repl)
		rest := b[end:]
		for {
			var n int
			switch {
			case bytes.HasPrefix(rest, []byte(sep)):
				n = len(sep)
			case bytes.HasPrefix(rest, []byte("/")):
				n = 1
			default:
				n = 0
			}
			if n == 0 {
				break
			}
			seg := 0
			for n+seg < len(rest) && isSegmentByte(rest[n+seg]) {
				seg++
			}
			if seg == 0 {
				break
			}
			out.WriteByte('/')
			out.Write(rest[n : n+seg])
			rest = rest[n+seg:]
		}
		b = rest
	}
}

// isSegmentByte reports whether c can continue a path segment.
func isSegmentByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return c == '.' || c == '-' || c == '_' || c == '~' || c == '+' || c >= 0x80
}

func isHostByte(c byte) bool {
	return c == '.' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// redactURLs replaces each registered URL, longest first, then its bare
// host:port.
func (r *redactor) redactURLs(b []byte) []byte {
	urls := append([]string(nil), r.urls...)
	sort.SliceStable(urls, func(i, j int) bool { return len(urls[i]) > len(urls[j]) })
	isPort := func(c byte) bool { return c >= '0' && c <= '9' }
	for _, u := range urls {
		b = replaceBounded(b, u, redactedURL, isPort)
	}
	for _, u := range urls {
		host := u
		if i := strings.Index(host, "://"); i >= 0 {
			host = host[i+3:]
		}
		b = replaceBounded(b, host, redactedHost, isPort)
	}
	return b
}

// replaceBounded replaces old with repl where old is neither preceded nor
// followed by a byte for which cont reports true.
func replaceBounded(b []byte, old, repl string, cont func(byte) bool) []byte {
	if old == "" {
		return b
	}
	var out bytes.Buffer
	pat := []byte(old)
	for {
		i := bytes.Index(b, pat)
		if i < 0 {
			out.Write(b)
			return out.Bytes()
		}
		end := i + len(pat)
		before := i > 0 && cont(b[i-1]) && cont(pat[0])
		after := end < len(b) && cont(b[end])
		if before || after {
			out.Write(b[:end])
		} else {
			out.Write(b[:i])
			out.WriteString(repl)
		}
		b = b[end:]
	}
}
