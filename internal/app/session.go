package app

import (
	"context"
	"errors"
	"log/slog"
	"net"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
)

// handleConn is the per-connection entry point spawned by the accept
// loop. The handshake flow per spec §5.3:
//
//  1. Decode the OP header via the codec.
//  2. Dispatch on opcode:
//     - OP_REQ_DEVLIST → short-lived query: list devices, encode reply,
//     close conn.
//     - OP_REQ_IMPORT → long-lived session: decode busid, hand the fd
//     to the kernel via ExportOnConn, block until ExportOnConn
//     returns (which happens on session end).
//
// fd-passing contract (spec §5.4 item 4): the handler closes the conn
// on every error path before ExportOnConn returns success. Once
// ExportOnConn returns (whether success or failure), the handler MUST
// NOT close the conn itself — the kernel owns the fd on success, and
// the adapter documented to have closed it on failure. The handedOff
// flag implements this split.
func (e *Exporter) handleConn(ctx context.Context, conn net.Conn) {
	handedOff := false

	defer func() {
		if handedOff {
			return
		}

		err := conn.Close()
		if err != nil {
			e.logger.Debug("exporter session close",
				slog.Any("err", err))
		}
	}()

	_, op, _, err := e.codec.DecodeHeader(conn)
	if err != nil {
		e.logger.Debug("exporter decode header",
			slog.Any("err", err))

		return
	}

	switch op {
	case wire.OpReqDevlist:
		e.serveDevlist(ctx, conn)
	case wire.OpReqImport:
		handedOff = e.serveImport(ctx, conn)
	case wire.OpRepDevlist, wire.OpRepImport:
		// Reply opcodes arriving on an accepted connection indicate a
		// misbehaving peer (or a reversed-role misconfiguration).
		// Logged at Debug; the deferred close handles teardown.
		e.logger.Debug("exporter received reply opcode on accept side",
			slog.Any("opcode", op))
	default:
		e.logger.Debug("exporter unexpected opcode",
			slog.Any("opcode", op))
	}
}

// serveDevlist handles the OP_REQ_DEVLIST request. The handler calls
// ListAvailable (which forwards to kernel.ListLocalDevices) and writes
// the reply via the codec. The conn is closed by the deferred handler
// in handleConn; this function only reports success/failure via logs
// because the opcode is a short-lived query — no domain state is
// affected by the outcome.
func (e *Exporter) serveDevlist(ctx context.Context, conn net.Conn) {
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
// body, then calls ExportOnConn. Returns true when the fd was handed
// off to the kernel (success path) so handleConn's deferred close
// skips closing the conn — the kernel owns it at that point. Any
// error path returns false; the deferred handler then closes the
// conn per spec §5.4 item 4.
//
// The block until ExportOnConn returns IS the session-lifetime wait:
// the kernel adapter keeps the sysfs write open until the session ends
// (the kernel writes SDEV_EVENT_DOWN on teardown, at which point the
// adapter's blocking read on the sysfs side unblocks and returns).
// The accept loop's wg.Done on runSession return therefore reflects
// real session termination.
func (e *Exporter) serveImport(ctx context.Context, conn net.Conn) bool {
	busID, err := e.codec.DecodeOpReqImport(conn)
	if err != nil {
		e.logger.Warn("exporter decode import request",
			slog.Any("err", err))

		return false
	}

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

