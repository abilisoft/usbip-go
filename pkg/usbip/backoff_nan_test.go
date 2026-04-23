package usbip_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// TestExponentialBackoffConfigValidateRejectsNaN pins the invariant
// that NaN Jitter is an error at construction. Every comparison
// against NaN is false, so a raw `Jitter < 0 || Jitter >= 1` test
// would silently accept math.NaN() and the bad value would land on
// the reconnect loop where Next can then panic inside the jitter
// multiplier.
func TestExponentialBackoffConfigValidateRejectsNaN(t *testing.T) {
	t.Parallel()

	cfg := usbip.ExponentialBackoffConfig{
		Min:    time.Second,
		Max:    time.Minute,
		Jitter: math.NaN(),
	}

	require.Error(t, cfg.Validate(),
		"NaN Jitter must be rejected by Validate")
}

// TestNewExponentialBackoffPanicsOnNaN ensures the constructor path
// also refuses NaN, mirroring the out-of-range rejection behaviour.
// The panic is intentional: a NaN Jitter is a programmer error that
// would otherwise surface as a silent corruption of every reconnect
// schedule built from the same config.
func TestNewExponentialBackoffPanicsOnNaN(t *testing.T) {
	t.Parallel()

	cfg := usbip.ExponentialBackoffConfig{
		Min:    time.Second,
		Max:    time.Minute,
		Jitter: math.NaN(),
	}

	require.Panics(t, func() {
		_ = usbip.NewExponentialBackoff(cfg)
	})
}
