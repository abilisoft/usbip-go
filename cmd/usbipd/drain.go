package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// drainPollInterval is the default poll period while waiting for the
// daemon to report sessions=[] AND listening.accepting=false. 200ms is
// brisk enough for operators but comfortable enough for a status
// endpoint that queries the kernel on each GET.
const drainPollInterval = 200 * time.Millisecond

// drainHTTPTimeout caps a single HTTP request against the status UDS.
// The endpoint is local; a request that can't complete within a second
// is a signal something is very wrong.
const drainHTTPTimeout = time.Second

// newDrainCmd returns the `usbipd drain` subcommand. It asks the
// running daemon to refuse new accepts and exit once in-flight
// sessions complete; polling continues until either sessions=[] AND
// listening.accepting=false (success), the UDS disappears (daemon
// gone, success), or --drain-timeout expires (exit 9).
func newDrainCmd() *cobra.Command {
	var (
		socketPath   string
		drainTimeout time.Duration
		pollInterval time.Duration
	)

	cmd := &cobra.Command{
		Use:           "drain",
		Short:         "Request the running usbipd to refuse new accepts and exit",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDrain(cmd, drainArgs{
				socketPath:   socketPath,
				drainTimeout: drainTimeout,
				pollInterval: pollInterval,
			})
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&socketPath, "status-socket", defaultStatusSocket,
		"UDS path the running usbipd is serving its status endpoint on")
	flags.DurationVar(&drainTimeout, "drain-timeout", defaultDrainTimeout,
		"maximum wait for drain to complete before exit 9")
	flags.DurationVar(&pollInterval, "poll-interval", drainPollInterval,
		"interval between GET / poll requests after POST /drain")

	return cmd
}

// drainArgs bundles the per-invocation inputs into one value so runDrain
// stays focused on the drain protocol itself.
type drainArgs struct {
	socketPath   string
	drainTimeout time.Duration
	pollInterval time.Duration
}

// runDrain executes the client-side drain protocol: POST /drain once,
// then poll GET / until the daemon reports it is idle or the timeout
// expires. A dial failure after the first POST is treated as success
// per spec §7.7 (the daemon exited while we were polling).
func runDrain(cmd *cobra.Command, args drainArgs) error {
	client := newDrainHTTPClient(args.socketPath)

	ctx, cancel := context.WithTimeout(cmd.Context(), args.drainTimeout)
	defer cancel()

	err := postDrain(ctx, client)
	if err != nil {
		return fmt.Errorf("post /drain: %w", err)
	}

	err = pollUntilIdle(ctx, client, args.pollInterval)
	if err == nil {
		return nil
	}

	if errors.Is(err, context.DeadlineExceeded) {
		_, writeErr := fmt.Fprintf(cmd.ErrOrStderr(),
			"drain timed out after %s\n", args.drainTimeout)
		if writeErr != nil {
			// Stderr unwriteable is an unusual failure mode — surface
			// it rather than silently swallow the write.
			return fmt.Errorf("write drain-timeout notice: %w", writeErr)
		}

		return fmt.Errorf("%w after %s", errDrainTimeout, args.drainTimeout)
	}

	return err
}

// newDrainHTTPClient returns an http.Client that dials the given UDS
// path. Every request uses a fresh dialer derived from the ambient
// context so drain's overall deadline governs per-request I/O too.
func newDrainHTTPClient(path string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer

				return d.DialContext(ctx, "unix", path)
			},
		},
		Timeout: drainHTTPTimeout,
	}
}

// postDrain issues the initial POST /drain. Transport errors are
// surfaced; a 2xx response is required — anything else is wrapped
// verbatim so operators can tell stateful-miss from transport failure.
func postDrain(ctx context.Context, client *http.Client) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://usbipd/drain", nil)
	if err != nil {
		return fmt.Errorf("build POST /drain: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do POST /drain: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: HTTP %d", errDrainPostFailed, resp.StatusCode)
	}

	return nil
}

// errDrainPostFailed is returned when the initial POST /drain receives
// a non-2xx response. err113 wants sentinels for dynamic messages, so
// the HTTP status code is appended via fmt.Errorf("%w: ...").
var errDrainPostFailed = errors.New("POST /drain failed")

// pollUntilIdle loops GET / requests at pollInterval until sessions is
// empty AND listening.accepting is false, or the context is done. A
// dial failure after the first iteration is treated as "daemon gone
// → drained".
func pollUntilIdle(ctx context.Context, client *http.Client, pollInterval time.Duration) error {
	// First probe happens immediately — if the daemon already drained
	// between POST and the first GET, operators should not wait an
	// extra poll interval for no reason.
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		done, err := probeStatusOnce(ctx, client)
		if err != nil {
			return err
		}

		if done {
			return nil
		}

		err = waitNextPoll(ctx, ticker)
		if err != nil {
			return err
		}
	}
}

// waitNextPoll blocks on the next ticker tick or ctx cancellation.
// Extracted from pollUntilIdle so that function's cognitive complexity
// stays under the gocognit threshold.
func waitNextPoll(ctx context.Context, ticker *time.Ticker) error {
	select {
	case <-ctx.Done():
		cause := context.Cause(ctx)
		if cause != nil && !errors.Is(cause, context.DeadlineExceeded) {
			return fmt.Errorf("drain cancelled: %w", cause)
		}

		return fmt.Errorf("drain context: %w", ctx.Err())
	case <-ticker.C:
		return nil
	}
}

// probeStatusOnce issues a single GET /. Returns done=true when the
// daemon reports sessions=[] AND listening.accepting=false, or when
// the dial fails with a connect error (the daemon exited → drained).
// Transport errors that don't look like a clean shutdown propagate.
func probeStatusOnce(ctx context.Context, client *http.Client) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://usbipd/", nil)
	if err != nil {
		return false, fmt.Errorf("build GET /: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		if isDaemonGoneError(err) {
			return true, nil
		}

		return false, fmt.Errorf("do GET /: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("read GET / body: %w", err)
	}

	var state statusProbe

	err = json.Unmarshal(body, &state)
	if err != nil {
		return false, fmt.Errorf("decode GET / body: %w", err)
	}

	return len(state.Sessions) == 0 && !state.Listening.Accepting, nil
}

// statusProbe is a minimal view of the schema-v1 status JSON — only
// the fields drain needs to make its go/no-go decision. Using a typed
// view (not map[string]any) keeps the JSON contract compiler-checked.
type statusProbe struct {
	Sessions  []json.RawMessage `json:"sessions"`
	Listening listeningState    `json:"listening"`
}

// isDaemonGoneError reports whether err from an HTTP Do call looks
// like the daemon exiting between polls. ECONNREFUSED + ENOENT (dial
// to a path that no longer exists) + closed-connection errors all
// count. Any other transport failure is unexpected and propagates.
func isDaemonGoneError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, net.ErrClosed) {
		return true
	}

	// net.OpError wraps syscall and fs.ErrNotExist chains for UDS dial;
	// errors.As walks the chain.
	var netErr *net.OpError

	return errors.As(err, &netErr)
}
