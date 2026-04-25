// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

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

// errCoverageInjected is a sentinel returned by failingWriter / the
// truncated-read helpers used to drive error paths that are otherwise
// unreachable with in-memory buffers.
var errCoverageInjected = errors.New("injected write failure")

// failingWriter always returns errCoverageInjected from Write.
type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) { return 0, errCoverageInjected }

// shortReader returns n bytes of zeros once, then an arbitrary non-EOF
// error on subsequent Reads. Used to exercise the "non-EOF reader
// failure" branches in ReadPaddedString / DecodeDevice that io.EOF
// coverage cannot reach.
type shortReader struct {
	n   int
	err error
}

func (s *shortReader) Read(p []byte) (int, error) {
	if s.n == 0 {
		return 0, s.err
	}

	k := min(len(p), s.n)
	for i := range k {
		p[i] = 0
	}

	s.n -= k

	return k, nil
}

// TestWritePaddedStringWriterError drives the Write-error branch.
func TestWritePaddedStringWriterError(t *testing.T) {
	t.Parallel()

	err := wire.WritePaddedString(failingWriter{}, "1-1", domain.BusIDSize)
	require.ErrorIs(t, err, errCoverageInjected)
}

// TestReadPaddedStringNonEOFReaderError drives the non-EOF branch of
// ReadPaddedString's read-error handling.
func TestReadPaddedStringNonEOFReaderError(t *testing.T) {
	t.Parallel()

	_, _, err := wire.ReadPaddedString(&shortReader{err: errCoverageInjected}, domain.BusIDSize)
	require.ErrorIs(t, err, errCoverageInjected)
}

// TestDecodeDeviceNonEOFReaderError drives DecodeDevice's non-EOF
// read-error branch.
func TestDecodeDeviceNonEOFReaderError(t *testing.T) {
	t.Parallel()

	_, _, err := wire.DecodeDevice(&shortReader{err: errCoverageInjected})
	require.ErrorIs(t, err, errCoverageInjected)
}

// TestEncodeDeviceWriterError covers EncodeDevice's tail-write failure.
func TestEncodeDeviceWriterError(t *testing.T) {
	t.Parallel()

	err := wire.EncodeDevice(failingWriter{}, domain.Device{})
	require.ErrorIs(t, err, errCoverageInjected)
}

// TestEncodeOpRepDevlistHeaderWriteError covers the header-write branch.
func TestEncodeOpRepDevlistHeaderWriteError(t *testing.T) {
	t.Parallel()

	err := wire.EncodeOpRepDevlist(failingWriter{}, nil)
	require.ErrorIs(t, err, errCoverageInjected)
}

// TestEncodeOpRepDevlistCountWriteError covers the count-write branch.
// It uses a writer that succeeds once (header) and fails on the second
// write (count).
func TestEncodeOpRepDevlistCountWriteError(t *testing.T) {
	t.Parallel()

	w := &nthFailWriter{failOn: 2}

	err := wire.EncodeOpRepDevlist(w, nil)
	require.ErrorIs(t, err, errCoverageInjected)
}

// TestEncodeOpRepDevlistDeviceEncodeError covers the per-device
// encode failure path.
func TestEncodeOpRepDevlistDeviceEncodeError(t *testing.T) {
	t.Parallel()

	// Busid overflow forces EncodeDevice → ErrBusIDInvalid.
	dev := domain.Device{
		BusID: domain.BusID(bytes.Repeat([]byte{'a'}, domain.BusIDSize)),
	}

	var buf bytes.Buffer

	err := wire.EncodeOpRepDevlist(&buf, []domain.Device{dev})
	require.ErrorIs(t, err, domain.ErrBusIDInvalid)
}

// TestEncodeOpRepDevlistInterfaceWriteError covers encodeInterfaces's
// Write-error path.
func TestEncodeOpRepDevlistInterfaceWriteError(t *testing.T) {
	t.Parallel()

	dev := domain.Device{
		Path:          "/sys/a",
		BusID:         domain.BusID("1-1"),
		NumInterfaces: 1,
	}

	// Header (8) + count (4) + device (312) = 324. Fail on the 4th Write
	// call (the interface descriptor).
	w := &nthFailWriter{failOn: 4}

	err := wire.EncodeOpRepDevlist(w, []domain.Device{dev})
	require.ErrorIs(t, err, errCoverageInjected)
}

// TestEncodeOpReqImportHeaderWriteError covers the header-write path.
func TestEncodeOpReqImportHeaderWriteError(t *testing.T) {
	t.Parallel()

	err := wire.EncodeOpReqImport(failingWriter{}, domain.BusID("1-1"))
	require.ErrorIs(t, err, errCoverageInjected)
}

// TestEncodeOpRepImportHeaderWriteError covers the header-write path.
func TestEncodeOpRepImportHeaderWriteError(t *testing.T) {
	t.Parallel()

	err := wire.EncodeOpRepImport(failingWriter{}, domain.Device{})
	require.ErrorIs(t, err, errCoverageInjected)
}

// TestDecodeOpReqImportOpcodeMismatch exercises the branch that guards
// against an OP_REP_* landing where OP_REQ_IMPORT was expected.
func TestDecodeOpReqImportOpcodeMismatch(t *testing.T) {
	t.Parallel()

	// Feed an OP_REP_IMPORT header where OP_REQ_IMPORT was expected.
	buf := bytes.NewBuffer(wire.EncodeHeader(wire.OpRepImport, 0))

	_, err := wire.DecodeOpReqImport(buf)
	require.ErrorIs(t, err, domain.ErrProtocolMismatch)
}

// TestDecodeOpRepImportOpcodeMismatch exercises the opposite guard.
func TestDecodeOpRepImportOpcodeMismatch(t *testing.T) {
	t.Parallel()

	// OP_REP_DEVLIST is a valid opcode that isn't OP_REP_IMPORT.
	buf := bytes.NewBuffer(wire.EncodeHeader(wire.OpRepDevlist, 0))

	_, _, err := wire.DecodeOpRepImport(buf)
	require.ErrorIs(t, err, domain.ErrProtocolMismatch)
}

// TestDecodeOpRepDevlistCountTruncated covers the count-u32 truncation
// path.
func TestDecodeOpRepDevlistCountTruncated(t *testing.T) {
	t.Parallel()

	// Header + 2 bytes of an intended u32 count → truncated.
	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))
	buf.Write([]byte{0, 0})

	_, _, err := wire.DecodeOpRepDevlist(&buf)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

// TestDecodeOpRepDevlistOpcodeMismatch: a request opcode where a
// reply was expected.
func TestDecodeOpRepDevlistOpcodeMismatch(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpReqDevlist, 0))

	_, _, err := wire.DecodeOpRepDevlist(&buf)
	require.ErrorIs(t, err, domain.ErrProtocolMismatch)
}

// TestCodecForwardsHeader exercises the Codec.EncodeHeader and
// Codec.DecodeHeader wrappers for coverage.
func TestCodecForwardsHeader(t *testing.T) {
	t.Parallel()

	c := wire.NewCodec()

	got := c.EncodeHeader(wire.OpReqImport, 0)
	require.Equal(t, wire.EncodeHeader(wire.OpReqImport, 0), got)

	ver, op, status, err := c.DecodeHeader(bytes.NewReader(got))
	require.NoError(t, err)
	require.Equal(t, domain.ProtocolVersion, ver)
	require.Equal(t, wire.OpReqImport, op)
	require.Equal(t, uint32(0), status)
}

// nthFailWriter succeeds for the first failOn-1 Write calls and returns
// errCoverageInjected on the nth call.
type nthFailWriter struct {
	count  int
	failOn int
}

func (n *nthFailWriter) Write(p []byte) (int, error) {
	n.count++
	if n.count == n.failOn {
		return 0, errCoverageInjected
	}

	return len(p), nil
}

// TestDecodeDeviceBusnumOverflow forces the busnum u32→u16 guard.
func TestDecodeDeviceBusnumOverflow(t *testing.T) {
	t.Parallel()

	buf := make([]byte, wire.DeviceWireSize)
	// Set busnum to 0x00010000, which overflows uint16.
	buf[288] = 0x00
	buf[289] = 0x01
	buf[290] = 0x00
	buf[291] = 0x00

	_, _, err := wire.DecodeDevice(bytes.NewReader(buf))
	require.Error(t, err)
	require.Contains(t, err.Error(), "busnum")
}

// TestDecodeDeviceBusnumAtMaxU16 is a boundary test: 0xFFFF fits in a
// uint16 (the guard uses strict `>`, so this value must pass). Kills
// the CONDITIONALS_BOUNDARY mutant for layout.go busnum check.
func TestDecodeDeviceBusnumAtMaxU16(t *testing.T) {
	t.Parallel()

	buf := make([]byte, wire.DeviceWireSize)

	buf[288] = 0x00
	buf[289] = 0x00
	buf[290] = 0xFF
	buf[291] = 0xFF

	dev, _, err := wire.DecodeDevice(bytes.NewReader(buf))
	require.NoError(t, err)
	require.Equal(t, uint16(0xFFFF), dev.BusNum)
}

// TestDecodeDeviceDevnumAtMaxU16 is a boundary test for devnum. Kills
// the CONDITIONALS_BOUNDARY mutant for layout.go devnum check.
func TestDecodeDeviceDevnumAtMaxU16(t *testing.T) {
	t.Parallel()

	buf := make([]byte, wire.DeviceWireSize)

	buf[292] = 0x00
	buf[293] = 0x00
	buf[294] = 0xFF
	buf[295] = 0xFF

	dev, _, err := wire.DecodeDevice(bytes.NewReader(buf))
	require.NoError(t, err)
	require.Equal(t, uint16(0xFFFF), dev.DevNum)
}

// TestDecodeDeviceDevnumOverflow forces the devnum u32→u16 guard.
func TestDecodeDeviceDevnumOverflow(t *testing.T) {
	t.Parallel()

	buf := make([]byte, wire.DeviceWireSize)
	// Set devnum to 0x00010000 (overflow); busnum stays 0.
	buf[292] = 0x00
	buf[293] = 0x01
	buf[294] = 0x00
	buf[295] = 0x00

	_, _, err := wire.DecodeDevice(bytes.NewReader(buf))
	require.Error(t, err)
	require.Contains(t, err.Error(), "devnum")
}

// failAtReader returns buf bytes on the first read, then errCoverageInjected
// on all subsequent reads — drives the "non-EOF error" branch of
// decodeInterfaces when the device read succeeds but interface read fails
// with a non-EOF error.
type failAtReader struct {
	buf    []byte
	off    int
	failAt int
	err    error
}

func (f *failAtReader) Read(p []byte) (int, error) {
	if f.off >= f.failAt {
		return 0, f.err
	}

	remaining := f.failAt - f.off
	n := min(len(p), remaining, len(f.buf)-f.off)

	copy(p, f.buf[f.off:f.off+n])

	f.off += n

	return n, nil
}

// TestDecodeInterfacesNonEOFReadError drives decodeInterfaces's
// non-EOF read-error branch.
func TestDecodeInterfacesNonEOFReadError(t *testing.T) {
	t.Parallel()

	// Build a valid header + count=1 + full device with NumInterfaces=1,
	// then fail on the interface-descriptor read.
	var payload bytes.Buffer

	payload.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))
	payload.Write([]byte{0, 0, 0, 1})

	dev := domain.Device{
		Path:          "/sys/x",
		BusID:         domain.BusID("1-1"),
		NumInterfaces: 1,
	}

	require.NoError(t, wire.EncodeDevice(&payload, dev))

	r := &failAtReader{
		buf:    payload.Bytes(),
		failAt: payload.Len(),
		err:    errCoverageInjected,
	}

	_, _, err := wire.DecodeOpRepDevlist(r)
	require.ErrorIs(t, err, errCoverageInjected)
}

// TestDecodeDevlistBodyNonEOFDeviceError drives decodeDevlistBody's
// non-EOF decode-device error branch (busnum overflow mid-list).
func TestDecodeDevlistBodyNonEOFDeviceError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))
	buf.Write([]byte{0, 0, 0, 1})

	// Write a full 312-byte descriptor with busnum overflowed.
	bad := make([]byte, wire.DeviceWireSize)
	// busnum offset 288
	bad[288] = 0x01

	buf.Write(bad)

	_, _, err := wire.DecodeOpRepDevlist(&buf)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode device at index 0")
}

// TestDecodeOpRepDevlistCountReadNonEOFError drives wrapUnexpectedEOF's
// non-EOF branch at the count-read step.
func TestDecodeOpRepDevlistCountReadNonEOFError(t *testing.T) {
	t.Parallel()

	// Build header bytes then a reader that fails with a non-EOF error
	// on the count read.
	header := wire.EncodeHeader(wire.OpRepDevlist, 0)

	r := &failAtReader{
		buf:    header,
		failAt: len(header),
		err:    errCoverageInjected,
	}

	_, _, err := wire.DecodeOpRepDevlist(r)
	require.ErrorIs(t, err, errCoverageInjected)
}

// TestEncodeOpRepImportDeviceError exercises EncodeOpRepImport's
// device-encode failure path (BusID overflow).
func TestEncodeOpRepImportDeviceError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	dev := domain.Device{
		BusID: domain.BusID(bytes.Repeat([]byte{'a'}, domain.BusIDSize)),
	}

	err := wire.EncodeOpRepImport(&buf, dev)
	require.ErrorIs(t, err, domain.ErrBusIDInvalid)
}

// TestEncodeOpReqImportBusIDWriteFlow exercises EncodeOpReqImport's
// busid-write path when the header succeeds but the padded-string
// write fails.
func TestEncodeOpReqImportBusIDWriteFlow(t *testing.T) {
	t.Parallel()

	// Fail on the 2nd Write call (busid).
	w := &nthFailWriter{failOn: 2}

	err := wire.EncodeOpReqImport(w, domain.BusID("1-1"))
	require.ErrorIs(t, err, errCoverageInjected)
}

// TestDecodeOpReqImportBusIDReadError exercises DecodeOpReqImport's
// busid-read failure path.
func TestDecodeOpReqImportBusIDReadError(t *testing.T) {
	t.Parallel()

	// Provide header but no busid bytes.
	buf := bytes.NewBuffer(wire.EncodeHeader(wire.OpReqImport, 0))

	_, err := wire.DecodeOpReqImport(buf)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}
