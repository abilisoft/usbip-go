package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// errInvalidOutput is the sentinel base for the --output enum check.
// Returned from validateGlobalFlags so MapError can key off it via
// errors.Is if we ever want to distinguish output-class usage errors
// from others; today it fits the ExitUsage bucket via isUsageError.
var errInvalidOutput = errors.New("invalid --output")

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
	// Config is an optional YAML config path. Currently unused by the
	// subcommands; retained on the global surface for spec §7.2 parity.
	Config string
}

// ctxKey is a private context-key type (avoids collisions with other
// packages that might stash values in the same context).
type ctxKey struct{ name string }

// loggerCtxKey is the context key under which PersistentPreRunE
// stores the *slog.Logger built from the parsed flags.
var loggerCtxKey = ctxKey{name: "logger"}

// flagsCtxKey is the context key storing the parsed globalFlags.
var flagsCtxKey = ctxKey{name: "flags"}

// newRootCmd constructs the top-level `usbip` cobra command with global
// flags, version subcommand, and a PersistentPreRunE that builds the
// logger and stashes both logger + flags on the context.
func newRootCmd() *cobra.Command {
	gf := &globalFlags{}

	cmd := &cobra.Command{
		Use:   "usbip",
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
	flags.StringVarP(&gf.Output, "output", "o", "table", "output format: table or json")
	flags.StringVar(&gf.LogLevel, "log-level", "info", "log level: error/warn/info/debug/trace")
	flags.StringVar(&gf.LogFormat, "log-format", "auto", "log handler: auto/pretty/json")
	flags.CountVarP(&gf.VerboseCount, "verbose", "v", "verbose counter: -v=debug, -vv=trace")
	flags.BoolVar(&gf.NoColor, "no-color", false, "disable ANSI color output")
	flags.StringVar(&gf.Config, "config", "", "path to YAML config file")

	// Fixed-completion hints for enum flags (spec §7.6 static completion).
	_ = cmd.RegisterFlagCompletionFunc("output", cobra.FixedCompletions(
		[]cobra.Completion{"table", "json"}, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("log-level", cobra.FixedCompletions(
		[]cobra.Completion{"error", "warn", "info", "debug", "trace"},
		cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("log-format", cobra.FixedCompletions(
		[]cobra.Completion{"auto", "pretty", "json"}, cobra.ShellCompDirectiveNoFileComp))

	cmd.AddCommand(newVersionCmd())

	return cmd
}

// validateGlobalFlags enforces the enum constraints on --output (the
// other enum flags are validated inside buildLogger). Cobra does not
// provide StringVar enum validation out of the box, so this runs in
// PersistentPreRunE.
func validateGlobalFlags(f *globalFlags) error {
	switch f.Output {
	case "table", "json":
		return nil
	default:
		return fmt.Errorf("%w %q (want table or json)", errInvalidOutput, f.Output)
	}
}

