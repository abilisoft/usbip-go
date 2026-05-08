package wire

import (
	"fmt"
	"io"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// EncodeOpReqImport writes an OP_REQ_IMPORT request for the supplied
// busid: 8-byte header + 32-byte NUL-padded busid (spec §6.2).
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

// DecodeOpReqImport reads an OP_REQ_IMPORT request and returns the
// requested busid.
func DecodeOpReqImport(r io.Reader) (domain.BusID, error) {
	_, op, _, err := DecodeHeader(r)
	if err != nil {
		return "", err
	}

	if op != OpReqImport {
		return "", fmt.Errorf("%w: expected OP_REQ_IMPORT got 0x%04x",
			domain.ErrProtocolMismatch, uint16(op))
	}

	busid, err := ReadPaddedString(r, domain.BusIDSize)
	if err != nil {
		return "", err
	}

	return domain.BusID(busid), nil
}

// EncodeOpRepImport writes a success OP_REP_IMPORT reply (status=0)
// with the device body (spec §6.2).
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

// DecodeOpRepImport reads an OP_REP_IMPORT reply. The decoder returns
// ErrProtocolError if the header's status field is non-zero (surfaced
// by DecodeHeader for reply opcodes). On success the device body is
// returned.
func DecodeOpRepImport(r io.Reader) (domain.Device, error) {
	_, op, _, err := DecodeHeader(r)
	if err != nil {
		return domain.Device{}, err
	}

	if op != OpRepImport {
		return domain.Device{}, fmt.Errorf("%w: expected OP_REP_IMPORT got 0x%04x",
			domain.ErrProtocolMismatch, uint16(op))
	}

	dev, err := DecodeDevice(r)
	if err != nil {
		return domain.Device{}, err
	}

	return dev, nil
}
