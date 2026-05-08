package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/spf13/cobra"
)

// attachExpectedArgs is the positional-arg count for `attach`:
// <remote> + <busid>. Named to appease mnd.
const attachExpectedArgs = 2

// attachCompletionTimeout is the per-call deadline for the second-arg
// ListRemote dial (spec §7.6). Shell completion must feel instant, so
// we cap network interaction at 800ms and swallow any failure.
const attachCompletionTimeout = 800 * time.Millisecond

// completeNetworkEnv is the env-var opt-in that mirrors the
// --complete-network persistent flag. Either one enables network-
// dialing completion.
const completeNetworkEnv = "USBIP_COMPLETE_NETWORK"

// attachFlags bundles the attach-subcommand flags.
type attachFlags struct {
	AutoReconnect bool
	MaxAttempts   int
	Backoff       string
}

// newAttachCmd constructs the `usbip attach <host> <busid>` command.
func newAttachCmd() *cobra.Command {
	af := &attachFlags{}

	cmd := &cobra.Command{
		Use:   "attach <remote> <busid>",
		Short: "Attach a remote USB device to a local vhci port",
		Args:  cobra.ExactArgs(attachExpectedArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAttach(cmd, args, af)
		},
	}

	cmd.ValidArgsFunction = completeAttachArgs

	flags := cmd.Flags()
	flags.BoolVar(&af.AutoReconnect, "auto-reconnect", false, "watch for detach and re-attach automatically")
	flags.IntVar(&af.MaxAttempts, "max-attempts", 0, "cap on reconnect retries (0 = infinite)")
	flags.StringVar(&af.Backoff, "backoff", "",
		`reconnect backoff spec: "exp:<min>:<max>" or "fixed:<delay>"`)

	return cmd
}

// completeAttachArgs is the ValidArgsFunction for `usbip attach`.
// First arg: history-backed suggestions (silent on I/O error).
// Second arg: empty list unless USBIP_COMPLETE_NETWORK=1 or
// --complete-network is set, in which case we dial Importer.ListRemote
// capped at attachCompletionTimeout and return the busid list. All
// failures are silent per spec §7.6.
func completeAttachArgs(
	cmd *cobra.Command,
	args []string,
	_ string,
) ([]cobra.Completion, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return readHistory(), cobra.ShellCompDirectiveNoFileComp
	case 1:
		if !networkCompletionEnabled(cmd) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return completeAttachBusIDs(cmd, args[0]), cobra.ShellCompDirectiveNoFileComp
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// networkCompletionEnabled reports whether the user opted in to
// network-dialing shell completion, either via the
// USBIP_COMPLETE_NETWORK env var (any non-empty value) or the
// --complete-network persistent flag. Either signal is sufficient.
func networkCompletionEnabled(cmd *cobra.Command) bool {
	if os.Getenv(completeNetworkEnv) == "1" {
		return true
	}

	if cmd == nil {
		return false
	}

	f := cmd.Root().PersistentFlags().Lookup("complete-network")
	if f == nil {
		return false
	}

	return f.Value.String() == "true"
}

// completeAttachBusIDs dials the remote and returns its busid list as
// cobra completion suggestions. The dial is capped at 800ms and errors
// are swallowed — completion must never surface a failure user-side.
func completeAttachBusIDs(cmd *cobra.Command, remote string) []cobra.Completion {
	ep, err := domain.ParseRemote(remote)
	if err != nil {
		return nil
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	ctx, cancel := context.WithTimeout(ctx, attachCompletionTimeout)
	defer cancel()

	imp, err := newImporter(withLoggerFromCtx(ctx)...)
	if err != nil {
		return nil
	}

	defer func() { _ = imp.Close() }()

	devs, err := imp.ListRemote(ctx, ep)
	if err != nil {
		return nil
	}

	out := make([]cobra.Completion, 0, len(devs))
	for _, d := range devs {
		out = append(out, fmt.Sprintf("%s\t%04x:%04x", d.BusID, d.VendorID, d.ProductID))
	}

	return out
}

// runAttach performs the attach flow and renders the resulting Port.
func runAttach(cmd *cobra.Command, args []string, af *attachFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	spec, err := parseAttachArgs(args, af)
	if err != nil {
		return err
	}

	imp, err := newImporter(withLoggerFromCtx(ctx)...)
	if err != nil {
		return err
	}

	defer func() { _ = imp.Close() }()

	port, err := imp.Attach(ctx, spec.remote, spec.busID, spec.opts)
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}

	// History-record the host on success. Failures are logged (not
	// user-visible) because they must not clobber the attach result.
	recordErr := recordHistory(args[0])
	if recordErr != nil {
		log := loggerFromCtx(ctx)
		if log != nil {
			log.Warn("record attach history", "err", recordErr)
		}
	}

	return renderAttachResult(cmd, port)
}

// attachSpec bundles the parsed-and-validated inputs to Importer.Attach.
type attachSpec struct {
	remote usbip.RemoteEndpoint
	busID  usbip.BusID
	opts   usbip.AttachOptions
}

// parseAttachArgs validates args + flags and returns the attach spec.
// Splitting the parse out of runAttach keeps each function under the
// gocognit cap of 10.
func parseAttachArgs(args []string, af *attachFlags) (attachSpec, error) {
	ep, err := domain.ParseRemote(args[0])
	if err != nil {
		return attachSpec{}, errUsage("invalid remote %q: %s", args[0], err)
	}

	busID, err := domain.ParseBusID(args[1])
	if err != nil {
		return attachSpec{}, errUsage("invalid busid %q: %s", args[1], err)
	}

	var backoff usbip.BackoffStrategy
	if af.Backoff != "" {
		backoff, err = parseBackoff(af.Backoff)
		if err != nil {
			return attachSpec{}, errUsage("%s: %s", errInvalidBackoff, err)
		}
	}

	return attachSpec{
		remote: ep,
		busID:  busID,
		opts: usbip.AttachOptions{
			AutoReconnect: af.AutoReconnect,
			MaxAttempts:   af.MaxAttempts,
			Backoff:       backoff,
		},
	}, nil
}

// renderAttachResult writes the port information using the renderer
// selected by --output.
func renderAttachResult(cmd *cobra.Command, port usbip.Port) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	if outputFromCtx(ctx) == outputJSON {
		err := (jsonRenderer{}).AttachAck(out, port)
		if err != nil {
			return fmt.Errorf("render ack: %w", err)
		}

		return nil
	}

	err := (tableRenderer{}).Ports(out, []usbip.Port{port})
	if err != nil {
		return fmt.Errorf("render ports: %w", err)
	}

	return nil
}
