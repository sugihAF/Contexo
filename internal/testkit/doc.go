// Package testkit provides end-to-end test harnesses: a real Contexo server
// on a loopback port and a real .contexo project wired to it. It is for
// tests only; nothing in a shipped binary imports it.
//
// # Server (server.go, WP-0.2)
//
// StartServer runs the real server.NewRouter, wired like app.Run, over a
// temporary gitstore behind httptest. Auth is the legacy shared key
// ("testkit-key" by default, or WithKey) with no user store, so every
// request authenticates as the legacy admin. The returned Server carries:
//
//   - URL: the base URL, without a trailing slash
//   - Key: send "Authorization: Bearer " + Key on every request
//   - Store: the server's own gitstore, for seeding or inspecting repos
//     without going through HTTP
//
// StartOldServer runs the same server behind a wrapper that emulates a
// server from before contract v1: GET /health answers {"status":"ok"},
// the "at" query parameter is ignored (HEAD content is served with 200),
// "expect_absent" is ignored on push, and no X-Contexo-At,
// X-Contexo-Capabilities or X-Contexo-Server-Version header is sent. Use it
// to test a client's fallback for servers it cannot rely on.
//
// # Project (project.go, WP-0.4)
//
// NewProject writes a temporary .contexo project (config, index, tags, the
// wiki directories and project credentials) with the pages given as
// PageSpec values; PageBytes renders one valid page. Pair it with a server:
//
//	s := testkit.StartServer(t)
//	p := testkit.NewProject(t, testkit.ProjectOptions{
//		ServerURL: s.URL,
//		Key:       s.Key,
//		RepoID:    "demo",
//	})
//
// # Constraints
//
// StartServer, StartOldServer and NewProject call t.Setenv, so a test that
// uses them must not call t.Parallel (t.Setenv panics there). Tests under
// internal/server/... must not import testkit: testkit imports
// internal/server, so that would be an import cycle.
package testkit
