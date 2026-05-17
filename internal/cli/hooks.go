package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/config"
)

// claudeSettingsRel is the path (relative to the project root) where
// Claude Code reads project-level settings. We register the Stop hook
// here so capture only fires for projects that opted in.
const claudeSettingsRel = ".claude/settings.json"

// contexoHookMarker tags the hook entry we write so uninstall can find it
// without touching other hooks the user has configured.
const contexoHookMarker = "contexo-capture-turn"

// hookCommand is the shell snippet registered as the Stop-hook target.
// Using `ctx capture turn` (no flags) means Claude Code's stdin payload is
// the source of truth; the command exits 0 even when capture is disabled
// or the project isn't a Contexo project.
const hookCommand = "ctx capture turn"

func newHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Manage Claude Code Stop-hook integration for Contexo capture",
	}
	cmd.AddCommand(newHooksInstallCmd())
	cmd.AddCommand(newHooksUninstallCmd())
	cmd.AddCommand(newHooksStatusCmd())
	return cmd
}

func newHooksInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Register the Contexo capture Stop-hook in .claude/settings.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			return installHook(cmd, root)
		},
	}
}

func newHooksUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the Contexo capture Stop-hook from .claude/settings.json",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			return uninstallHook(cmd, root)
		},
	}
}

func newHooksStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the Contexo capture Stop-hook is currently installed",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			installed, err := hookInstalled(root)
			if err != nil {
				return err
			}
			if installed {
				fmt.Fprintln(cmd.OutOrStdout(), "Stop hook: installed")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Stop hook: not installed")
			}
			return nil
		},
	}
}

func installHook(cmd *cobra.Command, root string) error {
	if _, err := os.Stat(config.ContexoDirPath(root)); err != nil {
		return fmt.Errorf("hooks install: not a Contexo project (run 'ctx init' first)")
	}

	path := filepath.Join(root, filepath.FromSlash(claudeSettingsRel))
	settings, err := loadSettings(path)
	if err != nil {
		return err
	}

	if findContexoStopHook(settings) >= 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Stop hook already installed; nothing to do.")
		return nil
	}

	settings["hooks"] = upsertStopHook(settings["hooks"])
	if err := saveSettings(path, settings); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Installed Contexo capture Stop-hook in %s\n", claudeSettingsRel)
	fmt.Fprintln(cmd.OutOrStdout(), "Restart Claude Code (or open a new session) so the hook is picked up.")
	return nil
}

func uninstallHook(cmd *cobra.Command, root string) error {
	path := filepath.Join(root, filepath.FromSlash(claudeSettingsRel))
	settings, err := loadSettings(path)
	if err != nil {
		return err
	}

	hooksField, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		fmt.Fprintln(cmd.OutOrStdout(), "No hooks present; nothing to do.")
		return nil
	}
	stopList, _ := hooksField["Stop"].([]interface{})
	cleaned := make([]interface{}, 0, len(stopList))
	removed := 0
	for _, entry := range stopList {
		group, ok := entry.(map[string]interface{})
		if !ok {
			cleaned = append(cleaned, entry)
			continue
		}
		nestedRaw, _ := group["hooks"].([]interface{})
		filteredNested := make([]interface{}, 0, len(nestedRaw))
		for _, h := range nestedRaw {
			hMap, ok := h.(map[string]interface{})
			if !ok {
				filteredNested = append(filteredNested, h)
				continue
			}
			if marker, _ := hMap["_contexo"].(string); marker == contexoHookMarker {
				removed++
				continue
			}
			filteredNested = append(filteredNested, h)
		}
		if len(filteredNested) == 0 {
			continue // drop the whole group if we emptied it
		}
		group["hooks"] = filteredNested
		cleaned = append(cleaned, group)
	}
	if removed == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Stop hook not installed; nothing to do.")
		return nil
	}
	if len(cleaned) == 0 {
		delete(hooksField, "Stop")
	} else {
		hooksField["Stop"] = cleaned
	}
	if len(hooksField) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooksField
	}
	if err := saveSettings(path, settings); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Removed Contexo capture Stop-hook from %s\n", claudeSettingsRel)
	return nil
}

func hookInstalled(root string) (bool, error) {
	path := filepath.Join(root, filepath.FromSlash(claudeSettingsRel))
	settings, err := loadSettings(path)
	if err != nil {
		return false, err
	}
	return findContexoStopHook(settings) >= 0, nil
}

func loadSettings(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]interface{}{}, nil
		}
		return nil, fmt.Errorf("hooks: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]interface{}{}, nil
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("hooks: parse %s: %w", path, err)
	}
	if settings == nil {
		settings = map[string]interface{}{}
	}
	return settings, nil
}

func saveSettings(path string, settings map[string]interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("hooks: mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("hooks: marshal: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// findContexoStopHook returns the index (in the Stop list) of the first
// group containing our marker, or -1 if absent. The numeric index isn't
// used by callers; this is a "does it exist" probe.
func findContexoStopHook(settings map[string]interface{}) int {
	hooksField, ok := settings["hooks"].(map[string]interface{})
	if !ok {
		return -1
	}
	stopList, _ := hooksField["Stop"].([]interface{})
	for i, entry := range stopList {
		group, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		nested, _ := group["hooks"].([]interface{})
		for _, h := range nested {
			hMap, ok := h.(map[string]interface{})
			if !ok {
				continue
			}
			if marker, _ := hMap["_contexo"].(string); marker == contexoHookMarker {
				return i
			}
		}
	}
	return -1
}

// upsertStopHook appends the Contexo Stop-hook group to whatever hooks
// configuration the user already had, preserving every other entry.
func upsertStopHook(existing interface{}) map[string]interface{} {
	hooksField, _ := existing.(map[string]interface{})
	if hooksField == nil {
		hooksField = map[string]interface{}{}
	}
	stopList, _ := hooksField["Stop"].([]interface{})

	group := map[string]interface{}{
		"matcher": "",
		"hooks": []interface{}{
			map[string]interface{}{
				"type":     "command",
				"command":  hookCommand,
				"_contexo": contexoHookMarker,
			},
		},
	}
	hooksField["Stop"] = append(stopList, group)
	return hooksField
}
