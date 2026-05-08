// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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

// globalFlags bundles the shared top-level flags from spec §7.2. The
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
	// equivalent opt-in (spec §7.6).
	CompleteNetwork bool
}

// ctxKey is a private context-key type (avoids collisions with other
// packages that might stash values in the same context).
type ctxKey struct{ name string }

// loggerCtxKey is the context key under which PersistentPreRunE
// stores the *slog.Logger built from the parsed flags.
var loggerCtxKey = ctxKey{name: "logger"}

// flagsCtxKey is the context key storing the parsed globalFlags.
var flagsCtxKey = ctxKey{name: "flags"}

// newRootCmd constructs the top-level `usbip-go` cobra command with global
// flags, version subcommand, and a PersistentPreRunE that builds the
// logger and stashes both logger + flags on the context.
func newRootCmd() *cobra.Command {
	gf := &globalFlags{}

	cmd := &cobra.Command{
		Use:   "usbip-go",
		Short: "USB/IP client",
		Long: "usbip is the client for the USB/IP protocol: list, attach, " +
			"detach, bind, unbind, watch, and completion subcommands.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			err := validateGlobalFlags(gf)
			if err != nil {
				return err
			}

			log, err := buildLogger(*gf)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			ctx = context.WithValue(ctx, loggerCtxKey, log)
			ctx = context.WithValue(ctx, flagsCtxKey, gf)
			cmd.SetContext(ctx)

			return nil
		},
	}

	flags := cmd.PersistentFlags()
	flags.StringVarP(&gf.Output, "output", "o", outputTable, "output format: table or json")
	flags.StringVar(&gf.LogLevel, "log-level", "info", "log level: error/warn/info/debug/trace")
	flags.StringVar(&gf.LogFormat, "log-format", "auto", "log handler: auto/pretty/json")
	flags.CountVarP(&gf.VerboseCount, "verbose", "v", "verbose counter: -v=debug, -vv=trace")
	flags.BoolVar(&gf.NoColor, "no-color", false, "disable ANSI color output")
	flags.BoolVar(&gf.CompleteNetwork, "complete-network", false,
		"allow network-dialing shell completion (same as USBIP_COMPLETE_NETWORK=1)")

	// Fixed-completion hints for enum flags (spec §7.6 static
	// completion). These calls intentionally touch cobra's global
	// flagCompletionFunctions map; because the production binary
	// builds exactly one root command, the global state causes no
	// trouble. Parallel tests bypass registration via newRootCmd's
	// `skipFlagCompletionRegistration` hook to avoid data races with
	// cobra's internal bash-completion annotation writer.
	if !skipFlagCompletionRegistration {
		_ = cmd.RegisterFlagCompletionFunc("output", cobra.FixedCompletions(
			[]cobra.Completion{outputTable, outputJSON}, cobra.ShellCompDirectiveNoFileComp))
		_ = cmd.RegisterFlagCompletionFunc("log-level", cobra.FixedCompletions(
			[]cobra.Completion{"error", "warn", "info", "debug", "trace"},
			cobra.ShellCompDirectiveNoFileComp))
		_ = cmd.RegisterFlagCompletionFunc("log-format", cobra.FixedCompletions(
			[]cobra.Completion{"auto", "pretty", "json"}, cobra.ShellCompDirectiveNoFileComp))
	}

	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newAttachCmd())
	cmd.AddCommand(newDetachCmd())
	cmd.AddCommand(newPortCmd())
	cmd.AddCommand(newBindCmd())
	cmd.AddCommand(newUnbindCmd())
	cmd.AddCommand(newWatchCmd())

	// Cobra registers `completion {bash,zsh,fish,pwsh}` automatically
	// via the root command, so we augment it with `install`.
	withCompletionInstall(cmd)

	return cmd
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
	v, ok := ctx.Value(loggerCtxKey).(*slog.Logger)
	if !ok {
		return nil
	}

	return v
}

