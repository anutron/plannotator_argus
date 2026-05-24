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

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop a running plannotator-argus daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Default()
			if err != nil {
				return err
			}
			_ = cfg.LoadFromEnv()
			pidPath := cfg.PIDPath()
			raw, err := os.ReadFile(pidPath)
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("no pidfile at %s (daemon not running?)", pidPath)
			}
			if err != nil {
				return fmt.Errorf("read pidfile %s: %w", pidPath, err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				return fmt.Errorf("invalid pidfile %s: %w", pidPath, err)
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				return fmt.Errorf("find pid %d: %w", pid, err)
			}
			// Verify the target is alive before SIGTERM'ing. Avoids
			// signalling an unrelated process if the PID has been recycled.
			if err := proc.Signal(syscall.Signal(0)); err != nil {
				_ = os.Remove(pidPath)
				return fmt.Errorf("no process for pid %d (stale pidfile removed)", pid)
			}
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("signal pid %d: %w", pid, err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if err := proc.Signal(syscall.Signal(0)); err != nil {
					_ = os.Remove(pidPath)
					fmt.Fprintf(os.Stderr, "stopped pid %d\n", pid)
					return nil
				}
				time.Sleep(200 * time.Millisecond)
			}
			return fmt.Errorf("pid %d did not exit within 10s", pid)
		},
	}
}
