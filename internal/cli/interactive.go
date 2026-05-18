package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/AlecAivazis/survey/v2/terminal"
	"golang.org/x/term"

	"github.com/sugihAF/contexo/internal/sync"
)

// stdinIsTTY reports whether the process's stdin is attached to a terminal.
// Used to decide whether interactive prompts (pickers) are appropriate, or
// whether we should fall back to a clear error for non-interactive callers
// (CI, pipes, redirected stdin) to avoid hanging.
func stdinIsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// repoOptionLabel renders one repo entry for the picker. Role is right-
// padded to a fixed column so multi-entry lists line up nicely.
func repoOptionLabel(opt sync.RepoOption, idWidth int) string {
	id := opt.ID
	if len(id) < idWidth {
		id = id + strings.Repeat(" ", idWidth-len(id))
	}
	role := opt.Role
	if role == "" {
		role = "member"
	}
	return fmt.Sprintf("%s  (%s)", id, role)
}

// fetchRepoOptions calls the server, returns the raw options plus pre-
// formatted display labels. Pure I/O wrapper around sync.Client.ListRepos
// kept separate from the picker so tests don't have to exercise survey.
func fetchRepoOptions(client *sync.Client) ([]sync.RepoOption, []string, error) {
	opts, err := client.ListRepos()
	if err != nil {
		return nil, nil, err
	}
	idWidth := 0
	for _, o := range opts {
		if len(o.ID) > idWidth {
			idWidth = len(o.ID)
		}
	}
	labels := make([]string, len(opts))
	for i, o := range opts {
		labels[i] = repoOptionLabel(o, idWidth)
	}
	return opts, labels, nil
}

// selectRepoInteractive presents a picker of the user's repos and returns
// the chosen ID. Errors with a friendly onboarding hint when the user is
// a member of zero repos. The caller is responsible for ensuring stdin is
// a TTY before invoking this.
func selectRepoInteractive(client *sync.Client, out io.Writer) (string, error) {
	opts, labels, err := fetchRepoOptions(client)
	if err != nil {
		return "", err
	}
	if len(opts) == 0 {
		fmt.Fprintln(out,
			"You're not a member of any repos yet.\n"+
				"Sign in to https://contexo-web.pages.dev to join a repo (or\n"+
				"ask the owner to mint an invite key and run 'ctx join <key>'),\n"+
				"then re-run.")
		return "", errors.New("no repos available")
	}

	var pick string
	prompt := &survey.Select{
		Message: "Pick a repo:",
		Options: labels,
	}
	if err := survey.AskOne(prompt, &pick); err != nil {
		if errors.Is(err, terminal.InterruptErr) {
			return "", errors.New("cancelled")
		}
		return "", fmt.Errorf("repo picker: %w", err)
	}
	for i, label := range labels {
		if label == pick {
			return opts[i].ID, nil
		}
	}
	return "", fmt.Errorf("picker returned unexpected label %q", pick)
}
