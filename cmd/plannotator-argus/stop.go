package main

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/anutron/plannotator_argus/internal/config"
	"github.com/anutron/plannotator_argus/internal/daemon"
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
			st, err := daemon.ProbePIDFile(pidPath)
			if err != nil {
				return fmt.Errorf("check pidfile %s: %w", pidPath, err)
			}
			if !st.Exists {
				return fmt.Errorf("no pidfile at %s (daemon not running?)", pidPath)
			}
			if !st.Running {
				_ = os.Remove(pidPath)
				return fmt.Errorf("no process for pid %d (stale pidfile removed)", st.PID)
			}
			if st.PID <= 0 {
				return fmt.Errorf("pidfile %s is locked but has no readable pid; refusing to signal", pidPath)
			}
			proc, err := os.FindProcess(st.PID)
			if err != nil {
				return fmt.Errorf("find pid %d: %w", st.PID, err)
			}
			// ProbePIDFile above confirmed liveness via the pidfile's
			// advisory lock, which proves a live plannotator-argus daemon
			// holds this exact file — not merely that some process exists
			// at this PID number, which is all a signal(0) check could
			// ever prove. That's what makes it safe to SIGTERM the
			// recorded PID here, even if the number has been reused since
			// the pidfile was written.
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("signal pid %d: %w", st.PID, err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				cur, err := daemon.ProbePIDFile(pidPath)
				if err != nil {
					return fmt.Errorf("check pidfile %s: %w", pidPath, err)
				}
				if !cur.Running {
					_ = os.Remove(pidPath)
					fmt.Fprintf(os.Stderr, "stopped pid %d\n", st.PID)
					return nil
				}
				time.Sleep(200 * time.Millisecond)
			}
			return fmt.Errorf("pid %d did not exit within 10s", st.PID)
		},
	}
}
