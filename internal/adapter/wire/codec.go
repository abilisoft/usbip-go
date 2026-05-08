// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"io"
	"log/slog"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// Codec is the wire-level USBIP protocol codec. All methods forward to
// the package-level encode/decode helpers. Per v1 contract §5.1 this type
// implements the app.ProtocolCodec interface; the compile-time
// assertion lives with the interface declaration in internal/app.
//
// Codec carries a *slog.Logger so permissive-read signals surfaced by
// the package-level helpers (e.g. trailing bytes after OP_REP_DEVLIST,
// v1 contract §6.2) can be logged to a caller-controlled sink. Zero-value
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

// DecodeOpRepDevlist calls the package-level decoder, logs any
// advisory signals surfaced in DecodeFlags (trailing bytes after the
// declared frame, padded-string fields that reached end-of-field
// without NUL per §6.2), and returns the decoded devices without the
// flags — the app-facing interface stays narrow.
func (c *Codec) DecodeOpRepDevlist(r io.Reader) ([]domain.Device, error) {
	devices, flags, err := DecodeOpRepDevlist(r)
	if err != nil {
		return nil, err
	}

	c.logDecodeFlags("OP_REP_DEVLIST", flags)

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

// DecodeOpReqImportBody forwards to the package-level body-only
// decoder. Daemon dispatchers that already consumed the header use
// this to read just the busid without re-reading the 8 header bytes.
func (*Codec) DecodeOpReqImportBody(r io.Reader) (domain.BusID, error) {
	return DecodeOpReqImportBody(r)
}

// EncodeOpRepImport forwards to the package-level EncodeOpRepImport.
func (*Codec) EncodeOpRepImport(w io.Writer, dev domain.Device) error {
	return EncodeOpRepImport(w, dev)
}

// EncodeOpRepImportError forwards to the package-level
// EncodeOpRepImportError. Used by the exporter to reject an import
// request with an upstream-defined ST_* status code (no body).
func (*Codec) EncodeOpRepImportError(w io.Writer, status uint32) error {
	return EncodeOpRepImportError(w, status)
}

// DecodeOpRepImport calls the package-level decoder, logs any
// padded-string truncation signals surfaced in DecodeFlags, and
// returns the decoded device without the flags.
func (c *Codec) DecodeOpRepImport(r io.Reader) (domain.Device, error) {
	dev, flags, err := DecodeOpRepImport(r)
	if err != nil {
		return domain.Device{}, err
	}

	c.logDecodeFlags("OP_REP_IMPORT", flags)

	return dev, nil
}

// logDecodeFlags emits one slog.Warn record per advisory signal in
// flags. Keeping the logic in one place means future decode flags can
// extend DecodeFlags without touching every Codec method.
func (c *Codec) logDecodeFlags(op string, flags DecodeFlags) {
	if flags.TrailingBytes {
		c.log().Warn("trailing bytes after payload", slog.String("op", op))
	}

	for _, trunc := range flags.TruncatedPaddedStrings {
		c.log().Warn("padded string not NUL-terminated",
			slog.String("op", op),
			slog.String("field", trunc.Field),
			slog.Int64("device_index", int64(trunc.DeviceIndex)))
	}
}

// log returns the codec's logger, initialising it to a no-op if
// the Codec was zero-value constructed.
func (c *Codec) log() *slog.Logger {
	if c.logger == nil {
		return noopLogger()
	}

	return c.logger
}
