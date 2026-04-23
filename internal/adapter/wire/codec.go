package wire

import (
	"io"
	"log/slog"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// Codec is the wire-level USBIP protocol codec. All methods forward to
// the package-level encode/decode helpers. Per spec §5.1 this type
// implements the app.ProtocolCodec interface; the compile-time
// assertion lives with the interface declaration in internal/app.
//
// Codec carries a *slog.Logger so permissive-read signals surfaced by
// the package-level helpers (e.g. trailing bytes after OP_REP_DEVLIST,
// spec §6.2) can be logged to a caller-controlled sink. Zero-value
// Codec{} is usable; its logger is a no-op handler so unit tests that
// construct Codec{} see no output. Inject a real logger via the
// WithLogger option.
type Codec struct {
	logger *slog.Logger
}

// Option configures a Codec.
type Option func(*Codec)

// WithLogger installs l as the Codec's logger. Passing nil selects
// the no-op handler.
func WithLogger(l *slog.Logger) Option {
	return func(c *Codec) {
		if l == nil {
			c.logger = noopLogger()

			return
		}

		c.logger = l
	}
}

// NewCodec constructs a Codec with no-op logging. Apply options to
// customise (currently only WithLogger).
func NewCodec(opts ...Option) *Codec {
	c := &Codec{logger: noopLogger()}
	for _, opt := range opts {
		opt(c)
	}

	return c
}

// noopLogger returns a *slog.Logger that discards all records.
// Using a discarding handler keeps the hot path allocation-free
// compared to nil-checks before every log call.
func noopLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

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

// DecodeOpRepDevlist calls the package-level decoder and emits a
// slog.Warn via the codec's logger when trailing bytes were observed
// (spec §6.2 permissive-read signal).
func (c *Codec) DecodeOpRepDevlist(r io.Reader) ([]domain.Device, error) {
	devices, trailing, err := DecodeOpRepDevlist(r)
	if err != nil {
		return nil, err
	}

	if trailing {
		c.log().Warn("trailing bytes after devlist")
	}

	return devices, nil
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

// log returns the codec's logger, initialising it to a no-op if
// the Codec was zero-value constructed.
func (c *Codec) log() *slog.Logger {
	if c.logger == nil {
		return noopLogger()
	}

	return c.logger
}
