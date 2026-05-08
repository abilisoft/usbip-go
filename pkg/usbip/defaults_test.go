package usbip_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestNewImporterDefaultsSucceeds proves the zero-option path: no
// adapters touched (kernel adapter construction does not open a real
// netlink socket; that happens lazily at Subscribe time), options
// default-apply, and a non-nil *Importer is returned.
func TestNewImporterDefaultsSucceeds(t *testing.T) {
	t.Parallel()

	imp, err := usbip.NewImporter()
	require.NoError(t, err)
	require.NotNil(t, imp)

	// Close is idempotent even on a default Importer that never
	// attached a port.
	require.NoError(t, imp.Close())
}

// TestNewExporterDefaultsSucceeds mirrors the importer variant.
func TestNewExporterDefaultsSucceeds(t *testing.T) {
	t.Parallel()

	exp, err := usbip.NewExporter()
	require.NoError(t, err)
	require.NotNil(t, exp)

	require.NoError(t, exp.Shutdown(t.Context()))
}

// TestNewExporterAllowCIDRInvalidReturnsError proves bad CIDR strings
// surface at construction time — not at Serve time, per spec §11.5.2.
func TestNewExporterAllowCIDRInvalidReturnsError(t *testing.T) {
	t.Parallel()

	_, err := usbip.NewExporter(usbip.WithExporterAllowCIDR("not-a-cidr"))
	require.Error(t, err)
}

// TestNewImporterAppliesOptions drives every NewImporter option so the
// config→internal translation branches are exercised end-to-end.
func TestNewImporterAppliesOptions(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	backoff := usbip.FixedBackoff{Delay: time.Millisecond}

	imp, err := usbip.NewImporter(
		usbip.WithImporterLogger(logger),
		usbip.WithImporterBackoff(backoff),
		usbip.WithImporterStatusPollInterval(200*time.Millisecond),
	)
	require.NoError(t, err)
	require.NotNil(t, imp)

	require.NoError(t, imp.Close())
}

// TestNewImporterNilOptionIsSkipped proves a nil ImporterOption in the
// variadic argument list does not crash NewImporter. Go convention
// tolerates nil options — see http.Handler composition — so consumers
// composing With* helpers conditionally can pass `nil` without a
// runtime panic.
func TestNewImporterNilOptionIsSkipped(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() {
		imp, err := usbip.NewImporter(nil)
		require.NoError(t, err)
		require.NotNil(t, imp)

		require.NoError(t, imp.Close())
	})
}

// TestNewExporterNilOptionIsSkipped mirrors TestNewImporterNilOptionIsSkipped
// for the exporter role.
func TestNewExporterNilOptionIsSkipped(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() {
		exp, err := usbip.NewExporter(nil)
		require.NoError(t, err)
		require.NotNil(t, exp)

		require.NoError(t, exp.Shutdown(t.Context()))
	})
}

// TestNewExporterAppliesOptions mirrors TestNewImporterAppliesOptions
// for the exporter role: every ExporterOption is applied so the
// translation helper covers every branch.
func TestNewExporterAppliesOptions(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	exp, err := usbip.NewExporter(
		usbip.WithExporterLogger(logger),
		usbip.WithExporterMaxSessions(10),
		usbip.WithExporterMaxSessionsPerPeer(2),
		usbip.WithExporterAcceptRateLimit(5.0),
		usbip.WithExporterAllowCIDR("127.0.0.0/8"),
		usbip.WithExporterMaxHandshakeBytes(4096),
		usbip.WithExporterHandshakeTimeout(time.Second),
		usbip.WithExporterShutdownTimeout(2*time.Second),
	)
	require.NoError(t, err)
	require.NotNil(t, exp)

	require.NoError(t, exp.Shutdown(t.Context()))
}
