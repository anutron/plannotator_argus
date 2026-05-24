package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/anutron/plannotator_argus/internal/config"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report whether the plannotator-argus daemon is running",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Default()
			if err != nil {
				return err
			}
			_ = cfg.LoadFromEnv()
			pidPath := cfg.PIDPath()
			info, err := os.Stat(pidPath)
			if errors.Is(err, os.ErrNotExist) {
				fmt.Println("not running")
				return nil
			}
			if err != nil {
				return fmt.Errorf("stat pidfile %s: %w", pidPath, err)
			}
			raw, err := os.ReadFile(pidPath)
			if err != nil {
				return fmt.Errorf("read pidfile %s: %w", pidPath, err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				return fmt.Errorf("invalid pidfile %s: %w", pidPath, err)
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				fmt.Printf("not running (stale pidfile pid=%d)\n", pid)
				return nil
			}
			if err := proc.Signal(syscall.Signal(0)); err != nil {
				fmt.Printf("not running (stale pidfile pid=%d)\n", pid)
				return nil
			}
			fmt.Printf("running (pid=%d, since=%s)\n", pid, info.ModTime().Format(time.RFC3339))
			return nil
		},
	}
}
