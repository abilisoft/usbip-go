package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/spf13/cobra"
)

// errAlreadyRunning signals that another usbipd instance owns the
// --status-socket. Returned by serveStatus, mapped to exit code 3.
var errAlreadyRunning = errors.New("usbipd: another instance is running")

// errDrainTimeout signals that `usbipd drain` exceeded --drain-timeout.
// Mapped to exit code 9; the stderr message is emitted by the drain
// subcommand itself.
var errDrainTimeout = errors.New("drain timed out")

// ctxKey is a private context key type; same rationale as cmd/usbip.
type ctxKey struct{ name string }

// loggerCtxKey stores the *slog.Logger built from the parsed flags.
var loggerCtxKey = ctxKey{name: "logger"}

// configCtxKey stores the parsed *Config.
var configCtxKey = ctxKey{name: "config"}

// skipFlagCompletionRegistration matches cmd/usbip's test hook so
// parallel root-construction tests don't race on cobra's global
// flagCompletionFunctions map.
var skipFlagCompletionRegistration = false

// newRootCmd builds the top-level `usbipd` cobra command. The default
// action (RunE on root) starts the daemon; subcommands override.
func newRootCmd() *cobra.Command {
	cfg := &Config{}

	cmd := &cobra.Command{
		Use:   "usbipd",
		Short: "USB/IP server daemon",
		Long: "usbipd exports local USB devices over the USB/IP protocol. " +
			"Runs in the foreground; systemd or an equivalent supervisor " +
			"manages the lifecycle (spec §7.7).",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			err := loadConfig(cfg)
			if err != nil {
				return err
			}

			log, err := buildLogger(*cfg)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			ctx = context.WithValue(ctx, loggerCtxKey, log)
			ctx = context.WithValue(ctx, configCtxKey, cfg)
			cmd.SetContext(ctx)

			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemon(cmd.Context(), cfg)
		},
	}

	bindFlags(cmd, cfg)

	if !skipFlagCompletionRegistration {
		_ = cmd.RegisterFlagCompletionFunc("log-level", cobra.FixedCompletions(
			[]cobra.Completion{"error", "warn", "info", "debug", "trace"},
			cobra.ShellCompDirectiveNoFileComp))
		_ = cmd.RegisterFlagCompletionFunc("log-format", cobra.FixedCompletions(
			[]cobra.Completion{"auto", "pretty", "json"},
			cobra.ShellCompDirectiveNoFileComp))
	}

	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newDrainCmd())

	return cmd
}

// loggerFromCtx retrieves the *slog.Logger stashed by PersistentPreRunE.
// Returns nil when no logger is installed; callers apply a default.
func loggerFromCtx(ctx context.Context) *slog.Logger {
	v, ok := ctx.Value(loggerCtxKey).(*slog.Logger)
	if !ok {
		return nil
	}

	return v
}
