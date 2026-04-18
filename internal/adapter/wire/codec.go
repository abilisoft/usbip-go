package wire

import (
	"io"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// Codec is the wire-level USBIP protocol codec. All methods forward to
// the package-level encode/decode helpers. Per spec §5.1 this type is
// intended to implement the (still-unreleased) app.ProtocolCodec
// interface once Phase 5 declares it; the compile-time assertion is
// therefore deferred.
type Codec struct{}

// NewCodec constructs a fresh Codec. The zero value is also usable.
func NewCodec() *Codec { return &Codec{} }

// EncodeHeader forwards to the package-level EncodeHeader.
func (*Codec) EncodeHeader(op OpCode, status uint32) []byte {
	return EncodeHeader(op, status)
}

// DecodeHeader forwards to the package-level DecodeHeader.
func (*Codec) DecodeHeader(r io.Reader) (uint16, OpCode, uint32, error) {
	return DecodeHeader(r)
}

// EncodeOpReqDevlist forwards to the package-level EncodeOpReqDevlist.
func (*Codec) EncodeOpReqDevlist() []byte { return EncodeOpReqDevlist() }

// EncodeOpRepDevlist forwards to the package-level EncodeOpRepDevlist.
func (*Codec) EncodeOpRepDevlist(w io.Writer, devices []domain.Device) error {
	return EncodeOpRepDevlist(w, devices)
}

// DecodeOpRepDevlist forwards to the package-level DecodeOpRepDevlist.
func (*Codec) DecodeOpRepDevlist(r io.Reader) ([]domain.Device, error) {
	return DecodeOpRepDevlist(r)
}

// EncodeOpReqImport forwards to the package-level EncodeOpReqImport.
func (*Codec) EncodeOpReqImport(w io.Writer, busID domain.BusID) error {
	return EncodeOpReqImport(w, busID)
}

// DecodeOpReqImport forwards to the package-level DecodeOpReqImport.
func (*Codec) DecodeOpReqImport(r io.Reader) (domain.BusID, error) {
	return DecodeOpReqImport(r)
}

// EncodeOpRepImport forwards to the package-level EncodeOpRepImport.
func (*Codec) EncodeOpRepImport(w io.Writer, dev domain.Device) error {
	return EncodeOpRepImport(w, dev)
}

// DecodeOpRepImport forwards to the package-level DecodeOpRepImport.
func (*Codec) DecodeOpRepImport(r io.Reader) (domain.Device, error) {
	return DecodeOpRepImport(r)
}
