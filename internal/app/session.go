package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

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
// closed it on failure. The handedOff flag implements this split.
func (e *Exporter) handleConn(ctx context.Context, conn net.Conn) {
	handedOff := false

	defer func() {
		if handedOff {
			return
		}

		err := conn.Close()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			e.logger.Debug("exporter session close",
				slog.Any("err", err))
		}
	}()

	stopTimeout := e.armHandshakeTimeout(conn)
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
		handedOff = e.serveImport(ctx, reader, conn, stopTimeout)
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

// armHandshakeTimeout spawns a watcher that closes conn after the
// configured HandshakeTimeout elapses on the Exporter's injected Clock.
// Returns a stop func that disarms the watcher; callers MUST call it
// exactly once after the handshake completes (or the watcher goroutine
// leaks until the timeout fires). A non-positive timeout disables the
// watcher entirely.
func (e *Exporter) armHandshakeTimeout(conn net.Conn) func() {
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

			err := conn.Close()
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
		e.logger.Warn("exporter decode import request",
			slog.Any("err", err))

		return false
	}

	sess, err := e.buildSession(conn, busID)
	if err != nil {
		e.logger.Warn("exporter build session",
			slog.Any("err", err))

		return false
	}

	peerKey := peerKeyFromAddr(conn.RemoteAddr())

	handle, err := e.registerSession(sess, peerKey, conn)
	if err != nil {
		e.logger.Warn("exporter session declined",
			slog.Any("busid", busID),
			slog.String("peer", peerKey),
			slog.Any("err", err))

		return false
	}

	// Publish SessionStarted AFTER register (under its own lock) and
	// BEFORE ExportOnConn blocks. Using a defer for SessionEnded binds
	// emission to handler exit regardless of the kernel-call outcome.
	e.publishSessionEvent(domain.SessionStartedEvent{
		At:      e.clock.Now(),
		Session: handle.session,
	})

	defer e.endSession(handle, "handler exited")

	err = e.kernel.ExportOnConn(ctx, conn, busID)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			e.logger.Warn("exporter export on conn",
				slog.Any("busid", busID),
				slog.Any("err", err))
		}

		return false
	}

	return true
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
