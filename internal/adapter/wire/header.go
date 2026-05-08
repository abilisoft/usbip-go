// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// headerSize is the OP header byte length (v1 contract §6.2 layout table).
const headerSize = 8

// Byte offsets inside the OP header (v1 contract §6.2).
const (
	offHdrVersion = 0
	offHdrOpCode  = 2
	offHdrStatus  = 4
)

// EncodeHeader returns the 8-byte OP header for op with the given
// status. The returned slice has length headerSize and is owned by the
// caller. Layout (big-endian):
//
//	u16 version (ProtocolVersion)
//	u16 opcode
//	u32 status
func EncodeHeader(op OpCode, status uint32) []byte {
	buf := make([]byte, headerSize)
	binary.BigEndian.PutUint16(buf[offHdrVersion:], domain.ProtocolVersion)
	binary.BigEndian.PutUint16(buf[offHdrOpCode:], uint16(op))
	binary.BigEndian.PutUint32(buf[offHdrStatus:], status)

	return buf
}

// DecodeHeader reads an 8-byte OP header from r. Per v1 contract §6.2 error
// matrix:
//
//   - Clean EOF before any byte is read → io.EOF (unwrapped).
//   - Partial header → io.ErrUnexpectedEOF wrapped.
//   - Version != ProtocolVersion → ErrProtocolMismatch.
//   - Unknown opcode → ErrProtocolMismatch.
//   - Status != 0 on a reply → ErrProtocolError.
//
// OP_REP_IMPORT is the narrow exception to the status-non-zero rule:
// the spec treats a non-zero OP_REP_IMPORT status as the peer saying
// "device unavailable / busy / not found" (a domain-level rejection),
// not a wire framing fault. DecodeOpRepImport calls decodeHeaderAllowStatus
// directly and classifies the status itself. Other reply opcodes keep
// the ErrProtocolError surface for malformed-reply detection.
//
// The 4-tuple return is dictated by the spec-level codec surface:
// callers need both the raw version (for diagnostics) and the validated
// opcode + status.
func DecodeHeader(r io.Reader) (uint16, OpCode, uint32, error) {
	version, op, status, err := decodeHeaderAllowStatus(r)
	if err != nil {
		return 0, 0, 0, err
	}

	if status != 0 && isReplyOpCode(op) {
		return 0, 0, 0, fmt.Errorf("%w: reply opcode 0x%04x status=%d",
			domain.ErrProtocolError, uint16(op), status)
	}

	return version, op, status, nil
}

// decodeHeaderAllowStatus performs the v1 contract §6.2 header decode WITHOUT
// the "reply status != 0" rejection. Callers with opcode-specific
// status semantics (OP_REP_IMPORT) invoke this variant and classify
// status themselves; every other caller goes through the public
// DecodeHeader wrapper.
func decodeHeaderAllowStatus(r io.Reader) (uint16, OpCode, uint32, error) {
	buf := make([]byte, headerSize)

	n, err := io.ReadFull(r, buf)
	if err != nil {
		return 0, 0, 0, mapHeaderReadErr(n, err)
	}

	version := binary.BigEndian.Uint16(buf[offHdrVersion:])
	op := OpCode(binary.BigEndian.Uint16(buf[offHdrOpCode:]))
	status := binary.BigEndian.Uint32(buf[offHdrStatus:])

	if version != domain.ProtocolVersion {
		return 0, 0, 0, fmt.Errorf("%w: got version 0x%04x, want 0x%04x",
			domain.ErrProtocolMismatch, version, domain.ProtocolVersion)
	}

	if !isKnownOpCode(op) {
		return 0, 0, 0, fmt.Errorf("%w: unknown opcode 0x%04x",
			domain.ErrProtocolMismatch, uint16(op))
	}

	return version, op, status, nil
}

// mapHeaderReadErr classifies the io.ReadFull error into the v1 contract §6.2
// error matrix rows for OP header reads. Clean EOF on byte 0 is
// returned unwrapped; short read becomes a wrapped ErrUnexpectedEOF.
func mapHeaderReadErr(n int, err error) error {
	if n == 0 && errors.Is(err, io.EOF) {
		return io.EOF
	}

	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return fmt.Errorf("read op header: %w", io.ErrUnexpectedEOF)
	}

	return fmt.Errorf("read op header: %w", err)
}
