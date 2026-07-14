package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/anutron/plannotator_argus/internal/config"
	"github.com/anutron/plannotator_argus/internal/daemon"
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
			st, err := daemon.ProbePIDFile(pidPath)
			if err != nil {
				return fmt.Errorf("check pidfile %s: %w", pidPath, err)
			}
			if !st.Running {
				fmt.Printf("not running (stale pidfile pid=%d)\n", st.PID)
				return nil
			}
			fmt.Printf("running (pid=%d, since=%s)\n", st.PID, info.ModTime().Format(time.RFC3339))
			return nil
		},
	}
}
