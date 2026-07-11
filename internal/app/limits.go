// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"golang.org/x/time/rate"
)

// Default resource limits per security-release-quality OpenSpec. Zero-valued option fields
// resolve to these values at NewExporter time so every Exporter has a
// predictable baseline policy even without explicit caps.
const (
	defaultMaxSessions        = 128
	defaultMaxSessionsPerPeer = 8
	defaultAcceptRateLimit    = 10.0
	defaultAcceptBurst        = 20
	defaultMaxHandshakeBytes  = 64 * 1024
	defaultHandshakeTimeout   = domain.DefaultExporterHandshakeTimeout
)

// exporterLimits bundles the resolved-from-defaults resource caps so
// the accept and handshake paths can read them without re-resolving
// zeros on every call. A negative value means "cap disabled".
type exporterLimits struct {
	maxSessions        int
	maxSessionsPerPeer int
	maxHandshakeBytes  int
	handshakeTimeout   time.Duration
}

// resolveExporterLimits fills in the security-release-quality OpenSpec defaults for zero-valued
// option fields. Negative values pass through unchanged so a caller
// can disable any individual cap explicitly.
func resolveExporterLimits(cfg *exporterConfig) exporterLimits {
	l := exporterLimits{
		maxSessions:        cfg.maxSessions,
		maxSessionsPerPeer: cfg.maxSessionsPerPeer,
		maxHandshakeBytes:  cfg.maxHandshakeBytes,
		handshakeTimeout:   cfg.handshakeTimeout,
	}

	if l.maxSessions == 0 {
		l.maxSessions = defaultMaxSessions
	}

	if l.maxSessionsPerPeer == 0 {
		l.maxSessionsPerPeer = defaultMaxSessionsPerPeer
	}

	if l.maxHandshakeBytes == 0 {
		l.maxHandshakeBytes = defaultMaxHandshakeBytes
	}

	if l.handshakeTimeout == 0 {
		l.handshakeTimeout = defaultHandshakeTimeout
	}

	return l
}

// acceptLimiter is the accept-rate token bucket used by the accept
// loop. A zero or negative rps disables the bucket entirely (every
// allow call succeeds immediately) so tests that do not exercise rate
// limiting can ignore the knob.
type acceptLimiter struct {
	bucket *rate.Limiter
}

// newAcceptLimiter returns an acceptLimiter configured for rps tokens
// per second with the given burst size. rps <= 0 returns a disabled
// limiter that always permits; burst <= 0 picks up the default so a
// caller who supplied only rps still gets a usable bucket. NaN /
// +Inf inputs are also treated as "disabled" — neither comparison
// rps <= 0 nor rate.NewLimiter handles NaN sensibly (NaN slips
// through every numeric guard and produces a limiter that permits
// every connection), and an Inf rate is functionally unlimited
// anyway.
func newAcceptLimiter(rps float64, burst int) acceptLimiter {
	if rps <= 0 || math.IsNaN(rps) || math.IsInf(rps, 0) {
		return acceptLimiter{}
	}

	if burst <= 0 {
		burst = defaultAcceptBurst
	}

	return acceptLimiter{bucket: rate.NewLimiter(rate.Limit(rps), burst)}
}

// allow returns true when a token was consumed successfully. A
// disabled limiter always returns true.
func (l acceptLimiter) allow() bool {
	if l.bucket == nil {
		return true
	}

	return l.bucket.Allow()
}

// resolveAcceptRate picks the effective tokens-per-second rate.
// Zero option value picks up the security-release-quality OpenSpec default; callers may pass a
// negative rps to disable rate limiting entirely.
func resolveAcceptRate(cfg *exporterConfig) float64 {
	if cfg.acceptRateLimit == 0 {
		return defaultAcceptRateLimit
	}

	return cfg.acceptRateLimit
}

// resolveAcceptBurst picks the effective token bucket burst. Zero
// option value picks up the security-release-quality OpenSpec default.
func resolveAcceptBurst(cfg *exporterConfig) int {
	if cfg.acceptBurst == 0 {
		return defaultAcceptBurst
	}

	return cfg.acceptBurst
}

// handshakeLimitReader wraps a reader and tracks the total bytes read.
// Reads past limit return ErrHandshakeTooLarge so the caller can log +
// close without silently truncating the payload. A limit <= 0 disables
// the check — reads pass through unchanged.
type handshakeLimitReader struct {
	r     io.Reader
	n     int
	limit int
}

// newHandshakeLimitReader constructs a handshakeLimitReader. limit <= 0
// is the "disabled" form: the returned reader is just a thin passthrough.
func newHandshakeLimitReader(r io.Reader, limit int) *handshakeLimitReader {
	return &handshakeLimitReader{r: r, limit: limit}
}

// Read implements io.Reader. On every call it checks whether the
// running total plus p's proposed size would breach the cap; if so, it
// returns the ErrHandshakeTooLarge sentinel. A normal io.EOF is
// preserved unwrapped so io.ReadFull and friends classify it
// correctly; other errors surface with a wrap.
func (h *handshakeLimitReader) Read(p []byte) (int, error) {
	if h.limit <= 0 {
		return h.readUnbounded(p)
	}

	remaining := h.limit - h.n
	if remaining <= 0 {
		return 0, ErrHandshakeTooLarge
	}

	want := min(len(p), remaining)

	n, err := h.r.Read(p[:want])

	h.n += n

	return n, classifyReadErr(err)
}

// readUnbounded handles the cap<=0 passthrough. Split out so Read's
// bounded branch does not mix two error-classification rules.
func (h *handshakeLimitReader) readUnbounded(p []byte) (int, error) {
	n, err := h.r.Read(p)

	return n, classifyReadErr(err)
}

// classifyReadErr keeps io.EOF unwrapped and wraps every other error
// (including io.ErrUnexpectedEOF and net errors) with a handshake-read
// context so the log site can identify the phase.
func classifyReadErr(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, io.EOF) {
		return io.EOF
	}

	return fmt.Errorf("handshake read: %w", err)
}
