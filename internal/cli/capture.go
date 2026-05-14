package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sugihAF/contexo/internal/adapter/claudecode"
	"github.com/sugihAF/contexo/internal/config"
	"github.com/sugihAF/contexo/internal/recorder"
	boltdbstore "github.com/sugihAF/contexo/internal/store/boltdb"
	sqlitestore "github.com/sugihAF/contexo/internal/store/sqlite"
)

// CaptureState tracks the capture status for the CLI.
type CaptureState struct {
	Active   bool     `json:"active"`
	Paused   bool     `json:"paused"`
	Port     int      `json:"port"`
	Adapters []string `json:"adapters"`
	PID      int      `json:"pid,omitempty"`
}

func newCaptureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Control event capture",
	}

	cmd.AddCommand(newCaptureOnCmd())
	cmd.AddCommand(newCaptureOffCmd())
	cmd.AddCommand(newCapturePauseCmd())
	cmd.AddCommand(newCaptureResumeCmd())
	cmd.AddCommand(newCaptureStatusCmd())

	return cmd
}

func newCaptureOnCmd() *cobra.Command {
	var client string

	cmd := &cobra.Command{
		Use:   "on",
		Short: "Start event capture",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			ctxDir := config.CtxDirPath(root)

			cfg, err := config.Load(root)
			if err != nil {
				return err
			}

			if client == "" {
				client = cfg.DefaultClient
			}

			// Open stores
			db, err := sqlitestore.Open(filepath.Join(ctxDir, "index.sqlite"))
			if err != nil {
				return fmt.Errorf("capture: open sqlite: %w", err)
			}
			if err := db.Migrate(); err != nil {
				db.Close()
				return fmt.Errorf("capture: migrate: %w", err)
			}

			blobs, err := boltdbstore.New(
				filepath.Join(ctxDir, "blobs.db"),
				filepath.Join(ctxDir, "blobs"),
			)
			if err != nil {
				db.Close()
				return fmt.Errorf("capture: open boltdb: %w", err)
			}

			// Create recorder and HTTP server
			rec := recorder.New(ctxDir, db, blobs)
			srv := recorder.NewHTTPServer(rec, cfg.RecorderPort)

			if err := srv.Start(); err != nil {
				db.Close()
				blobs.Close()
				return fmt.Errorf("capture: start server: %w", err)
			}

			// Use actual port from listener (important when config port is 0)
			actualPort := cfg.RecorderPort
			if addr := srv.Addr(); addr != "" {
				if _, p, perr := parseHostPort(addr); perr == nil {
					actualPort = p
				}
			}

			// Write hooks config for the client
			if client == "claude_code" || client == "claude-code" {
				hooksData, err := claudecode.GenerateHooksConfig(actualPort)
				if err != nil {
					return fmt.Errorf("capture: generate hooks: %w", err)
				}
				hooksPath := filepath.Join(ctxDir, "hooks.json")
				if err := os.WriteFile(hooksPath, hooksData, 0o644); err != nil {
					return fmt.Errorf("capture: write hooks: %w", err)
				}
			}

			// Save state
			state := CaptureState{
				Active:   true,
				Port:     actualPort,
				Adapters: []string{client},
				PID:      os.Getpid(),
			}
			if err := saveCaptureState(ctxDir, &state); err != nil {
				return err
			}

			// Write PID file
			pidPath := filepath.Join(ctxDir, "recorder.pid")
			os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644)

			fmt.Fprintf(cmd.OutOrStdout(), "Capture started on port %d (client: %s)\n", actualPort, client)
			fmt.Fprintf(cmd.OutOrStdout(), "Listening at http://127.0.0.1:%d\n", actualPort)

			// Block until SIGINT/SIGTERM to keep the server alive
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			<-sigCh

			fmt.Fprintln(cmd.OutOrStdout(), "\nShutting down recorder...")
			srv.Stop()
			db.Close()
			blobs.Close()

			// Update state
			state.Active = false
			saveCaptureState(ctxDir, &state)
			os.Remove(pidPath)

			fmt.Fprintln(cmd.OutOrStdout(), "Capture stopped")
			return nil
		},
	}

	cmd.Flags().StringVar(&client, "client", "", "AI client to capture (claude-code, codex)")
	return cmd
}

func newCaptureOffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "off",
		Short: "Stop event capture",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			ctxDir := config.CtxDirPath(root)

			state := CaptureState{Active: false}
			if err := saveCaptureState(ctxDir, &state); err != nil {
				return err
			}

			// Remove PID file
			os.Remove(filepath.Join(ctxDir, "recorder.pid"))

			fmt.Fprintln(cmd.OutOrStdout(), "Capture stopped")
			return nil
		},
	}
}

func newCapturePauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause event capture (events are dropped)",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			ctxDir := config.CtxDirPath(root)

			state, err := loadCaptureState(ctxDir)
			if err != nil {
				return err
			}
			state.Paused = true
			if err := saveCaptureState(ctxDir, state); err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Capture paused (events will be dropped)")
			return nil
		},
	}
}

func newCaptureResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume event capture",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			ctxDir := config.CtxDirPath(root)

			state, err := loadCaptureState(ctxDir)
			if err != nil {
				return err
			}
			state.Paused = false
			if err := saveCaptureState(ctxDir, state); err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Capture resumed")
			return nil
		},
	}
}

func newCaptureStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show capture status",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetRootDir()
			ctxDir := config.CtxDirPath(root)

			state, err := loadCaptureState(ctxDir)
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "Status: inactive")
				return nil
			}

			if !state.Active {
				fmt.Fprintln(cmd.OutOrStdout(), "Status: inactive")
				return nil
			}

			status := "active"
			if state.Paused {
				status = "paused"
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n", status)
			fmt.Fprintf(cmd.OutOrStdout(), "Port: %d\n", state.Port)
			fmt.Fprintf(cmd.OutOrStdout(), "Adapters: %v\n", state.Adapters)
			return nil
		},
	}
}

func saveCaptureState(ctxDir string, state *CaptureState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(ctxDir, "capture_state.json"), data, 0o644)
}

func loadCaptureState(ctxDir string) (*CaptureState, error) {
	data, err := os.ReadFile(filepath.Join(ctxDir, "capture_state.json"))
	if err != nil {
		return &CaptureState{}, err
	}
	var state CaptureState
	if err := json.Unmarshal(data, &state); err != nil {
		return &CaptureState{}, err
	}
	return &state, nil
}

func parseHostPort(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	return host, port, err
}
