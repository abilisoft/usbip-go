package wire_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestOpCodeConstants pins spec §6.2 opcode numeric values.
func TestOpCodeConstants(t *testing.T) {
	t.Parallel()

	require.Equal(t, wire.OpCode(0x8005), wire.OpReqDevlist)
	require.Equal(t, wire.OpCode(0x0005), wire.OpRepDevlist)
	require.Equal(t, wire.OpCode(0x8003), wire.OpReqImport)
	require.Equal(t, wire.OpCode(0x0003), wire.OpRepImport)
}

// TestEncodeHeaderReqImport pins the exact byte layout from spec §6.2.
func TestEncodeHeaderReqImport(t *testing.T) {
	t.Parallel()

	got := wire.EncodeHeader(wire.OpReqImport, 0)
	want := []byte{0x01, 0x11, 0x80, 0x03, 0, 0, 0, 0}
	require.Equal(t, want, got)
}

// TestEncodeHeaderAllOpcodes round-trips every opcode via DecodeHeader.
func TestEncodeHeaderAllOpcodes(t *testing.T) {
	t.Parallel()

	ops := []wire.OpCode{
		wire.OpReqDevlist, wire.OpRepDevlist,
		wire.OpReqImport, wire.OpRepImport,
	}
	for _, op := range ops {
		buf := bytes.NewReader(wire.EncodeHeader(op, 0))

		ver, gotOp, status, err := wire.DecodeHeader(buf)
		require.NoError(t, err)
		require.Equal(t, domain.ProtocolVersion, ver)
		require.Equal(t, op, gotOp)
		require.Equal(t, uint32(0), status)
	}
}

// TestDecodeHeaderCleanEOF: spec §6.2 — clean EOF before any header
// byte returns io.EOF unchanged, not wrapped.
func TestDecodeHeaderCleanEOF(t *testing.T) {
	t.Parallel()

	_, _, _, err := wire.DecodeHeader(bytes.NewReader(nil))
	require.ErrorIs(t, err, io.EOF)
	// must be exactly io.EOF (not wrapped).
	require.Equal(t, io.EOF, err)
}

// TestDecodeHeaderShortRead: partial header → io.ErrUnexpectedEOF wrapped.
func TestDecodeHeaderShortRead(t *testing.T) {
	t.Parallel()

	// 3 bytes only.
	_, _, _, err := wire.DecodeHeader(bytes.NewReader([]byte{0x01, 0x11, 0x80}))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

// TestDecodeHeaderVersionMismatch: version != 0x0111 → ErrProtocolMismatch.
func TestDecodeHeaderVersionMismatch(t *testing.T) {
	t.Parallel()

	// Version 0x0112, opcode OpRepDevlist.
	buf := []byte{0x01, 0x12, 0x00, 0x05, 0, 0, 0, 0}

	_, _, _, err := wire.DecodeHeader(bytes.NewReader(buf))
	require.ErrorIs(t, err, domain.ErrProtocolMismatch)
}

// TestDecodeHeaderUnknownOpcode: unknown opcode → ErrProtocolMismatch.
func TestDecodeHeaderUnknownOpcode(t *testing.T) {
	t.Parallel()

	buf := []byte{0x01, 0x11, 0xFF, 0xFF, 0, 0, 0, 0}

	_, _, _, err := wire.DecodeHeader(bytes.NewReader(buf))
	require.ErrorIs(t, err, domain.ErrProtocolMismatch)
}

// TestDecodeHeaderReplyStatusNonZero: reply header with status != 0 →
// ErrProtocolError.
func TestDecodeHeaderReplyStatusNonZero(t *testing.T) {
	t.Parallel()

	// OpRepImport with status=5.
	buf := []byte{0x01, 0x11, 0x00, 0x03, 0, 0, 0, 5}

	_, _, _, err := wire.DecodeHeader(bytes.NewReader(buf))
	require.ErrorIs(t, err, domain.ErrProtocolError)
}

// TestDecodeHeaderRequestStatusIgnored: request opcodes don't trigger
// the status-non-zero check (requests always have status=0 by definition
// and the spec's error-matrix row is scoped to replies).
func TestDecodeHeaderRequestStatusIgnored(t *testing.T) {
	t.Parallel()

	// OpReqDevlist with status=0.
	buf := []byte{0x01, 0x11, 0x80, 0x05, 0, 0, 0, 0}

	_, op, status, err := wire.DecodeHeader(bytes.NewReader(buf))
	require.NoError(t, err)
	require.Equal(t, wire.OpReqDevlist, op)
	require.Equal(t, uint32(0), status)
}

// TestDecodeHeaderReaderError: non-EOF reader errors are surfaced wrapped.
func TestDecodeHeaderReaderError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("injected read failure")
	r := &failingReader{err: sentinel}

	_, _, _, err := wire.DecodeHeader(r)
	require.ErrorIs(t, err, sentinel)
}

// failingReader returns err on every Read.
type failingReader struct{ err error }

func (f *failingReader) Read(_ []byte) (int, error) { return 0, f.err }
