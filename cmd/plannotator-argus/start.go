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
				return errors.New("background mode is not implemented; rerun with --foreground (use nohup or a launchd plist to background it for now)")
			}
			cfg := config.Default()
			if err := cfg.LoadFromEnv(); err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return daemon.Run(ctx, cfg, log)
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run the daemon in the foreground (required in v1)")
	return cmd
}
