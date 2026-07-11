// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"fmt"
	"io"

	"github.com/abilisoft/usbip-go/internal/protocol"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// EncodeOpReqImport writes an OP_REQ_IMPORT request for the supplied
// busid: 8-byte header + 32-byte NUL-padded busid (v1 contract §6.2).
func EncodeOpReqImport(w io.Writer, busID domain.BusID) error {
	header := EncodeHeader(OpReqImport, 0)

	_, err := w.Write(header)
	if err != nil {
		return fmt.Errorf("write OP_REQ_IMPORT header: %w", err)
	}

	err = WritePaddedString(w, string(busID), domain.BusIDSize)
	if err != nil {
		return err
	}

	return nil
}

// DecodeOpReqImport reads a full OP_REQ_IMPORT request (header +
// body) and returns the requested busid. Used by callers that own
// the read of the entire request from raw bytes.
//
// Daemons whose dispatcher already consumed the header MUST call
// DecodeOpReqImportBody instead — calling this function would
// re-read 8 bytes from the busid region and surface them as a
// (bogus) version mismatch.
func DecodeOpReqImport(r io.Reader) (domain.BusID, error) {
	_, op, _, err := DecodeHeader(r)
	if err != nil {
		return "", err
	}

	if op != OpReqImport {
		return "", fmt.Errorf("%w: expected OP_REQ_IMPORT got 0x%04x",
			domain.ErrProtocolMismatch, uint16(op))
	}

	return DecodeOpReqImportBody(r)
}

// DecodeOpReqImportBody reads ONLY the OP_REQ_IMPORT body (the
// 32-byte busid) from r. The 8-byte header must already have been
// consumed by the caller — this function does NOT re-read it.
//
// Body-only entry point so the daemon's accept dispatcher (which
// already decoded the header to route the connection) does not
// double-read the header and mis-decode the busid as a header.
func DecodeOpReqImportBody(r io.Reader) (domain.BusID, error) {
	busid, truncated, err := ReadPaddedString(r, domain.BusIDSize)
	if err != nil {
		return "", err
	}

	// A truncated busid field means ReadPaddedString stopped at a NUL
	// or non-printable byte with junk still following (the field was
	// not well-formed NUL padding). Surfacing the printable prefix
	// silently would let a peer pre-load a valid-looking busid in front
	// of arbitrary bytes, misleading operator logs and every sysfs
	// helper downstream.
	if truncated {
		return "", fmt.Errorf("decode OP_REQ_IMPORT: %w: busid field is not NUL-padded",
			domain.ErrBusIDInvalid)
	}

	// Validate against the sysfs-safe charset so a peer supplying
	// whitespace, control bytes, or path separators is rejected with
	// ErrBusIDInvalid here, well before the sysfs layer receives it.
	// The stricter topology pattern belongs at the user-input boundary
	// (ParseBusID); the wire rule stays permissive enough to accept
	// real-world shapes like "usbip-vudc.0" while blocking basename
	// escape bytes.
	parsed, err := domain.ValidateWireBusID(busid)
	if err != nil {
		return "", fmt.Errorf("decode OP_REQ_IMPORT: %w", err)
	}

	return parsed, nil
}

// EncodeOpRepImport writes a success OP_REP_IMPORT reply (status=0)
// with the device body (v1 contract §6.2).
func EncodeOpRepImport(w io.Writer, dev domain.Device) error {
	header := EncodeHeader(OpRepImport, 0)

	_, err := w.Write(header)
	if err != nil {
		return fmt.Errorf("write OP_REP_IMPORT header: %w", err)
	}

	err = EncodeDevice(w, dev)
	if err != nil {
		return err
	}

	return nil
}

// EncodeOpRepImportError writes an error OP_REP_IMPORT reply (status != 0,
// no device body) per v1 contract §6.2. status MUST be one of the upstream
// ST_* codes (ST_NA=1, ST_DEV_BUSY=2, ST_DEV_ERR=3, ST_NODEV=4). A zero
// status would let the peer decode a body that the wire frame does not
// carry; an unknown status would surface as ErrProtocolError on the
// importer side and obscure the rejection. Both are rejected at encode.
func EncodeOpRepImportError(w io.Writer, status uint32) error {
	switch status {
	case ImportStatusNA, ImportStatusDevBusy, ImportStatusDevErr, ImportStatusNoDev:
	default:
		return fmt.Errorf("%w: EncodeOpRepImportError invalid status %d",
			domain.ErrProtocolError, status)
	}

	header := EncodeHeader(OpRepImport, status)

	_, err := w.Write(header)
	if err != nil {
		return fmt.Errorf("write OP_REP_IMPORT error header: %w", err)
	}

	return nil
}

// DecodeOpRepImport reads an OP_REP_IMPORT reply. Per v1 contract §6.2 the
// header's status field means "device unavailable / busy / not found"
// on this opcode — a domain-level rejection, not a wire framing fault.
// A non-zero status surfaces as domain.ErrDeviceNotFound so the
// Importer.Attach caller sees the canonical rejection sentinel (RANK
// 5). A zero status returns the decoded device body.
//
// Because of that opcode-specific semantic, DecodeOpRepImport calls
// the internal decodeHeaderAllowStatus helper directly — DecodeHeader
// would convert a non-zero status into ErrProtocolError and hide the
// rejection behind a misleading classification.
func DecodeOpRepImport(r io.Reader) (domain.Device, DecodeFlags, error) {
	_, op, status, err := decodeHeaderAllowStatus(r)
	if err != nil {
		return domain.Device{}, DecodeFlags{}, err
	}

	if op != OpRepImport {
		return domain.Device{}, DecodeFlags{},
			fmt.Errorf("%w: expected OP_REP_IMPORT got 0x%04x",
				domain.ErrProtocolMismatch, uint16(op))
	}

	if status != 0 {
		return domain.Device{}, DecodeFlags{},
			fmt.Errorf("%w: OP_REP_IMPORT status=%d",
				mapImportStatus(status), status)
	}

	dev, flags, err := DecodeDevice(r)
	if err != nil {
		return domain.Device{}, DecodeFlags{}, err
	}

	return dev, flags, nil
}

// OP_REP_IMPORT status codes from upstream tools/usb/usbip/libsrc/
// usbip_common.h. Exported so app-layer code can encode the
// appropriate error on the exporter side without redeclaring the
// upstream protocol constants.
//
//	ST_OK         = 0  // success (encoded as the absence of an error)
//	ST_NA         = 1  // device not exported / unknown
//	ST_DEV_BUSY   = 2  // already in use by another importer
//	ST_DEV_ERR    = 3  // stub-side internal error
//	ST_NODEV      = 4  // no such device on remote
const (
	ImportStatusNA      = protocol.ImportStatusNA
	ImportStatusDevBusy = protocol.ImportStatusDevBusy
	ImportStatusDevErr  = protocol.ImportStatusDevErr
	ImportStatusNoDev   = protocol.ImportStatusNoDev
)

// mapImportStatus converts a non-zero OP_REP_IMPORT status to the
// matching domain sentinel. An unknown status is a wire-protocol
// violation — the spec defines exactly four error codes (ST_NA,
// ST_DEV_BUSY, ST_DEV_ERR, ST_NODEV); anything else surfaces as
// ErrProtocolError so the caller can distinguish "device not
// available" from "peer speaks a status code we don't understand".
func mapImportStatus(status uint32) error {
	switch status {
	case ImportStatusNA, ImportStatusNoDev:
		return domain.ErrDeviceNotFound
	case ImportStatusDevBusy:
		return domain.ErrDeviceAlreadyBound
	case ImportStatusDevErr:
		return domain.ErrDeviceUnavailable
	default:
		return domain.ErrProtocolError
	}
}
