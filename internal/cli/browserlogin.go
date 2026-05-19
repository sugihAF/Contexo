package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/pkg/browser"
)

// browserLoginTimeout caps how long we wait for the user to complete the
// dashboard flow. Long enough for a Google sign-in dance + tab confusion,
// short enough not to leak a forgotten loopback listener forever.
const browserLoginTimeout = 5 * time.Minute

// BrowserLoginResult is what the loopback callback hands back when the
// dashboard finishes minting a PAT. Name and Email are populated from
// the dashboard's authenticated session (Google identity) and may be
// empty if that information wasn't available.
type BrowserLoginResult struct {
	Token string
	Name  string
	Email string
}

// runBrowserLogin drives the loopback-redirect login flow:
//
//   1. Listen on a random 127.0.0.1 port.
//   2. Open the dashboard's /cli-login page with port + CSRF state + hostname.
//   3. Block until the dashboard posts the token (and identity) back,
//      the user Ctrl-Cs, or the timeout elapses.
//
// On success the minted PAT + the user's name/email (from the dashboard's
// Google session) are returned. The loopback server is shut down before
// returning either way.
func runBrowserLogin(ctx context.Context, dashboardURL string, out io.Writer) (BrowserLoginResult, error) {
	var zero BrowserLoginResult
	state, err := randomState(16)
	if err != nil {
		return zero, fmt.Errorf("browser login: generate state: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return zero, fmt.Errorf("browser login: open loopback: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	type chRes struct {
		res BrowserLoginResult
		err error
	}
	resultCh := make(chan chRes, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gotState := q.Get("state")
		token := q.Get("token")

		if gotState == "" || gotState != state {
			writeFailureHTML(w, "state mismatch")
			resultCh <- chRes{err: errors.New("browser login: state mismatch (possible CSRF) — please re-run")}
			return
		}
		if !strings.HasPrefix(token, "ctxp_") {
			writeFailureHTML(w, "missing or malformed token")
			resultCh <- chRes{err: errors.New("browser login: callback did not include a CLI personal access token")}
			return
		}
		writeSuccessHTML(w)
		resultCh <- chRes{res: BrowserLoginResult{
			Token: token,
			Name:  strings.TrimSpace(q.Get("name")),
			Email: strings.TrimSpace(q.Get("email")),
		}}
	})
	// Anything other than /callback gets a soft 404. Useful when the user
	// hits /favicon.ico from the success page.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		_ = server.Serve(listener)
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	dashURL, err := buildCliLoginURL(dashboardURL, port, state)
	if err != nil {
		return zero, err
	}

	fmt.Fprintf(out, "Opening %s in your browser...\n", dashURL)
	fmt.Fprintf(out, "If it doesn't open automatically, paste that URL into your browser.\n")
	if err := browser.OpenURL(dashURL); err != nil {
		fmt.Fprintf(out, "(could not open browser automatically: %v)\n", err)
	}

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case res := <-resultCh:
		return res.res, res.err
	case <-time.After(browserLoginTimeout):
		return zero, fmt.Errorf("browser login: timed out after %s — re-run, or use --no-browser to paste a token manually", browserLoginTimeout)
	}
}

// buildCliLoginURL constructs the dashboard URL the user is sent to. The
// hostname query param is informational only; the dashboard surfaces it
// so the user can verify "yes, that's the machine I'm sitting at."
func buildCliLoginURL(dashboardURL string, port int, state string) (string, error) {
	base := strings.TrimRight(dashboardURL, "/")
	u, err := url.Parse(base + "/cli-login")
	if err != nil {
		return "", fmt.Errorf("browser login: bad dashboard URL %q: %w", dashboardURL, err)
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	q := u.Query()
	q.Set("port", fmt.Sprint(port))
	q.Set("state", state)
	q.Set("hostname", host)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func randomState(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeSuccessHTML(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(successHTML))
}

func writeFailureHTML(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	body := strings.ReplaceAll(failureHTML, "{{REASON}}", reason)
	_, _ = w.Write([]byte(body))
}

const successHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Contexo CLI authorized</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
           display: flex; align-items: center; justify-content: center; min-height: 100vh;
           margin: 0; background: #f7f8fa; color: #1a202c; }
    .card { background: white; padding: 2.5rem 3rem; border-radius: 12px;
            box-shadow: 0 2px 16px rgba(0,0,0,0.08); text-align: center; max-width: 440px; }
    h1 { margin: 0 0 0.75rem; font-size: 1.35rem; }
    p { color: #4a5568; margin: 0; }
    .check { color: #38a169; font-size: 2rem; line-height: 1; margin-bottom: 0.5rem; }
  </style>
</head>
<body>
  <div class="card">
    <div class="check">&check;</div>
    <h1>CLI authorized</h1>
    <p>You can close this tab and return to your terminal.</p>
  </div>
</body>
</html>
`

const failureHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Contexo CLI login failed</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
           display: flex; align-items: center; justify-content: center; min-height: 100vh;
           margin: 0; background: #f7f8fa; color: #1a202c; }
    .card { background: white; padding: 2.5rem 3rem; border-radius: 12px;
            box-shadow: 0 2px 16px rgba(0,0,0,0.08); text-align: center; max-width: 440px;
            border-left: 4px solid #e53e3e; }
    h1 { margin: 0 0 0.75rem; font-size: 1.2rem; color: #c53030; }
    p { color: #4a5568; margin: 0; font-size: 0.9rem; }
  </style>
</head>
<body>
  <div class="card">
    <h1>CLI login failed</h1>
    <p>{{REASON}}. Return to your terminal and re-run <code>ctx login</code>.</p>
  </div>
</body>
</html>
`
