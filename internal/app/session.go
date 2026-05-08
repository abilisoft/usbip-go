// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

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
// loop. The handshake flow per v1 contract §5.3:
//
//  1. Wrap the conn reader in a handshake-bytes cap (v1 contract §11.5.3).
//  2. Arm a handshake timeout that closes the conn if no progress is
//     made in time.
//  3. Decode the OP header via the codec.
//  4. Dispatch on opcode:
//     - OP_REQ_DEVLIST → short-lived query: list devices, encode reply,
//     close conn.
//     - OP_REQ_IMPORT → long-lived session: decode busid, register the
//     session under the global + per-peer caps, hand the fd to the
//     kernel via ExportOnConn, block until waitForSessionEnd observes
//     kernel-side session end.
//
// fd-passing contract (v1 contract §5.4 item 4): the kernel dups the accepted
// fd on ExportOnConn success and holds its own ref; the app's original
// ref is released here exactly once via connCloser (sync.Once). The
// deferred close fires on every handler exit regardless of outcome so
// the kernel's dup keeps the socket alive while the app's userspace ref
// is released promptly. sync.Once guards against the handshake-timeout
// watcher's concurrent close and against the adapter's documented
// self-close on failure.
func (e *Exporter) handleConn(ctx context.Context, conn net.Conn) {
	closer := &connCloser{conn: conn}

	defer func() {
		err := closer.close()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			e.logger.Debug("exporter session close",
				slog.Any("err", err))
		}
	}()

	stopTimeout := e.armHandshakeTimeout(closer)
	defer stopTimeout()

	reader := newHandshakeLimitReader(conn, e.cfg.maxHandshakeBytes)

	_, op, _, err := e.codec.DecodeHeader(reader)
	if err != nil {
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
	case wire.OpReqImport:
		e.serveImport(ctx, reader, conn, stopTimeout)
	case wire.OpRepDevlist, wire.OpRepImport:
		stopTimeout()
		// Reply opcodes arriving on an accepted connection indicate a
		// misbehaving peer (or a reversed-role misconfiguration).
		e.logger.Debug("exporter received reply opcode on accept side",
			slog.Any("opcode", op))
	default:
		stopTimeout()
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
//
// The clock.After call is issued on the CALLER goroutine BEFORE the
// watcher is spawned so the deadline is registered on the Clock before
// armHandshakeTimeout returns. Test code routinely calls
// clk.Advance(handshakeTimeout + …) the moment a connection is
// accepted; under the previous arrangement (After inside the spawned
// goroutine) the Advance could beat the watcher to the Clock, leaving
// the pending list empty — the watcher would register its deadline
// against an already-advanced Now and never fire, making the
// handshake-timeout tests flaky under -race -count=N.
func (e *Exporter) armHandshakeTimeout(closer *connCloser) func() {
	if e.cfg.handshakeTimeout <= 0 {
		return func() {}
	}

	stop := make(chan struct{})
	stopped := make(chan struct{})

	// Register the handshake deadline on the caller goroutine so the
	// pending list is guaranteed to contain our timer the instant this
	// function returns. The channel is captured by the watcher below.
	timerCh := e.clock.After(e.cfg.handshakeTimeout)

	e.sessionsWG.Go(func() {
		defer close(stopped)

		select {
		case <-timerCh:
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
// handleConn's deferred cleanup. Emits the accept counter on every
// terminal transition so dashboards see the same count for devlist
// handshakes as for import handshakes.
func (e *Exporter) serveDevlist(ctx context.Context, _ io.Reader, conn net.Conn) {
	devs, err := e.kernel.ListExportedDevices(ctx)
	if err != nil {
		e.logger.Warn("exporter list exported devices",
			slog.String("op", string(HandshakeOpDevlist)),
			slog.String("outcome", string(OutcomeHandshakeFailed)),
			slog.Any("err", err))

		return
	}

	err = e.codec.EncodeOpRepDevlist(conn, devs)
	if err != nil {
		e.logger.Warn("exporter encode devlist reply",
			slog.String("op", string(HandshakeOpDevlist)),
			slog.String("outcome", string(OutcomeHandshakeFailed)),
			slog.Any("err", err))

		return
	}

	e.logger.Debug("exporter devlist served",
		slog.String("op", string(HandshakeOpDevlist)),
		slog.String("outcome", string(OutcomeHandshakeOK)),
		slog.Int("device_count", len(devs)))
}

// serveImport handles the OP_REQ_IMPORT request. It decodes the busid
// body, registers the session (enforcing MaxSessions + per-peer cap),
// then calls ExportOnConn. handleConn's deferred connCloser.close()
// fires on every return path; sync.Once guards against double-close
// (handshake-timeout watcher, failure-path adapter self-close). The
// accepted fd is released after ExportOnConn returns regardless of
// outcome — per v1 contract §5.4 the kernel holds its own dup on success and
// the app's remaining ref must be released so only the kernel's ref
// keeps the socket alive.
//
// stopTimeout is the handshake-timeout disarm callback; it is invoked
// AFTER DecodeOpReqImport completes so a stalled body-decode still
// fires the handshake deadline.
//
// handshakeStart is the wall-clock instant the accept loop dispatched
// this conn to the handler; serveImport uses it to emit the
// usbip_exporter_handshake_duration_seconds sample at the handshake-
// complete boundary. Every terminal return path observes the metric so
// the histogram's import-label samples cover failed handshakes too;
// the success branch observes it the moment ExportOnConn returns,
// BEFORE the handler parks on waitForSessionEnd. Observing after
// serveImport returns would make the histogram record the full session
// lifetime rather than the handshake.
//
// After ExportOnConn returns success the kernel owns the fd but the
// session is still live; the real sysfs write completes immediately
// and the kernel carries the session for its actual duration. The
// handler MUST NOT exit yet — an early return would fire the deferred
// endSession and unregister the session, leaving Sessions() empty
// while the device is still exported. waitForSessionEnd blocks until
// the kernel signals session end via a matching uevent, Shutdown
// signals handle.done, or ctx cancels.
func (e *Exporter) serveImport(
	ctx context.Context, reader io.Reader, conn net.Conn, stopTimeout func(),
) {
	// Body-only decode: handleConn already consumed the 8-byte header
	// to dispatch to serveImport. Calling DecodeOpReqImport here would
	// re-read 8 bytes from the busid region and surface ErrProtocolMismatch
	// with the busid's first two bytes ("3-" -> 0x332d) as the bogus version.
	busID, err := e.codec.DecodeOpReqImportBody(reader)

	// Disarm the handshake deadline only once the full handshake read
	// has completed — successful or not. If we leave the watcher armed
	// past this point the long-running ExportOnConn call would be torn
	// down when the clock ticks forward.
	stopTimeout()

	if err != nil {
		e.logger.Warn("exporter decode import request",
			slog.String("op", string(HandshakeOpImport)),
			slog.String("outcome", string(OutcomeHandshakeFailed)),
			slog.Any("err", err))

		return
	}

	sess, err := e.buildSession(conn, busID)
	if err != nil {
		e.logger.Warn("exporter build session",
			slog.String("op", string(HandshakeOpImport)),
			slog.String("outcome", string(OutcomeHandshakeFailed)),
			slog.Any("err", err))

		return
	}

	peerKey := peerKeyFromAddr(conn.RemoteAddr())

	handle, err := e.registerSession(sess, peerKey, conn)
	if err != nil {
		e.logger.Warn("exporter session declined",
			slog.Any("busid", busID),
			slog.String("peer", peerKey),
			slog.String("op", string(HandshakeOpImport)),
			slog.String("outcome", string(classifySessionDeclineOutcome(err))),
			slog.Any("err", err))

		return
	}

	e.runRegisteredSession(ctx, conn, busID, handle)
}

// classifySessionDeclineOutcome maps a registerSession failure to the
// closed SessionOutcome set. The cap sentinels are the only errors
// registerSession can return at the failure branch this helper feeds;
// ACL and rate-limit declines happen earlier in acceptLoop, before
// registerSession runs, and emit OutcomeRejectedACL / OutcomeRejectedRate
// directly at those sites in exporter.go. Anything else falls through
// to handshake_failed so the closed-set contract is preserved.
func classifySessionDeclineOutcome(err error) SessionOutcome {
	if errors.Is(err, ErrMaxSessionsExceeded) || errors.Is(err, ErrPerPeerLimitExceeded) {
		return OutcomeRejectedCap
	}

	return OutcomeHandshakeFailed
}

// runRegisteredSession executes the post-registration session lifecycle:
// emit SessionStarted, open the KernelEvents subscription BEFORE handing
// the fd to the kernel, call ExportOnConn, then park on
// waitForSessionEnd. Extracted from serveImport to keep the parent
// function below the funlen cap while preserving every ordering
// invariant documented above.
func (e *Exporter) runRegisteredSession(
	ctx context.Context,
	conn net.Conn,
	busID domain.BusID,
	handle *sessionHandle,
) {
	// Successful registration: count the accept BEFORE ExportOnConn
	// because the kernel call may block for the session's entire
	// lifetime. Deferring the increment until ExportOnConn returns
	// would hide live sessions from the accepted_total counter.

	// Publish SessionStarted AFTER register (under its own lock) and
	// BEFORE ExportOnConn blocks. Using a defer for SessionEnded binds
	// emission to handler exit regardless of the kernel-call outcome.
	e.publishSessionEvent(domain.SessionStartedEvent{
		At:      e.clock.Now(),
		Session: handle.session,
	})

	e.logger.Info("exporter session accepted",
		slog.Any("busid", busID),
		slog.String("session_id", handle.session.ID.String()),
		slog.String("op", string(HandshakeOpImport)),
		slog.String("outcome", string(OutcomeHandshakeOK)))

	defer e.endSession(handle, "handler exited")

	// Subscribe to KernelEvents BEFORE ExportOnConn.
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
		// Subscribe failure is the same observability class as a
		// kernel-side error; record the typed reason BEFORE returning
		// so the deferred endSession publishes a kernel_error
		// classification rather than the "handler exited" fallback.
		reason := string(DisconnectReasonKernelError)
		handle.disconnectReason.Store(&reason)

		e.logger.Warn("exporter session-end pre-subscribe failed",
			slog.Any("busid", busID),
			slog.String("disconnect_reason", reason),
			slog.Any("err", evErr))

		// Subscribe is the only observation path for session end; if
		// we cannot open one we must not hand the fd to the kernel
		// (we would park forever after a successful handoff). Surface
		// the subscribe failure the same way as a kernel-side error.
		return
	}

	defer cancelEvents()

	err := e.kernel.ExportOnConn(ctx, conn, busID)
	if err != nil {
		reason := string(classifyDisconnectReason(err))
		handle.disconnectReason.Store(&reason)

		if !errors.Is(err, context.Canceled) {
			e.logger.Warn("exporter export on conn",
				slog.Any("busid", busID),
				slog.String("disconnect_reason", reason),
				slog.Any("err", err))
		}

		return
	}

	// ExportOnConn returned success: the kernel accepted the fd, the
	// handshake is done. Park on waitForSessionEnd until the kernel
	// signals the session ended; the typed disconnect reason is used
	// to override the deferred endSession's free-form "handler exited"
	// fallback so journald carries the closed-set classification.
	reason := string(e.waitForSessionEnd(ctx, busID, handle, events))
	handle.disconnectReason.Store(&reason)
}

// waitForSessionEnd blocks the post-ExportOnConn handler until the
// kernel signals the session ended. Signals observed, in priority
// order:
//
//  1. handle.done closed — Shutdown is tearing the exporter down;
//     returns DisconnectReasonShutdown. Exporter.Shutdown cancels every
//     handle before draining sessionsWG, so the handler exits promptly.
//  2. ctx cancelled — same treatment as handle.done.
//  3. A KernelEvents event whose busid matches busID:
//     - PortDetachedEvent: kernel published a `remove`-action uevent
//     for the exported device — returns DisconnectReasonClientGone
//     because the remote client's detach drove the signal.
//     - DeviceUnboundEvent: local unbind of the busid — same treatment.
//
// The subscription is opened by serveImport BEFORE kernel.ExportOnConn
// so a detach uevent published in the gap between "kernel took the
// fd" and the first post-ExportOnConn instruction is buffered in the
// channel rather than lost. waitForSessionEnd consumes the pre-opened
// events channel; the subscription cancel is owned by serveImport's
// defer.
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
				// Log at Warn so operators can distinguish this
				// disconnect-reason source from a real kernel adapter
				// failure surfaced by ExportOnConn.
				e.logger.Warn("session-end events channel closed unexpectedly",
					slog.Any("busid", busID))

				return DisconnectReasonKernelError
			}

			if eventEndsSessionForBusID(ev, busID) {
				return DisconnectReasonClientGone
			}
		}
	}
}

// eventEndsSessionForBusID returns true iff ev is a kernel-side signal
// that the exporter session for busID has ended. v1 contract §5.4
// says the kernel emits a `remove` uevent on the exported device's
// DEVPATH when the session tears down; the EventsAdapter's dispatcher
// turns that into a PortDetachedEvent or DeviceUnboundEvent depending
// on the SUBSYSTEM. Matching on BusID is sufficient — both events
// carry the busid verbatim and the handler's subscription is per-
// session so no cross-talk can masquerade as an end signal.
func eventEndsSessionForBusID(ev domain.Event, busID domain.BusID) bool {
	switch e := ev.(type) {
	case domain.PortDetachedEvent:
		return e.Port.BusID == busID
	case domain.DeviceUnboundEvent:
		return e.Device.BusID == busID
	}

	return false
}

// classifyDisconnectReason maps an ExportOnConn error terminator onto
// the §11.5.5 disconnect_total reason label. The caller invokes this
// only on the non-nil branch — successful ExportOnConn parks on
// waitForSessionEnd, which classifies via PortDetached / DeviceUnbound
// kernel events instead.
func classifyDisconnectReason(err error) DisconnectReason {
	if errors.Is(err, context.Canceled) {
		return DisconnectReasonShutdown
	}

	return DisconnectReasonKernelError
}

// endSession unregisters the session and publishes a SessionEnded
// event. Kept out of unregisterSession itself so the lock-holding
// bookkeeping call does not also trigger fan-out under the write lock
// (publishSessionEvent takes an RLock of the same mu, which would
// deadlock).
//
// reason is the FALLBACK reason supplied at defer time (typically
// "handler exited"). When waitForSessionEnd recorded a typed
// DisconnectReason on the handle BEFORE the deferred call fires,
// that closed-set value wins so journald carries the precise
// classification (graceful / client_gone / kernel_error / shutdown)
// rather than a free-form fallback.
func (e *Exporter) endSession(h *sessionHandle, reason string) {
	if typed := h.disconnectReason.Load(); typed != nil && *typed != "" {
		reason = *typed
	}

	e.unregisterSession(h.session.ID)

	e.publishSessionEvent(domain.SessionEndedEvent{
		At:      e.clock.Now(),
		Session: h.session,
		Reason:  reason,
	})

	e.logger.Info("exporter session ended",
		slog.Any("busid", h.session.BusID),
		slog.String("session_id", h.session.ID.String()),
		slog.String("disconnect_reason", reason))
}

// buildSession assembles the domain.Session recorded for the accepted
// connection. The session id is UUIDv7 (chronologically sortable) per
// v1 contract §11.5.5. A failure to generate the id is a process-level
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
