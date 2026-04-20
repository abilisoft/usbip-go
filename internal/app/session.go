package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// connCloser wraps a conn with a sync.Once so the deferred cleanup and
// the handshake-timeout watcher can both call close() without racing a
// double-close on the underlying fd.
type connCloser struct {
	conn net.Conn
	once sync.Once
	err  error
}

// close invokes conn.Close exactly once across all goroutines. Repeat
// callers get the same err the first close returned (nil in the happy
// path). Go's net.Conn tolerates double-close (returns a sentinel), but
// we must not double-close a handed-off fd — the kernel owns it.
func (c *connCloser) close() error {
	c.once.Do(func() {
		c.err = c.conn.Close()
	})

	return c.err
}

// handleConn is the per-connection entry point spawned by the accept
// loop. The handshake flow per spec §5.3:
//
//  1. Wrap the conn reader in a handshake-bytes cap (spec §11.5.3).
//  2. Arm a handshake timeout that closes the conn if no progress is
//     made in time.
//  3. Decode the OP header via the codec.
//  4. Dispatch on opcode:
//     - OP_REQ_DEVLIST → short-lived query: list devices, encode reply,
//     close conn.
//     - OP_REQ_IMPORT → long-lived session: decode busid, register the
//     session under the global + per-peer caps, hand the fd to the
//     kernel via ExportOnConn, block until ExportOnConn returns.
//
// fd-passing contract (spec §5.4 item 4): the handler closes the conn
// on every error path before ExportOnConn returns success. Once
// ExportOnConn returns (success OR the adapter rejected with the conn
// already closed), the handler MUST NOT close it itself — the kernel
// owns the fd on success, and the adapter is documented to have
// closed it on failure. The handedOff flag implements this split; a
// connCloser guards close-once for all non-handoff paths so the
// timeout watcher and the deferred cleanup do not double-close (Fix 4).
func (e *Exporter) handleConn(ctx context.Context, conn net.Conn) {
	handedOff := false

	closer := &connCloser{conn: conn}

	defer func() {
		if handedOff {
			return
		}

		err := closer.close()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			e.logger.Debug("exporter session close",
				slog.Any("err", err))
		}
	}()

	handshakeStart := e.clock.Now()

	stopTimeout := e.armHandshakeTimeout(closer)
	defer stopTimeout()

	reader := newHandshakeLimitReader(conn, e.cfg.maxHandshakeBytes)

	_, op, _, err := e.codec.DecodeHeader(reader)
	if err != nil {
		e.metrics.ExporterSessionAccepted(OutcomeHandshakeFailed)
		e.logger.Debug("exporter decode header",
			slog.Any("err", err))

		return
	}

	// Per-opcode handshake-body read sits INSIDE serveDevlist /
	// serveImport so the timeout covers the full handshake. Disarming
	// the deadline right after the header would leave a
	// DecodeOpReqImport body-decode stall uncovered.
	switch op {
	case wire.OpReqDevlist:
		stopTimeout()
		e.serveDevlist(ctx, reader, conn)
		e.metrics.ExporterHandshakeDuration(
			HandshakeOpDevlist,
			e.clock.Now().Sub(handshakeStart).Seconds(),
		)
	case wire.OpReqImport:
		handedOff = e.serveImport(ctx, reader, conn, stopTimeout)
		e.metrics.ExporterHandshakeDuration(
			HandshakeOpImport,
			e.clock.Now().Sub(handshakeStart).Seconds(),
		)
	case wire.OpRepDevlist, wire.OpRepImport:
		stopTimeout()
		e.metrics.ExporterSessionAccepted(OutcomeHandshakeFailed)
		// Reply opcodes arriving on an accepted connection indicate a
		// misbehaving peer (or a reversed-role misconfiguration).
		e.logger.Debug("exporter received reply opcode on accept side",
			slog.Any("opcode", op))
	default:
		stopTimeout()
		e.metrics.ExporterSessionAccepted(OutcomeHandshakeFailed)
		e.logger.Debug("exporter unexpected opcode",
			slog.Any("opcode", op))
	}
}

// armHandshakeTimeout spawns a watcher that closes conn (via the
// shared connCloser) after the configured HandshakeTimeout elapses on
// the Exporter's injected Clock. Returns a stop func that disarms the
// watcher; callers MUST call it exactly once after the handshake
// completes (or the watcher goroutine leaks until the timeout fires).
// A non-positive timeout disables the watcher entirely.
func (e *Exporter) armHandshakeTimeout(closer *connCloser) func() {
	if e.cfg.handshakeTimeout <= 0 {
		return func() {}
	}

	stop := make(chan struct{})
	stopped := make(chan struct{})

	e.sessionsWG.Go(func() {
		defer close(stopped)

		select {
		case <-e.clock.After(e.cfg.handshakeTimeout):
			e.logger.Debug("exporter handshake timeout",
				slog.Duration("timeout", e.cfg.handshakeTimeout))

			err := closer.close()
			if err != nil && !errors.Is(err, net.ErrClosed) {
				e.logger.Debug("exporter handshake close",
					slog.Any("err", err))
			}
		case <-stop:
		}
	})

	var once bool

	return func() {
		if once {
			return
		}

		once = true

		close(stop)
		<-stopped
	}
}

// serveDevlist handles the OP_REQ_DEVLIST request. Uses the limit
// reader as the input source so an attacker cannot send a huge
// trailing body to the exporter post-header. Conn close is handled by
// handleConn's deferred cleanup.
func (e *Exporter) serveDevlist(ctx context.Context, _ io.Reader, conn net.Conn) {
	devs, err := e.kernel.ListLocalDevices(ctx)
	if err != nil {
		e.logger.Warn("exporter list local devices",
			slog.Any("err", err))

		return
	}

	err = e.codec.EncodeOpRepDevlist(conn, devs)
	if err != nil {
		e.logger.Warn("exporter encode devlist reply",
			slog.Any("err", err))
	}
}

// serveImport handles the OP_REQ_IMPORT request. It decodes the busid
// body, registers the session (enforcing MaxSessions + per-peer cap),
// then calls ExportOnConn. Returns true when the fd was handed off
// (success path) so handleConn's deferred close skips closing the conn
// — the kernel owns it at that point. Any error path returns false;
// the deferred handler closes the conn per spec §5.4 item 4.
//
// stopTimeout is the handshake-timeout disarm callback; it is invoked
// AFTER DecodeOpReqImport completes so a stalled body-decode still
// fires the handshake deadline (Fix 3).
//
// After ExportOnConn returns success the kernel owns the fd but the
// session is still live (RANK 1); the real sysfs write completes
// immediately and the kernel carries the session for its actual
// duration. The handler MUST NOT exit yet — an early return would fire
// the deferred endSession and unregister the session, leaving Sessions()
// empty while the device is still exported and leaking the accepted
// conn. waitForSessionEnd blocks until the kernel signals session end
// via a matching uevent, Shutdown signals handle.done, or ctx cancels.
func (e *Exporter) serveImport(
	ctx context.Context, reader io.Reader, conn net.Conn, stopTimeout func(),
) bool {
	busID, err := e.codec.DecodeOpReqImport(reader)

	// Disarm the handshake deadline only once the full handshake read
	// has completed — successful or not. If we leave the watcher armed
	// past this point the long-running ExportOnConn call would be torn
	// down when the clock ticks forward.
	stopTimeout()

	if err != nil {
		e.metrics.ExporterSessionAccepted(OutcomeHandshakeFailed)
		e.logger.Warn("exporter decode import request",
			slog.Any("err", err))

		return false
	}

	sess, err := e.buildSession(conn, busID)
	if err != nil {
		e.metrics.ExporterSessionAccepted(OutcomeHandshakeFailed)
		e.logger.Warn("exporter build session",
			slog.Any("err", err))

		return false
	}

	peerKey := peerKeyFromAddr(conn.RemoteAddr())

	handle, err := e.registerSession(sess, peerKey, conn)
	if err != nil {
		e.metrics.ExporterSessionAccepted(OutcomeRejectedCap)
		e.logger.Warn("exporter session declined",
			slog.Any("busid", busID),
			slog.String("peer", peerKey),
			slog.Any("err", err))

		return false
	}

	// Successful registration: count the accept BEFORE ExportOnConn
	// because the kernel call may block for the session's entire lifetime.
	// Deferring the increment until ExportOnConn returns would hide live
	// sessions from the accepted_total counter.
	e.metrics.ExporterSessionAccepted(OutcomeHandshakeOK)
	e.updateSessionsActiveGauge()

	// Publish SessionStarted AFTER register (under its own lock) and
	// BEFORE ExportOnConn blocks. Using a defer for SessionEnded binds
	// emission to handler exit regardless of the kernel-call outcome.
	e.publishSessionEvent(domain.SessionStartedEvent{
		At:      e.clock.Now(),
		Session: handle.session,
	})

	defer e.endSession(handle, "handler exited")

	// Subscribe to KernelEvents BEFORE ExportOnConn (pass-2 RANK 1).
	// The real adapter's ExportOnConn returns the moment the sysfs
	// write lands, and the kernel can emit the matching detach uevent
	// in the narrow gap between "kernel took the fd" and the first
	// instruction after ExportOnConn returns. If we subscribed only
	// after ExportOnConn, a kernel that published the event in that
	// gap would be lost and the handler would park forever.
	//
	// Opening the subscription first guarantees the event is buffered
	// into our channel regardless of timing. An already-pending event
	// is consumed by the first iteration of waitForSessionEnd's select.
	events, cancelEvents, evErr := e.events.Subscribe(ctx)
	if evErr != nil {
		e.logger.Warn("exporter session-end pre-subscribe failed",
			slog.Any("busid", busID),
			slog.Any("err", evErr))

		// Subscribe is the only observation path for session end; if
		// we cannot open one we must not hand the fd to the kernel
		// (we would park forever after a successful handoff). Surface
		// the subscribe failure the same way as a kernel-side error
		// and let the deferred close tear the conn down.
		e.metrics.ExporterDisconnect(DisconnectReasonKernelError)

		return false
	}

	defer cancelEvents()

	err = e.kernel.ExportOnConn(ctx, conn, busID)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			e.logger.Warn("exporter export on conn",
				slog.Any("busid", busID),
				slog.Any("err", err))
		}

		e.metrics.ExporterDisconnect(classifyDisconnectReason(err))

		return false
	}

	reason := e.waitForSessionEnd(ctx, busID, handle, events)

	e.metrics.ExporterDisconnect(reason)

	return true
}

// waitForSessionEnd blocks the post-ExportOnConn handler until the
// kernel signals the session ended (RANK 1). Signals observed, in
// priority order:
//
//  1. handle.done closed — Shutdown is tearing the exporter down;
//     returns DisconnectReasonShutdown. Exporter.Shutdown cancels every
//     handle before draining sessionsWG, so the handler exits promptly.
//  2. ctx cancelled — same treatment as handle.done.
//  3. A KernelEvents event whose busid matches busID:
//     - PortDetachedEvent: kernel published a `remove`-action uevent
//       for the exported device — returns DisconnectReasonClientGone
//       because the remote client's detach drove the signal.
//     - DeviceUnboundEvent: local unbind of the busid — same treatment.
//
// The subscription is opened by serveImport BEFORE kernel.ExportOnConn
// (pass-2 RANK 1) so a detach uevent published in the gap between
// "kernel took the fd" and the first post-ExportOnConn instruction is
// buffered in the channel rather than lost. waitForSessionEnd consumes
// the pre-opened events channel; the subscription cancel is owned by
// serveImport's defer.
func (e *Exporter) waitForSessionEnd(
	ctx context.Context,
	busID domain.BusID,
	handle *sessionHandle,
	events <-chan domain.Event,
) DisconnectReason {
	for {
		select {
		case <-handle.done:
			return DisconnectReasonShutdown
		case <-ctx.Done():
			return DisconnectReasonShutdown
		case ev, ok := <-events:
			if !ok {
				// Source closed without a matching event. The kernel
				// events subscription has torn down from under us; the
				// safest thing is to unwind the session as if the kernel
				// signalled end — otherwise the handler leaks forever.
				return DisconnectReasonKernelError
			}

			if eventEndsSessionForBusID(ev, busID) {
				return DisconnectReasonClientGone
			}
		}
	}
}

// eventEndsSessionForBusID returns true iff ev is a kernel-side signal
// that the exporter session for busID has ended. The spec §5.4 contract
// says the kernel emits a `remove` uevent on the exported device's
// DEVPATH when the session tears down; parseUevent turns that into a
// PortDetachedEvent or DeviceUnboundEvent depending on the SUBSYSTEM.
// Matching on BusID is sufficient — both events carry the busid verbatim
// and the handler's subscription is per-session so no cross-talk can
// masquerade as an end signal.
func eventEndsSessionForBusID(ev domain.Event, busID domain.BusID) bool {
	switch e := ev.(type) {
	case domain.PortDetachedEvent:
		return e.Port.BusID == busID
	case domain.DeviceUnboundEvent:
		return e.Device.BusID == busID
	}

	return false
}

// classifyDisconnectReason maps an ExportOnConn terminator onto the
// §11.5.5 disconnect_total reason label.
func classifyDisconnectReason(err error) DisconnectReason {
	if err == nil {
		return DisconnectReasonGraceful
	}

	if errors.Is(err, context.Canceled) {
		return DisconnectReasonShutdown
	}

	return DisconnectReasonKernelError
}

// updateSessionsActiveGauge snapshots the current session-map size and
// pushes it to usbip_exporter_sessions_active. Called on every
// transition that moves the count.
func (e *Exporter) updateSessionsActiveGauge() {
	e.mu.RLock()

	n := len(e.sessions)

	e.mu.RUnlock()

	e.metrics.ExporterSessionsActive(n)
}

// endSession unregisters the session and publishes a SessionEnded
// event. Kept out of unregisterSession itself so the lock-holding
// bookkeeping call does not also trigger fan-out under the write lock
// (publishSessionEvent takes an RLock of the same mu, which would
// deadlock).
func (e *Exporter) endSession(h *sessionHandle, reason string) {
	e.unregisterSession(h.session.ID)

	e.publishSessionEvent(domain.SessionEndedEvent{
		At:      e.clock.Now(),
		Session: h.session,
		Reason:  reason,
	})
}

// buildSession assembles the domain.Session recorded for the accepted
// connection. The session id is UUIDv7 (chronologically sortable) per
// spec §11.5.5. A failure to generate the id is a process-level
// problem (rand source exhausted); surfaced to the caller.
func (e *Exporter) buildSession(conn net.Conn, busID domain.BusID) (domain.Session, error) {
	id, err := domain.NewSessionID()
	if err != nil {
		return domain.Session{}, newSessionIDError(err)
	}

	return domain.Session{
		ID:         id,
		RemoteAddr: remoteAddrPort(conn.RemoteAddr()),
		BusID:      busID,
		StartedAt:  e.clock.Now(),
	}, nil
}

// newSessionIDError wraps the rand-source failure so session builders
// can err113-cleanly surface the underlying cause.
func newSessionIDError(err error) error { return &sessionIDError{err: err} }

// sessionIDError reports UUIDv7 generation failures as a distinct
// sentinel-wrappable error type.
type sessionIDError struct{ err error }

func (e *sessionIDError) Error() string { return "generate session id: " + e.err.Error() }

func (e *sessionIDError) Unwrap() error { return e.err }

// remoteAddrPort extracts a netip.AddrPort from a net.Addr, returning
// the zero value when the addr is nil or not a TCP/UDP addr. The
// session record uses the netip form so JSON serialisation is cheap
// and string-round-trippable.
func remoteAddrPort(addr net.Addr) netip.AddrPort {
	if addr == nil {
		return netip.AddrPort{}
	}

	if t, ok := addr.(*net.TCPAddr); ok && t != nil {
		ap := t.AddrPort()

		return ap
	}

	host, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return netip.AddrPort{}
	}

	return host
}
