// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

// errInvalidOutput is the sentinel base for the --output enum check.
// Returned from validateGlobalFlags so MapError can key off it via
// errors.Is if we ever want to distinguish output-class usage errors
// from others; today it fits the ExitUsage bucket via isUsageError.
var errInvalidOutput = errors.New("invalid --output")

// skipFlagCompletionRegistration is flipped on by TestMain so parallel
// tests do not populate cobra's global flagCompletionFunctions map and
// avoid the intra-process race between `prepareCustomAnnotationsForFlags`
// and `ValidateRequiredFlags`.
var skipFlagCompletionRegistration = false

// outputTable and outputJSON are the two legal values of --output;
// centralising them kills goconst's complaint about duplicated literals.
const (
	outputTable = "table"
	outputJSON  = "json"
)

// globalFlags bundles the shared top-level flags from v1 contract §7.2. The
// struct is populated by cobra's flag bindings during ParseFlags; each
// subcommand reads from the shared pointer stored on the root cobra
// command via context.
type globalFlags struct {
	// Output selects the rendering mode; one of "table" or "json".
	Output string
	// LogLevel is one of "error", "warn", "info", "debug", "trace".
	LogLevel string
	// LogFormat is one of "auto", "pretty", "json".
	LogFormat string
	// VerboseCount counts -v/-vv occurrences: 1=debug, 2=trace.
	VerboseCount int
	// NoColor disables ANSI colors in the pretty handler and the
	// table renderer, mirroring NO_COLOR=<any> env behaviour.
	NoColor bool
	// CompleteNetwork gates network-backed shell completion. When
	// false (the default), second-arg completion for `usbip-go attach`
	// returns an empty list to avoid silently dialing remotes during
	// tab-completion. The USBIP_COMPLETE_NETWORK=1 env var is the
	// equivalent opt-in (v1 contract §7.6).
	CompleteNetwork bool
}

// Private zero-sized context-key types avoid collisions without mutable
// package-level key values.
type loggerContextKey struct{}

type flagsContextKey struct{}

// newRootCmd constructs the top-level `usbip-go` cobra command with global
// flags, version subcommand, and a PersistentPreRunE that builds the
// logger and stashes both logger + flags on the context.
func newRootCmd() *cobra.Command {
	gf := &globalFlags{}

	cmd := &cobra.Command{
		Use:   "usbip-go",
		Short: "USB/IP client (Go reimplementation)",
		Long: "usbip-go is the pure-Go USB/IP CLI: list, attach, detach, " +
			"bind, unbind, watch, serve, drain, and completion subcommands.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			err := validateGlobalFlags(gf)
			if err != nil {
				return err
			}

			// --no-color must propagate to the table renderer (lipgloss
			// reads NO_COLOR via the colorprofile package). os.Setenv
			// before any styleWriter is constructed so the first render
			// already sees the demand.
			if gf.NoColor {
				_ = os.Setenv("NO_COLOR", "1")
			}

			log, err := buildLogger(*gf)
			if err != nil {
				return err
			}

			// Install as slog.Default so library code that has no
			// access to ctx (pkg/usbip helpers, transitive deps that
			// log via slog) inherits the same handler / level / format.
			// Without this an operator running `usbip-go serve` saw
			// stdlib-formatted lines like
			//   2026/05/08 19:18:38 INFO status-socket group lookup ...
			// mixed in among the configured tint / JSON output.
			slog.SetDefault(log)

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			ctx = context.WithValue(ctx, loggerContextKey{}, log)
			ctx = context.WithValue(ctx, flagsContextKey{}, gf)
			cmd.SetContext(ctx)

			return nil
		},
	}

	flags := cmd.PersistentFlags()
	flags.StringVarP(&gf.Output, "output", "o", outputTable, "output format: table or json")
	flags.StringVar(&gf.LogLevel, "log-level", defaultLogLevel, "log level: error/warn/info/debug/trace")
	flags.StringVar(&gf.LogFormat, "log-format", defaultLogFormat, "log handler: auto/pretty/json")
	flags.CountVarP(&gf.VerboseCount, "verbose", "v", "verbose counter: -v=debug, -vv=trace")
	flags.BoolVar(&gf.NoColor, "no-color", false, "disable ANSI color output")
	flags.BoolVar(&gf.CompleteNetwork, "complete-network", false,
		"allow network-dialing shell completion (same as USBIP_COMPLETE_NETWORK=1)")

	// Fixed-completion hints for enum flags (v1 contract §7.6 static
	// completion). These calls intentionally touch cobra's global
	// flagCompletionFunctions map; because the production binary
	// builds exactly one root command, the global state causes no
	// trouble. Parallel tests bypass registration via newRootCmd's
	// `skipFlagCompletionRegistration` hook to avoid data races with
	// cobra's internal bash-completion annotation writer.
	if !skipFlagCompletionRegistration {
		registerRootFlagCompletions(cmd)
	}

	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newAttachCmd())
	cmd.AddCommand(newDetachCmd())
	cmd.AddCommand(newPortCmd())
	cmd.AddCommand(newBindCmd())
	cmd.AddCommand(newUnbindCmd())
	cmd.AddCommand(newWatchCmd())
	cmd.AddCommand(newServeCmd())
	cmd.AddCommand(newDrainCmd())

	// Cobra registers `completion {bash,zsh,fish,pwsh}` automatically
	// via the root command, so we augment it with `install`.
	withCompletionInstall(cmd)

	return cmd
}

// registerRootFlagCompletions installs the fixed completions for global enum
// flags. Registration is isolated from command construction because Cobra
// stores these callbacks in process-global state.
func registerRootFlagCompletions(cmd *cobra.Command) {
	_ = cmd.RegisterFlagCompletionFunc("output", cobra.FixedCompletions(
		[]cobra.Completion{outputTable, outputJSON}, cobra.ShellCompDirectiveNoFileComp,
	))
	_ = cmd.RegisterFlagCompletionFunc("log-level", cobra.FixedCompletions(
		[]cobra.Completion{
			logLevelError,
			logLevelWarn,
			logLevelInfo,
			logLevelDebug,
			logLevelTrace,
		},
		cobra.ShellCompDirectiveNoFileComp,
	))
	_ = cmd.RegisterFlagCompletionFunc("log-format", cobra.FixedCompletions(
		[]cobra.Completion{logFormatAuto, logFormatPretty, logFormatJSON},
		cobra.ShellCompDirectiveNoFileComp,
	))
}

// validateGlobalFlags enforces the enum constraints on --output (the
// other enum flags are validated inside buildLogger). Cobra does not
// provide StringVar enum validation out of the box, so this runs in
// PersistentPreRunE.
func validateGlobalFlags(f *globalFlags) error {
	switch f.Output {
	case outputTable, outputJSON:
		return nil
	default:
		return fmt.Errorf("%w %q (want table or json)", errInvalidOutput, f.Output)
	}
}

// loggerFromCtx retrieves the *slog.Logger stashed by PersistentPreRunE.
// Returns nil when no logger is installed; callers apply a default at
// that point.
func loggerFromCtx(ctx context.Context) *slog.Logger {
	v, ok := ctx.Value(loggerContextKey{}).(*slog.Logger)
	if !ok {
		return nil
	}

	return v
}

// loggerOrDefault returns the ctx-bound logger or slog.Default() when
// the ctx carries no logger. Use this at any log site whose caller
// path may pre-date PersistentPreRunE (e.g. http.Server handlers
// invoked from net/http internals, helper goroutines spawned before
// the cobra cmd has installed its logger). The resulting *slog.Logger
// is never nil — callers can issue Info/Warn/Error directly.
func loggerOrDefault(ctx context.Context) *slog.Logger {
	if log := loggerFromCtx(ctx); log != nil {
		return log
	}

	return slog.Default()
}
