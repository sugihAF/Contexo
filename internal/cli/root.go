package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

var rootDir string

// NewRootCmd creates the root ctx command.
func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "ctx",
		Short:        "CtxHub CLI — AI development context management",
		Long:         "ctx captures, stores, and retrieves AI-assisted development context.",
		SilenceUsage: true,
	}

	cmd.PersistentFlags().StringVar(&rootDir, "root", "", "project root directory (default: current directory)")

	cmd.AddCommand(NewInitCmd())
	cmd.AddCommand(newCaptureCmd())
	cmd.AddCommand(newSessionCmd())
	cmd.AddCommand(newCommitCmd())
	cmd.AddCommand(newLogCmd())
	cmd.AddCommand(newShowCmd())
	cmd.AddCommand(newLinkCmd())
	cmd.AddCommand(newContextCmd())
	cmd.AddCommand(newBlameCmd())
	cmd.AddCommand(newPushCmd())
	cmd.AddCommand(newPullCmd())
	cmd.AddCommand(newMCPCmd())
	cmd.AddCommand(newRemoteCmd())
	cmd.AddCommand(newAuthCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newConfigCmd())
	cmd.AddCommand(newOpenSessionCmd())
	cmd.AddCommand(newCodexCmd())

	return cmd
}

// GetRootDir returns the project root directory.
func GetRootDir() string {
	if rootDir != "" {
		return rootDir
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot get working directory: %v\n", err)
		return "."
	}
	return dir
}

// shortID safely truncates an ID to 8 characters for display.
func shortID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

// openDB opens the SQLite database in the .ctx directory.
func openDB(root string) (*sqlitestore.DB, error) {
	ctxDir := filepath.Join(root, ".ctx")
	db, err := sqlitestore.Open(filepath.Join(ctxDir, "index.sqlite"))
	if err != nil {
		return nil, err
	}
	return db, nil
}
