// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// statusSource is the substitutable seam the status server queries for
// live daemon state. Production wiring is *statusExporter in run.go;
// unit tests replace it with a struct stub to exercise the handler
// without a Linux kernel.
//
// BoundDevices returns (devices, nil) on success and (nil, err) when
// the underlying ListAvailable fails. The handler surfaces the err
// via the bound_devices_error JSON field rather than silently serving
// an empty bound_devices array.
type statusSource interface {
	BoundDevices(ctx context.Context) ([]usbip.Device, error)
	Sessions(ctx context.Context) []usbip.Session
	Listening() listeningState
	KernelModules(ctx context.Context) (map[string]usbip.ModuleState, error)
	Drain(ctx context.Context) error
}

// listeningState is the JSON-serialised view of the accept-path state
// rendered under `listening` in the §7.7 schema-v1 response.
type listeningState struct {
	Addr       string `json:"addr"`
	Activation bool   `json:"activation"`
	Accepting  bool   `json:"accepting"`
}

// statusResponse is the typed JSON envelope served on GET /. Declared
// as a concrete struct (not map[string]any) so the compiler guards the
// schema-v1 contract against drift (v1 contract §7.7).
//
// BoundDevicesError carries the human-readable reason ListAvailable
// failed when bound_devices would otherwise be an empty slice (RANK
// 12). The field is omitempty so the happy-path JSON stays unchanged.
type statusResponse struct {
	Schema            string                       `json:"schema"`
	Version           string                       `json:"version"`
	Commit            string                       `json:"commit"`
	UptimeSec         int64                        `json:"uptime_sec"`
	Listening         listeningState               `json:"listening"`
	BoundDevices      []boundDeviceJSON            `json:"bound_devices"`
	BoundDevicesError string                       `json:"bound_devices_error,omitempty"`
	Sessions          []sessionJSON                `json:"sessions"`
	KernelModules     map[string]usbip.ModuleState `json:"kernel_modules"`
}

// boundDeviceJSON is the schema-v1 shape for a bound device row.
type boundDeviceJSON struct {
	BusID     string `json:"busid"`
	VendorID  string `json:"vid"`
	ProductID string `json:"pid"`
}

// sessionJSON is the schema-v1 shape for a session row.
type sessionJSON struct {
	ID        string `json:"id"`
	Remote    string `json:"remote"`
	BusID     string `json:"busid"`
	StartedAt string `json:"started_at"`
	BytesIn   uint64 `json:"bytes_in"`
	BytesOut  uint64 `json:"bytes_out"`
}

// detectAlreadyRunningDialTimeout bounds the liveness probe; a stale
// socket that never answers must not wedge startup.
const detectAlreadyRunningDialTimeout = 250 * time.Millisecond

// errStatusLockFdTooLarge fires if the lock-file descriptor exceeds
// platform `int` width — impossible on Linux amd64/arm64 where int is
// 64-bit, but gosec G115's flow analysis demands a guarded narrowing.
var errStatusLockFdTooLarge = errors.New("status lock fd exceeds platform int width")

// statusReadHeaderTimeout caps header read time on the status HTTP
// server. The UDS has no real latency budget — five seconds is ample
// for a local client and short enough that a wedged peer cannot block
// the server goroutine indefinitely (G112 / Slowloris defence).
const statusReadHeaderTimeout = 5 * time.Second

// statusSocketMode is the permission mask applied to the UDS
// immediately after net.Listen via os.Chmod while the sidecar flock is
// still held. 0660 matches v1 contract §7.7: the socket-group user can read
// status without root. Written as a named constant so gosec G302's
// "prefer 0600" default can be pointed at this declaration (intent is
// explicit, not accidental).
//
// syscall.Umask was tried briefly and rejected: umask is a process-wide
// attribute, so racing goroutines (the test suite hits this hard via
// t.Parallel + t.TempDir) observe the narrowed mask and create their
// own files/directories with mode 0660, which strips the execute bit
// from directories and breaks MkdirTemp. Flock + post-bind chmod is the
// durable answer: the window between bind and chmod is contained by
// the flock (cross-process) and by the default-0022 umask (world still
// cannot connect() because UDS connect requires write perm absent from
// mode 0755).
const statusSocketMode os.FileMode = 0o660

// dialFunc is the minimal contract detectAlreadyRunningWithDialer
// consumes. The production dialer is net.Dialer.DialContext; tests
// inject a stub to exercise specific errno branches that would
// otherwise require filesystem or network manipulation.
type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// detectAlreadyRunning dials path. Success means another instance
// owns the socket and errAlreadyRunning is returned. ENOENT means
// fresh start; ECONNREFUSED means stale file the caller may unlink.
// Any other dial error — EACCES, ELOOP, context.DeadlineExceeded,
// etc. — surfaces verbatim so the caller does NOT unlink a socket
// it could not prove was stale.
func detectAlreadyRunning(ctx context.Context, path string) error {
	var d net.Dialer

	return detectAlreadyRunningWithDialer(ctx, path, d.DialContext)
}

// detectAlreadyRunningWithDialer is the dialer-injected core of
// detectAlreadyRunning. The production entry point passes a net.Dialer
// bound method; tests pass a stub. Semantics match the comment on
// detectAlreadyRunning; keeping the behaviour in one place avoids
// drift between production and test paths.
func detectAlreadyRunningWithDialer(
	ctx context.Context, path string, dial dialFunc,
) error {
	dctx, cancel := context.WithTimeout(ctx, detectAlreadyRunningDialTimeout)
	defer cancel()

	conn, err := dial(dctx, "unix", path)
	if err == nil {
		_ = conn.Close()

		return fmt.Errorf("%w: %s", errAlreadyRunning, path)
	}

	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	// ECONNREFUSED: stale file, caller unlinks. The dial error wraps
	// syscall.ECONNREFUSED on Linux.
	if errors.Is(err, syscall.ECONNREFUSED) {
		return nil
	}

	return fmt.Errorf("detect already-running on %s: %w", path, err)
}

// serveStatus binds the UDS at path, applies mode 0660 + optional
// group chown, and serves the status endpoint until ctx is cancelled.
// started is closed once the listener is accepting requests — callers
// can use it to synchronise on "server ready".
//
// The detect→unlink→bind sequence runs under an exclusive flock on a
// sidecar lock file (<path>.lock). Two daemons racing for the same
// path see deterministic semantics: the flock winner claims the
// socket; the loser falls through to detectAlreadyRunning and returns
// errAlreadyRunning. Without the flock, the TOCTOU window between
// detect and bind can let two daemons each decide the socket is stale
// and unlink each other's listener.
func serveStatus(
	ctx context.Context,
	path string,
	group string,
	src statusSource,
	started chan<- struct{},
) error {
	lis, err := bindStatusSocket(ctx, path, group)
	if err != nil {
		return err
	}

	// Listener close is the status-goroutine's responsibility; unlink
	// of the UDS file is NOT — runDaemon's own cleanup path owns that
	// so the socket is removed even when completeShutdown's
	// ShutdownTimeout fires with the status goroutine still wedged. A
	// forced os.Exit skips goroutine defers but still runs runDaemon's
	// function-scoped defers before returning to main.
	defer func() { _ = lis.Close() }()

	// drainCtx is the daemon ctx itself, NOT context.WithoutCancel(ctx).
	// The HTTP request context is ignored by the handler (the second
	// arg is `_ *http.Request`), so the status client's connection
	// state never reaches Drain — that part of the previous comment
	// stands. The daemon's own cancellation MUST propagate, however:
	// an operator who Ctrl-C's the daemon process AFTER triggering
	// drain expects the in-flight Shutdown call to abort, not to keep
	// running detached past the parent ctx. context.WithoutCancel
	// would have severed the daemon-cancel signal too.
	drainCtx := ctx

	// drainStarted gates handleStatusDrain so concurrent / repeat
	// POST /drain calls fold into a single goroutine spawn (see
	// ADR-0012). The variable lives in this closure so its lifetime
	// matches the status server itself; tests construct their own
	// server via startStatusTestServer and get a fresh flag per run.
	var drainStarted atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		handleStatusGet(w, r, src)
	})
	mux.HandleFunc("POST /drain", func(w http.ResponseWriter, r *http.Request) {
		// Reject any query params: v1 has no recognised flags on
		// /drain. Silently accepting `?force=true` would let a
		// future client typo masquerade as success; ADR-0012
		// chooses the explicit-rejection forward-compat policy.
		if r.URL.RawQuery != "" {
			http.Error(w, "POST /drain accepts no query parameters in v1", http.StatusBadRequest)

			return
		}

		handleStatusDrain(drainCtx, &drainStarted, w, src)
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: statusReadHeaderTimeout,
	}

	go func() {
		<-ctx.Done()
		// Force-close the listener; http.Serve returns with the resulting
		// net.ErrClosed and shuts cleanly.
		_ = lis.Close()
	}()

	if started != nil {
		close(started)
	}

	serr := server.Serve(lis)
	if serr != nil && !errors.Is(serr, net.ErrClosed) && !errors.Is(serr, http.ErrServerClosed) {
		return fmt.Errorf("serve status: %w", serr)
	}

	return nil
}

// bindStatusSocket is the mode-atomic bind sequence: it acquires an
// exclusive flock on <path>.lock, detects whether another daemon owns
// path, unlinks any stale remnant, installs the statusSocketUmask, and
// then calls net.Listen. Returns a ready-to-serve listener or
// errAlreadyRunning. Flock is held for the duration of detect+unlink+
// bind so concurrent daemons serialise; the flock is then released
// because the kernel's in-use UDS is itself the cross-process
// exclusion lock for the serving phase.
func bindStatusSocket(ctx context.Context, path, group string) (net.Listener, error) {
	lockPath := path + ".lock"

	// Create the lock file if needed; permissions match the socket so
	// an operator ls -la showing both is unsurprising. filepath.Clean
	// reassures gosec G304 of the path shape.
	lockFile, err := os.OpenFile(filepath.Clean(lockPath),
		os.O_CREATE|os.O_RDWR, statusSocketMode)
	if err != nil {
		return nil, fmt.Errorf("open status lock %q: %w", lockPath, err)
	}

	defer func() { _ = lockFile.Close() }()

	// uintptr→int narrowing guarded inline so gosec G115 sees a
	// bounded value; same idiom as cmd/usbipd/logger.go's isStderrTTY.
	rawFd := lockFile.Fd()
	if rawFd > uintptr(^uint(0)>>1) {
		return nil, fmt.Errorf("%w: got %d", errStatusLockFdTooLarge, rawFd)
	}

	lockFd := int(rawFd)

	lerr := syscall.Flock(lockFd, syscall.LOCK_EX|syscall.LOCK_NB)
	if lerr != nil {
		// Another daemon holds the lock → treat as already-running.
		// Surface the path for operator debug.
		if errors.Is(lerr, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", errAlreadyRunning, path)
		}

		return nil, fmt.Errorf("flock status lock %q: %w", lockPath, lerr)
	}

	defer func() { _ = syscall.Flock(lockFd, syscall.LOCK_UN) }()

	err = detectAlreadyRunning(ctx, path)
	if err != nil {
		return nil, err
	}

	// Unlink pre-existing file so bind succeeds. Fresh starts land on
	// ENOENT; stale starts land on a regular file leftover by a crashed
	// daemon. Guarded by the flock above so no peer can race us here.
	rerr := os.Remove(path)
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return nil, fmt.Errorf("unlink stale status socket %q: %w", path, rerr)
	}

	var lc net.ListenConfig

	lis, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("bind status socket %q: %w", path, err)
	}

	// Tighten the mode before releasing the flock. Any concurrent
	// daemon is still blocked on the flock above, so no peer can
	// observe the transient default-umask mode.
	cerr := os.Chmod(path, statusSocketMode)
	if cerr != nil {
		_ = lis.Close()
		_ = os.Remove(path)

		return nil, fmt.Errorf("chmod status socket %q: %w", path, cerr)
	}

	applyStatusSocketACL(path, group)

	return lis, nil
}

// applyStatusSocketACL chowns the UDS to the configured group.
// Lookup / chown failures are logged via slog.Default but do NOT fail
// startup (v1 contract §7.7: chown is an ops-facing convenience, not a hard
// gate — a dev machine without a `usbip-go` group still boots). Mode is
// set by bindStatusSocket's post-bind os.Chmod call, so this helper is
// chown-only. Returns nothing: every failure path is best-effort.
func applyStatusSocketACL(path string, group string) {
	if group == "" {
		return
	}

	grp, err := user.LookupGroup(group)
	if err != nil {
		slog.Default().Info("status-socket group lookup failed, skipping chown",
			slog.String("group", group), slog.Any("err", err))

		return
	}

	gid, err := strconv.Atoi(grp.Gid)
	if err != nil {
		slog.Default().Warn("status-socket group has non-numeric gid, skipping chown",
			slog.String("group", group), slog.String("gid", grp.Gid), slog.Any("err", err))

		return
	}

	err = os.Chown(path, -1, gid)
	if err != nil {
		// Best-effort — a non-root dev machine typically can't chown
		// to an arbitrary group. Surface the reason without aborting.
		slog.Default().Info("status-socket chown skipped (non-fatal)",
			slog.String("group", group), slog.Int("gid", gid), slog.Any("err", err))
	}
}

// handleStatusGet renders the schema-v1 JSON. Uptime is computed
// against processStartTime captured at package init; bound devices
// and sessions come from src.
func handleStatusGet(w http.ResponseWriter, r *http.Request, src statusSource) {
	km, err := src.KernelModules(r.Context())
	if err != nil {
		// Kernel-module probing is best-effort on non-Linux hosts; log
		// and fall through with whatever partial map came back so the
		// schema is still served.
		slog.Default().Info("status: kernel-module probe failed",
			slog.Any("err", err))
	}

	devs, bdErr := src.BoundDevices(r.Context())

	bdErrStr := ""
	if bdErr != nil {
		// ListAvailable failure (typically /sys inaccessible). Surface
		// via bound_devices_error rather than pretending the export
		// list is empty. Operators polling / can distinguish "no
		// exports" from "sysfs unreachable".
		bdErrStr = bdErr.Error()
		slog.Default().Warn("status: list bound devices failed",
			slog.Any("err", bdErr))
	}

	resp := statusResponse{
		Schema:            "v1",
		Version:           version,
		Commit:            commit,
		UptimeSec:         int64(time.Since(processStartTime).Seconds()),
		Listening:         src.Listening(),
		BoundDevices:      devicesToJSON(devs),
		BoundDevicesError: bdErrStr,
		Sessions:          sessionsToJSON(src.Sessions(r.Context())),
		KernelModules:     km,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	encErr := enc.Encode(resp)
	if encErr != nil {
		slog.Default().Warn("status: encode response failed",
			slog.Any("err", encErr))
	}
}

// handleStatusDrain kicks off the drain in a goroutine and returns 200
// immediately. Synchronising the drain with the response would let a
// slow kernel block the status client; the drain is fire-and-forget
// and the client uses `GET /` to poll for completion. drainCtx is
// derived from the daemon's server ctx (see serveStatus), so drain
// outlives the HTTP client but still stops when the daemon itself
// is asked to shut down.
//
// started guards against repeat POSTs spawning redundant goroutines.
// ADR-0012 requires `POST /drain` be idempotent at the handler level:
// the first POST flips started true via CompareAndSwap and spawns the
// drain goroutine, returning 202 Accepted (RFC 9110 §15.3.3 — the
// request was accepted for asynchronous processing). Subsequent POSTs
// see started already true and return 200 OK as an idempotent
// no-op acknowledgement. The two-code split lets monitoring tools
// distinguish "I initiated this drain" from "someone else already
// did" without parsing a response body. The underlying src.Drain
// implementation is also idempotent (Exporter.Shutdown wraps in
// sync.Once); the handler-level guard avoids the wasted goroutines
// and the duplicate error log noise that would otherwise occur on
// the rare path where Drain returns non-nil.
func handleStatusDrain(drainCtx context.Context, started *atomic.Bool, w http.ResponseWriter, src statusSource) {
	if !started.CompareAndSwap(false, true) {
		w.WriteHeader(http.StatusOK)

		return
	}

	go func() {
		err := src.Drain(drainCtx)
		if err != nil {
			slog.Default().Error("status: drain returned error",
				slog.Any("err", err))
		}
	}()

	w.WriteHeader(http.StatusAccepted)
}

// devicesToJSON renders []usbip.Device into the schema-v1 row shape.
func devicesToJSON(in []usbip.Device) []boundDeviceJSON {
	out := make([]boundDeviceJSON, 0, len(in))
	for _, d := range in {
		out = append(out, boundDeviceJSON{
			BusID:     string(d.BusID),
			VendorID:  fmt.Sprintf("0x%04x", d.VendorID),
			ProductID: fmt.Sprintf("0x%04x", d.ProductID),
		})
	}

	return out
}

// sessionsToJSON renders []usbip.Session into the schema-v1 row shape.
func sessionsToJSON(in []usbip.Session) []sessionJSON {
	out := make([]sessionJSON, 0, len(in))
	for _, s := range in {
		out = append(out, sessionJSON{
			ID:        s.ID.String(),
			Remote:    s.RemoteAddr.String(),
			BusID:     string(s.BusID),
			StartedAt: s.StartedAt.UTC().Format(time.RFC3339Nano),
			BytesIn:   s.BytesIn,
			BytesOut:  s.BytesOut,
		})
	}

	return out
}

// processStartTime is captured at package-init so the status
// handler reports meaningful uptime values.
var processStartTime = time.Now()
