package app_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// importerTestEpoch is the epoch used to seed FakeClock across importer
// tests. A fixed epoch keeps test logs readable.
func importerTestEpoch() time.Time {
	return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
}

// newImporterForTest constructs an Importer with every required
// dependency stubbed so individual tests only wire the mocks they
// actually exercise.
func newImporterForTest(t *testing.T, opts ...app.ImporterOption) *app.Importer {
	t.Helper()

	const baseOptCount = 5

	base := make([]app.ImporterOption, 0, baseOptCount+len(opts))

	base = append(base,
		app.WithImporterKernel(&ImporterKernelMock{}),
		app.WithImporterEvents(&KernelEventsMock{
			SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
				ch := make(chan domain.Event)

				return ch, func() { close(ch) }, nil
			},
		}),
		app.WithImporterTransport(&TransportMock{}),
		app.WithImporterCodec(&ProtocolCodecMock{}),
		app.WithImporterClock(testutil.NewFakeClockAt(importerTestEpoch())),
	)

	return app.NewImporter(append(base, opts...)...)
}

// TestNewImporterReturnsNonNil asserts NewImporter succeeds with every
// required dependency wired in.
func TestNewImporterReturnsNonNil(t *testing.T) {
	t.Parallel()

	imp := newImporterForTest(t)
	require.NotNil(t, imp)
	require.NoError(t, imp.Close())
}

// TestNewImporterCloseIsIdempotent asserts Close returns nil on repeat
// invocations so `defer imp.Close()` is safe even after a prior
// explicit Close.
func TestNewImporterCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	imp := newImporterForTest(t)

	require.NoError(t, imp.Close())
	require.NoError(t, imp.Close())
	require.NoError(t, imp.Close())
}

// TestNewImporterPanicsOnMissingKernel proves the required-dependency
// guard in NewImporter.
func TestNewImporterPanicsOnMissingKernel(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"app.NewImporter: ImporterKernel is required (use WithImporterKernel)",
		func() { app.NewImporter() })
}

// TestNewImporterPanicsOnMissingEvents guards the second required
// dependency.
func TestNewImporterPanicsOnMissingEvents(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"app.NewImporter: KernelEvents is required (use WithImporterEvents)",
		func() {
			app.NewImporter(app.WithImporterKernel(&ImporterKernelMock{}))
		})
}

// TestNewImporterPanicsOnMissingTransport guards the third required
// dependency.
func TestNewImporterPanicsOnMissingTransport(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"app.NewImporter: Transport is required (use WithImporterTransport)",
		func() {
			app.NewImporter(
				app.WithImporterKernel(&ImporterKernelMock{}),
				app.WithImporterEvents(&KernelEventsMock{}),
			)
		})
}

// TestNewImporterPanicsOnMissingCodec guards the fourth required
// dependency.
func TestNewImporterPanicsOnMissingCodec(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"app.NewImporter: ProtocolCodec is required (use WithImporterCodec)",
		func() {
			app.NewImporter(
				app.WithImporterKernel(&ImporterKernelMock{}),
				app.WithImporterEvents(&KernelEventsMock{}),
				app.WithImporterTransport(&TransportMock{}),
			)
		})
}

// TestNewImporterAppliesLoggerAndClock covers the optional options so
// their setter paths are not dead code. The effect is observable only
// after a method that uses them is called; for the scaffolding test
// we just confirm construction succeeds.
func TestNewImporterAppliesLoggerAndClock(t *testing.T) {
	t.Parallel()

	clk := testutil.NewFakeClockAt(importerTestEpoch())

	imp := app.NewImporter(
		app.WithImporterKernel(&ImporterKernelMock{}),
		app.WithImporterEvents(&KernelEventsMock{}),
		app.WithImporterTransport(&TransportMock{}),
		app.WithImporterCodec(&ProtocolCodecMock{}),
		app.WithImporterClock(clk),
		app.WithImporterLogger(nil),
	)
	require.NotNil(t, imp)
	require.NoError(t, imp.Close())

	// Touch the fake clock so the import isn't unused — and so a
	// future regression that drops WithImporterClock surfaces as a
	// compile failure in this test.
	_ = clk.Now()

	// Silence unused-import for net in the scaffolding phase; Attach
	// tests in Task 5.5 will exercise the conn path directly.
	_ = net.Conn(nil)
}
