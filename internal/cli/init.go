package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
)

// NewInitCmd creates the ctx init command.
func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize .contexo knowledge directory",
		Long: "Creates a .contexo/ tree in the current project for storing AI " +
			"knowledge pages. Idempotent — re-running leaves existing pages alone.",
		RunE: runInit,
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	root := GetRootDir()
	hubDir := config.ContexoDirPath(root)

	dirs := []string{
		hubDir,
		filepath.Join(hubDir, "wiki", "concepts"),
		filepath.Join(hubDir, "wiki", "entities"),
		filepath.Join(hubDir, "wiki", "analyses"),
		filepath.Join(hubDir, "raw", "sessions"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("init: create %s: %w", d, err)
		}
	}

	// Write config.json (don't overwrite if exists)
	cfgPath := config.ContexoConfigPath(root)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := config.Save(root, config.DefaultConfig()); err != nil {
			return fmt.Errorf("init: save config: %w", err)
		}
	}

	// Seed index.md and tags.md if they don't exist yet
	if err := writeIfAbsent(filepath.Join(hubDir, "index.md"), seedIndex); err != nil {
		return err
	}
	if err := writeIfAbsent(filepath.Join(hubDir, "tags.md"), seedTags); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Initialized .contexo in %s\n", root)
	return nil
}

func writeIfAbsent(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

const seedIndex = `# Knowledge Index

Always-loaded index for this project's Contexo knowledge base. Find what's
relevant here, then read individual pages on demand.

## Concepts
(none yet)

## Entities
(none yet)

## Analyses
(none yet)

## Sources
(none yet)
`

const seedTags = `# Tags

(none yet)
`
