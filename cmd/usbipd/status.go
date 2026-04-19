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
	"strconv"
	"syscall"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// statusSource is the substitutable seam the status server queries for
// live daemon state. Production wiring is *statusExporter in run.go;
// unit tests replace it with a struct stub to exercise the handler
// without a Linux kernel.
type statusSource interface {
	BoundDevices(ctx context.Context) []usbip.Device
	Sessions(ctx context.Context) []usbip.Session
	Listening() listeningState
	KernelModules(ctx context.Context) (map[string]string, error)
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
// schema-v1 contract against drift (spec §7.7).
type statusResponse struct {
	Schema        string            `json:"schema"`
	Version       string            `json:"version"`
	Commit        string            `json:"commit"`
	UptimeSec     int64             `json:"uptime_sec"`
	Listening     listeningState    `json:"listening"`
	BoundDevices  []boundDeviceJSON `json:"bound_devices"`
	Sessions      []sessionJSON     `json:"sessions"`
	KernelModules map[string]string `json:"kernel_modules"`
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

// statusReadHeaderTimeout caps header read time on the status HTTP
// server. The UDS has no real latency budget — five seconds is ample
// for a local client and short enough that a wedged peer cannot block
// the server goroutine indefinitely (G112 / Slowloris defence).
const statusReadHeaderTimeout = 5 * time.Second

// statusSocketMode is the permission mask applied to the UDS after
// bind. 0660 matches spec §7.7: the socket-group user can read status
// without root. Written as a named constant so gosec G302's "prefer
// 0600" default can be pointed at this declaration (intent is explicit,
// not accidental).
const statusSocketMode os.FileMode = 0o660

// detectAlreadyRunning dials path. Success → another instance owns the
// socket → errAlreadyRunning. ECONNREFUSED → stale file → nil (caller
// must unlink). ENOENT → fresh start → nil. Any other error falls
// through: we prefer to surface dial oddities verbatim.
func detectAlreadyRunning(ctx context.Context, path string) error {
	dctx, cancel := context.WithTimeout(ctx, detectAlreadyRunningDialTimeout)
	defer cancel()

	var d net.Dialer

	conn, err := d.DialContext(dctx, "unix", path)
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

	// Any other error (permissions, bad type) — let caller decide.
	// Today we treat it as non-fatal so startup proceeds; the listen
	// below will fail loudly if the path is truly unusable.
	return nil
}

// serveStatus binds the UDS at path, applies mode 0660 + optional
// group chown, and serves the status endpoint until ctx is cancelled.
// started is closed once the listener is accepting requests — callers
// can use it to synchronise on "server ready".
func serveStatus(
	ctx context.Context,
	path string,
	group string,
	src statusSource,
	started chan<- struct{},
) error {
	err := detectAlreadyRunning(ctx, path)
	if err != nil {
		return err
	}

	// Unlink pre-existing file so bind succeeds. Fresh starts land on
	// ENOENT; stale starts land on a regular file leftover by a
	// crashed daemon.
	rerr := os.Remove(path)
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return fmt.Errorf("unlink stale status socket %q: %w", path, rerr)
	}

	var lc net.ListenConfig

	lis, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("bind status socket %q: %w", path, err)
	}

	defer func() {
		_ = lis.Close()
		// Best-effort cleanup — unlink silently if the socket file is
		// still around. Matches spec §7.7: next startup handles stale
		// unlink on SIGKILL.
		_ = os.Remove(path)
	}()

	err = applyStatusSocketACL(path, group)
	if err != nil {
		return err
	}

	// drainCtx detaches from the HTTP request context so drain can
	// outlive the status client's connection but remains bounded by
	// the daemon's ctx. context.WithoutCancel(ctx) cancels alongside
	// daemon shutdown while ignoring per-request cancellation.
	drainCtx := context.WithoutCancel(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		handleStatusGet(w, r, src)
	})
	mux.HandleFunc("POST /drain", func(w http.ResponseWriter, _ *http.Request) {
		handleStatusDrain(drainCtx, w, src)
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

// applyStatusSocketACL applies mode 0660 and the configured group
// ownership. Lookup / chown failures are logged via slog.Default but do
// NOT fail startup (spec §7.7: chown is an ops-facing convenience, not
// a hard gate — a dev machine without a `usbip` group still boots).
func applyStatusSocketACL(path string, group string) error {
	err := os.Chmod(path, statusSocketMode)
	if err != nil {
		return fmt.Errorf("chmod status socket: %w", err)
	}

	if group == "" {
		return nil
	}

	grp, err := user.LookupGroup(group)
	if err != nil {
		slog.Default().Info("status-socket group lookup failed, skipping chown",
			slog.String("group", group), slog.Any("err", err))

		return nil
	}

	gid, err := strconv.Atoi(grp.Gid)
	if err != nil {
		slog.Default().Warn("status-socket group has non-numeric gid, skipping chown",
			slog.String("group", group), slog.String("gid", grp.Gid), slog.Any("err", err))

		return nil
	}

	err = os.Chown(path, -1, gid)
	if err != nil {
		// Best-effort — a non-root dev machine typically can't chown
		// to an arbitrary group. Surface the reason without aborting.
		slog.Default().Info("status-socket chown skipped (non-fatal)",
			slog.String("group", group), slog.Int("gid", gid), slog.Any("err", err))

		return nil
	}

	return nil
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

	resp := statusResponse{
		Schema:        "v1",
		Version:       version,
		Commit:        commit,
		UptimeSec:     int64(time.Since(processStartTime).Seconds()),
		Listening:     src.Listening(),
		BoundDevices:  devicesToJSON(src.BoundDevices(r.Context())),
		Sessions:      sessionsToJSON(src.Sessions(r.Context())),
		KernelModules: km,
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
func handleStatusDrain(drainCtx context.Context, w http.ResponseWriter, src statusSource) {
	go func() {
		err := src.Drain(drainCtx)
		if err != nil {
			slog.Default().Error("status: drain returned error",
				slog.Any("err", err))
		}
	}()

	w.WriteHeader(http.StatusOK)
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
