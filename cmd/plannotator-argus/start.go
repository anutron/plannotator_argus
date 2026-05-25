package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/anutron/plannotator_argus/internal/config"
	"github.com/anutron/plannotator_argus/internal/daemon"
)

func newStartCmd() *cobra.Command {
	var foreground bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the plannotator-argus daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !foreground {
				return errors.New("background mode is not implemented in v1. To background the daemon run: `nohup plannotator-argus start --foreground &` or install a launchd plist that invokes the same command")
			}
			cfg, err := config.Default()
			if err != nil {
				return err
			}
			if err := cfg.LoadFromEnv(); err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			// daemon.Run returns a non-nil error on fatal heartbeat
			// failure (sustained transport failure or HTTP 401 from
			// argus). Cobra-with-SilenceErrors and main's os.Exit(1)
			// fall-through propagate that into a non-zero process exit
			// code, which is what launchd's KeepAlive.SuccessfulExit=false
			// keys off to restart the daemon onto a freshly discovered
			// argus URL.
			return daemon.Run(ctx, cfg, log)
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run the daemon in the foreground (required in v1)")
	return cmd
}
