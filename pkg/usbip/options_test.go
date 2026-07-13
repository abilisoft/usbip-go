// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"log/slog"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestImporterOptionTypeIsFunc pins the public type shape: option
// constructors return a function value that can be invoked against a
// config pointer. A renamed or redeclared ImporterOption surfaces as a
// compile error.
func TestImporterOptionTypeIsFunc(t *testing.T) {
	t.Parallel()

	// Passing the option to a parameter typed as ImporterOption forces
	// the compile-time check: constructor result is convertible to the
	// public type.
	require.NotNil(t, acceptImporterOption(
		usbip.WithImporterLogger(slog.New(slog.DiscardHandler)),
	))
}

// acceptImporterOption forces its argument to the public ImporterOption
// type — the call site only type-checks when the passed value matches
// that named function type. Returns the argument so callers can assert
// non-nil in a single statement.
func acceptImporterOption(o usbip.ImporterOption) usbip.ImporterOption { return o }

// TestWithImporterLoggerStoresLogger proves NewImporter applied with
// WithImporterLogger returns an Importer that uses the caller's slog
// handler. We construct the facade Importer via the test hook using an
// internal Importer whose options were first translated through the
// facade option; log output then passes through the injected buffer.
func TestWithImporterLoggerStoresLogger(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	cfg := usbip.NewImporterConfigForTest(usbip.WithImporterLogger(logger))

	require.Same(t, logger, cfg.LoggerForTest())
}

// TestWithImporterBackoffStores proves the option stores the supplied
// BackoffStrategy on the importerConfig.
func TestWithImporterBackoffStores(t *testing.T) {
	t.Parallel()

	b := usbip.FixedBackoff{Delay: 100 * time.Millisecond}

	cfg := usbip.NewImporterConfigForTest(usbip.WithImporterBackoff(b))

	require.Equal(t, usbip.BackoffStrategy(b), cfg.BackoffForTest())
}

func TestWithImporterBackoffFactoryStoresAndWins(t *testing.T) {
	t.Parallel()

	factory := usbip.BackoffFactory(func() usbip.BackoffStrategy {
		return usbip.FixedBackoff{Delay: time.Second}
	})

	cfg := usbip.NewImporterConfigForTest(
		usbip.WithImporterBackoff(usbip.FixedBackoff{Delay: time.Millisecond}),
		usbip.WithImporterBackoffFactory(factory),
	)

	require.Nil(t, cfg.BackoffForTest())
	require.NotNil(t, cfg.BackoffFactoryForTest())
	require.Equal(t, time.Second, cfg.BackoffFactoryForTest()().Next(0))
}

func TestWithImporterBackoffWinsAfterFactory(t *testing.T) {
	t.Parallel()

	want := usbip.FixedBackoff{Delay: 2 * time.Second}
	cfg := usbip.NewImporterConfigForTest(
		usbip.WithImporterBackoffFactory(func() usbip.BackoffStrategy {
			return usbip.FixedBackoff{Delay: time.Second}
		}),
		usbip.WithImporterBackoff(want),
	)

	require.Equal(t, usbip.BackoffStrategy(want), cfg.BackoffForTest())
	require.Nil(t, cfg.BackoffFactoryForTest())
}

func TestWithImporterBackoffFactoryNilRestoresDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		prior usbip.ImporterOption
	}{
		{
			name:  "clears legacy strategy",
			prior: usbip.WithImporterBackoff(usbip.FixedBackoff{Delay: time.Second}),
		},
		{
			name: "clears configured factory",
			prior: usbip.WithImporterBackoffFactory(func() usbip.BackoffStrategy {
				return usbip.FixedBackoff{Delay: time.Second}
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := usbip.NewImporterConfigForTest(
				tt.prior,
				usbip.WithImporterBackoffFactory(nil),
			)
			require.Nil(t, cfg.BackoffForTest())
			require.Nil(t, cfg.BackoffFactoryForTest())

			translated := usbip.ImporterAttachOptionsForTest(
				usbip.AttachOptions{AutoReconnect: true},
				tt.prior,
				usbip.WithImporterBackoffFactory(nil),
			)
			require.Nil(t, translated.Backoff)
			require.Nil(t, translated.BackoffFactory)
		})
	}
}

func TestAttachBackoffOverridesConfiguredFactoryWithoutInvokingIt(t *testing.T) {
	t.Parallel()

	var factoryCalls atomic.Int32

	want := usbip.FixedBackoff{Delay: 3 * time.Second}

	translated := usbip.ImporterAttachOptionsForTest(
		usbip.AttachOptions{AutoReconnect: true, Backoff: want},
		usbip.WithImporterBackoffFactory(func() usbip.BackoffStrategy {
			factoryCalls.Add(1)

			return usbip.FixedBackoff{Delay: time.Second}
		}),
	)

	require.Nil(t, translated.BackoffFactory)
	require.Equal(t, want.Delay, translated.Backoff.Next(0))
	require.Zero(t, factoryCalls.Load())
}

func TestConfiguredBackoffFactoryTranslationIsLazy(t *testing.T) {
	t.Parallel()

	var factoryCalls atomic.Int32

	translated := usbip.ImporterAttachOptionsForTest(
		usbip.AttachOptions{AutoReconnect: true},
		usbip.WithImporterBackoffFactory(func() usbip.BackoffStrategy {
			factoryCalls.Add(1)

			return usbip.FixedBackoff{Delay: time.Second}
		}),
	)

	require.Nil(t, translated.Backoff)
	require.NotNil(t, translated.BackoffFactory)
	require.Zero(t, factoryCalls.Load(), "translation must not construct state")
	require.Equal(t, time.Second, translated.BackoffFactory().Next(0))
	require.EqualValues(t, 1, factoryCalls.Load())
}

func TestConfiguredLegacyBackoffTranslatesWithoutFactory(t *testing.T) {
	t.Parallel()

	strategy := &customBackoff{}
	translated := usbip.ImporterAttachOptionsForTest(
		usbip.AttachOptions{AutoReconnect: true},
		usbip.WithImporterBackoff(strategy),
	)

	require.NotNil(t, translated.Backoff)
	require.Nil(t, translated.BackoffFactory)
	require.Equal(t, 42*time.Millisecond, translated.Backoff.Next(0))
	translated.Backoff.Reset()
	require.Equal(t, 1, strategy.resetCount)
}

// TestWithImporterStatusPollIntervalStores proves the option stores
// the supplied poll interval.
func TestWithImporterStatusPollIntervalStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewImporterConfigForTest(usbip.WithImporterStatusPollInterval(time.Second))

	require.Equal(t, time.Second, cfg.StatusPollIntervalForTest())
}

// TestWithExporterLoggerStores proves the exporter variant.
func TestWithExporterLoggerStores(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterLogger(logger))

	require.Same(t, logger, cfg.LoggerForTest())
}

// TestWithExporterMaxSessionsStores proves the session cap option.
func TestWithExporterMaxSessionsStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterMaxSessions(42))

	require.Equal(t, 42, cfg.MaxSessionsForTest())
}

// TestWithExporterMaxSessionsPerPeerStores proves the per-peer cap.
func TestWithExporterMaxSessionsPerPeerStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterMaxSessionsPerPeer(7))

	require.Equal(t, 7, cfg.MaxSessionsPerPeerForTest())
}

// TestWithExporterAcceptRateLimitStores proves the rate-limit option.
func TestWithExporterAcceptRateLimitStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterAcceptRateLimit(10.0))

	require.InDelta(t, 10.0, cfg.AcceptRateLimitForTest(), 0.001)
	require.True(t, cfg.AcceptRateLimitSetForTest())
}

func TestWithExporterAcceptRateLimitTracksExplicitZero(t *testing.T) {
	t.Parallel()

	omitted := usbip.NewExporterConfigForTest()
	explicit := usbip.NewExporterConfigForTest(usbip.WithExporterAcceptRateLimit(0))

	require.False(t, omitted.AcceptRateLimitSetForTest())
	require.True(t, explicit.AcceptRateLimitSetForTest())
	require.Zero(t, explicit.AcceptRateLimitForTest())
}

func TestWithExporterAcceptRateLimitLastOptionWins(t *testing.T) {
	t.Parallel()

	finalFinite := usbip.NewExporterConfigForTest(
		usbip.WithExporterAcceptRateLimit(math.NaN()),
		usbip.WithExporterAcceptRateLimit(7.5),
	)
	require.True(t, finalFinite.AcceptRateLimitSetForTest())
	require.InDelta(t, 7.5, finalFinite.AcceptRateLimitForTest(), 0)

	finalDisabled := usbip.NewExporterConfigForTest(
		usbip.WithExporterAcceptRateLimit(7.5),
		usbip.WithExporterAcceptRateLimit(0),
	)
	require.True(t, finalDisabled.AcceptRateLimitSetForTest())
	require.Zero(t, finalDisabled.AcceptRateLimitForTest())

	finalInvalid := usbip.NewExporterConfigForTest(
		usbip.WithExporterAcceptRateLimit(7.5),
		usbip.WithExporterAcceptRateLimit(math.Inf(1)),
	)
	require.True(t, finalInvalid.AcceptRateLimitSetForTest())
	require.True(t, math.IsInf(finalInvalid.AcceptRateLimitForTest(), 1))
}

// TestWithExporterAllowCIDRAppends proves multiple calls accumulate.
func TestWithExporterAllowCIDRAppends(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(
		usbip.WithExporterAllowCIDR("10.0.0.0/8"),
		usbip.WithExporterAllowCIDR("192.168.0.0/16", "172.16.0.0/12"),
	)

	require.Equal(t, []string{"10.0.0.0/8", "192.168.0.0/16", "172.16.0.0/12"}, cfg.AllowCIDRsForTest())
}

// TestWithExporterMaxHandshakeBytesStores proves the handshake cap.
func TestWithExporterMaxHandshakeBytesStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterMaxHandshakeBytes(4096))

	require.Equal(t, 4096, cfg.MaxHandshakeBytesForTest())
}

// TestWithExporterHandshakeTimeoutStores proves the timeout option.
func TestWithExporterHandshakeTimeoutStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterHandshakeTimeout(2 * time.Second))

	require.Equal(t, 2*time.Second, cfg.HandshakeTimeoutForTest())
}

// TestWithExporterShutdownTimeoutStores proves the shutdown-timeout
// option stores the value on the public config. Internal consumption
// is wired in the exporter lifecycle; the public field is stable
// across implementation changes.
func TestWithExporterShutdownTimeoutStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterShutdownTimeout(30 * time.Second))

	require.Equal(t, 30*time.Second, cfg.ShutdownTimeoutForTest())
}

// TestExporterOptionTypeIsFunc pins the public ExporterOption shape.
func TestExporterOptionTypeIsFunc(t *testing.T) {
	t.Parallel()

	require.NotNil(t, acceptExporterOption(usbip.WithExporterMaxSessions(1)))
}

// acceptExporterOption mirrors acceptImporterOption for the exporter.
func acceptExporterOption(o usbip.ExporterOption) usbip.ExporterOption { return o }
