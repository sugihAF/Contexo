package syncops

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sugihAF/contexo/internal/capture"
	"github.com/sugihAF/contexo/internal/schema"
	"github.com/sugihAF/contexo/internal/syncops/contract"
	"github.com/sugihAF/contexo/internal/syncstate"
)

// Owner: WP-1.2. Fields may be added, never renamed or removed.

// GuardResult is the D4 distill-guard verdict for a batch. Bypassed is true
// when CONTEXO_DISTILL_DISABLE=1 is set (E24); it never clears Required.
type GuardResult struct {
	Required bool
	Details  *contract.DistillRequiredDetails
	Bypassed bool
}

// guardFreshWindow is how recent a capture buffer must be to count as a
// pending session (the MCP handshake's 6h window, tools.go).
const guardFreshWindow = 6 * time.Hour

// guardDisableEnv turns the guard into a recorded bypass (tools.go parity).
const guardDisableEnv = "CONTEXO_DISTILL_DISABLE"

// CheckGuard evaluates the D4 distill guard over batch (the modified ∪ new
// paths about to be pushed). Required iff some batch page's local
// frontmatter type is concept or analysis AND some non-archived capture
// buffer has at least one turn (a non-blank line) and an mtime within
// guardFreshWindow of env.Now(). Every buffer is scanned, not just the most
// recent. Details (only when Required) lists the knowledge pages sorted,
// the fresh sessions newest first and the agent prompt. Bypassed reports
// CONTEXO_DISTILL_DISABLE=1 and is always set. A missing or unparsable batch
// file is not knowledge; a batch path failing ValidateRelPath is an error.
// No network, no lock, no writes.
func CheckGuard(env Env, batch []string) (*GuardResult, error) {
	res := &GuardResult{Bypassed: os.Getenv(guardDisableEnv) == "1"}
	pages, err := guardKnowledgePages(env.HubDir, batch)
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return res, nil
	}
	sessions, err := guardFreshSessions(env)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return res, nil
	}
	res.Required = true
	res.Details = &contract.DistillRequiredDetails{
		Pages:       pages,
		Sessions:    sessions,
		AgentPrompt: guardAgentPrompt(pages),
	}
	return res, nil
}

// guardKnowledgeType mirrors the MCP handshake's hasKnowledgePage: concept
// and analysis pages carry reasoning worth distilling.
func guardKnowledgeType(t schema.PageType) bool {
	return t == schema.TypeConcept || t == schema.TypeAnalysis
}

// guardKnowledgePages returns the batch paths whose local file parses as a
// concept or analysis page, deduplicated and sorted.
func guardKnowledgePages(hub string, batch []string) ([]string, error) {
	seen := map[string]bool{}
	pages := []string{}
	for _, rel := range batch {
		if err := syncstate.ValidateRelPath(rel); err != nil {
			return nil, Wrap(err, contract.CodeInvalidPath, "distill guard")
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		data, err := os.ReadFile(filepath.Join(hub, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, Wrap(err, contract.CodeInternal, "distill guard: read "+rel)
		}
		p, err := schema.ParsePage(syncstate.StripBOM(data))
		if err != nil || !guardKnowledgeType(p.Frontmatter.Type) {
			continue
		}
		pages = append(pages, rel)
	}
	sort.Strings(pages)
	return pages, nil
}

// guardSession is a fresh buffer with its full mtime (for ordering).
type guardSession struct {
	s     contract.CaptureSession
	mtime time.Time
}

// guardFreshSessions returns every pending buffer with at least one turn
// and an mtime within guardFreshWindow, newest first (ties by session id).
func guardFreshSessions(env Env) ([]contract.CaptureSession, error) {
	bufs, err := capture.List(env.HubDir)
	if err != nil {
		return nil, Wrap(err, contract.CodeInternal, "distill guard")
	}
	now := time.Now
	if env.Now != nil {
		now = env.Now
	}
	cutoff := now().Add(-guardFreshWindow)
	var fresh []guardSession
	for _, b := range bufs {
		data, mtime, ok, err := guardReadBuffer(b.Path())
		if err != nil {
			return nil, Wrap(err, contract.CodeInternal, "distill guard")
		}
		if !ok || !mtime.After(cutoff) {
			continue
		}
		turns := guardTurns(data)
		if turns < 1 {
			continue
		}
		fresh = append(fresh, guardSession{
			s: contract.CaptureSession{
				SessionID: b.SessionID,
				Turns:     turns,
				UpdatedAt: contract.TimeOf(mtime),
				Fresh:     true,
			},
			mtime: mtime,
		})
	}
	sort.SliceStable(fresh, func(i, j int) bool {
		if !fresh[i].mtime.Equal(fresh[j].mtime) {
			return fresh[i].mtime.After(fresh[j].mtime)
		}
		return fresh[i].s.SessionID < fresh[j].s.SessionID
	})
	out := make([]contract.CaptureSession, len(fresh))
	for i, f := range fresh {
		out[i] = f.s
	}
	return out, nil
}

// guardReadBuffer reads a buffer file and its mtime. ok is false when the
// file vanished since capture.List (archived concurrently).
func guardReadBuffer(path string) (data []byte, mtime time.Time, ok bool, err error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	data, err = os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	return data, fi.ModTime(), true, nil
}

// guardTurns counts the non-blank lines of a buffer (R12).
func guardTurns(data []byte) int {
	n := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}

// guardAgentPrompt is the text the extension offers to copy into the agent
// chat when the guard refuses a push.
func guardAgentPrompt(pages []string) string {
	return fmt.Sprintf("Push my pending Contexo changes with the ctx_push tool (%s). "+
		"Distill the pending session into a source page first when ctx_push asks you to.",
		strings.Join(pages, ", "))
}
