package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
	boltdbstore "github.com/sugihAF/contexo/internal/store/boltdb"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

// NewInitCmd creates the ctx init command.
func NewInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize .ctx directory structure",
		RunE:  runInit,
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	root := GetRootDir()
	ctxDir := config.CtxDirPath(root)

	// Create directory structure
	dirs := []string{
		ctxDir,
		filepath.Join(ctxDir, "sessions"),
		filepath.Join(ctxDir, "commits"),
		filepath.Join(ctxDir, "blobs"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("init: create %s: %w", d, err)
		}
	}

	// Write config.json (don't overwrite if exists)
	cfgPath := config.ConfigPath(root)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		cfg := config.DefaultConfig()
		if err := config.Save(root, cfg); err != nil {
			return fmt.Errorf("init: save config: %w", err)
		}
	}

	// Create SQLite DB
	dbPath := filepath.Join(ctxDir, "index.sqlite")
	db, err := sqlitestore.Open(dbPath)
	if err != nil {
		return fmt.Errorf("init: open sqlite: %w", err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		return fmt.Errorf("init: migrate sqlite: %w", err)
	}
	db.Close()

	// Create BoltDB
	boltPath := filepath.Join(ctxDir, "blobs.db")
	blobDir := filepath.Join(ctxDir, "blobs")
	bs, err := boltdbstore.New(boltPath, blobDir)
	if err != nil {
		return fmt.Errorf("init: open boltdb: %w", err)
	}
	bs.Close()

	fmt.Fprintf(cmd.OutOrStdout(), "Initialized .ctx in %s\n", root)
	return nil
}
