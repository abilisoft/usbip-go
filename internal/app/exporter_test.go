// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// exporterTestEpoch is the fixed epoch used to seed FakeClock across
// exporter tests so failure logs have a stable, readable time.
func exporterTestEpoch() time.Time {
	return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
}

// newExporterForTest constructs an Exporter with every required
// dependency stubbed. Individual tests override whichever dependency
// they exercise via additional options. Mirrors newImporterForTest.
func newExporterForTest(t *testing.T, opts ...app.ExporterOption) *app.Exporter {
	t.Helper()

	const baseOptCount = 5

	base := make([]app.ExporterOption, 0, baseOptCount+len(opts))

	base = append(base,
		app.WithExporterKernel(&ExporterKernelMock{}),
		app.WithExporterEvents(&KernelEventsMock{
			SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
				ch := make(chan domain.Event)

				return ch, func() { close(ch) }, nil
			},
		}),
		app.WithExporterTransport(&TransportMock{}),
		app.WithExporterCodec(&ProtocolCodecMock{}),
		app.WithExporterClock(testutil.NewFakeClockAt(exporterTestEpoch())),
	)

	return app.NewExporter(append(base, opts...)...)
}

// TestNewExporterReturnsNonNil asserts NewExporter succeeds with every
// required dependency wired in.
func TestNewExporterReturnsNonNil(t *testing.T) {
	t.Parallel()

	exp := newExporterForTest(t)
	require.NotNil(t, exp)
}

// TestNewExporterPanicsOnMissingKernel guards the required ExporterKernel.
func TestNewExporterPanicsOnMissingKernel(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"app.NewExporter: ExporterKernel is required (use WithExporterKernel)",
		func() { app.NewExporter() })
}

// TestNewExporterPanicsOnMissingEvents guards the required KernelEvents.
func TestNewExporterPanicsOnMissingEvents(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"app.NewExporter: KernelEvents is required (use WithExporterEvents)",
		func() {
			app.NewExporter(app.WithExporterKernel(&ExporterKernelMock{}))
		})
}

// TestNewExporterPanicsOnMissingTransport guards the required Transport.
func TestNewExporterPanicsOnMissingTransport(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"app.NewExporter: Transport is required (use WithExporterTransport)",
		func() {
			app.NewExporter(
				app.WithExporterKernel(&ExporterKernelMock{}),
				app.WithExporterEvents(&KernelEventsMock{}),
			)
		})
}

// TestNewExporterPanicsOnMissingCodec guards the required ProtocolCodec.
func TestNewExporterPanicsOnMissingCodec(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"app.NewExporter: ProtocolCodec is required (use WithExporterCodec)",
		func() {
			app.NewExporter(
				app.WithExporterKernel(&ExporterKernelMock{}),
				app.WithExporterEvents(&KernelEventsMock{}),
				app.WithExporterTransport(&TransportMock{}),
			)
		})
}

// TestNewExporterAppliesOptionalOptions exercises the optional option
// setters so they are not dead code.
func TestNewExporterAppliesOptionalOptions(t *testing.T) {
	t.Parallel()

	clk := testutil.NewFakeClockAt(exporterTestEpoch())

	exp := app.NewExporter(
		app.WithExporterKernel(&ExporterKernelMock{}),
		app.WithExporterEvents(&KernelEventsMock{}),
		app.WithExporterTransport(&TransportMock{}),
		app.WithExporterCodec(&ProtocolCodecMock{}),
		app.WithExporterClock(clk),
		app.WithExporterLogger(nil),
	)
	require.NotNil(t, exp)

	_ = clk.Now()
}

// testBusID returns the canonical busID used by exporter Bind/Unbind
// tests so failures reference a single value.
func testBusID() domain.BusID { return domain.BusID("1-2") }

// TestExporterBindHappyPath asserts Bind delegates to kernel.Bind with
// the given busID and returns nil on success.
func TestExporterBindHappyPath(t *testing.T) {
	t.Parallel()

	kernel := &ExporterKernelMock{
		BindFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	exp := newExporterForTest(t, app.WithExporterKernel(kernel))

	require.NoError(t, exp.Bind(context.Background(), testBusID()))
	require.Len(t, kernel.BindCalls(), 1)
	require.Equal(t, testBusID(), kernel.BindCalls()[0].BusID)
}

// TestExporterBindKernelFailure asserts kernel errors surface wrapped
// with the busid context.
func TestExporterBindKernelFailure(t *testing.T) {
	t.Parallel()

	kernel := &ExporterKernelMock{
		BindFunc: func(_ context.Context, _ domain.BusID) error { return domain.ErrDeviceAlreadyBound },
	}

	exp := newExporterForTest(t, app.WithExporterKernel(kernel))

	err := exp.Bind(context.Background(), testBusID())
	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound)
	require.Contains(t, err.Error(), string(testBusID()))
}

// TestExporterUnbindHappyPath asserts Unbind delegates to kernel.Unbind.
func TestExporterUnbindHappyPath(t *testing.T) {
	t.Parallel()

	kernel := &ExporterKernelMock{
		UnbindFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	exp := newExporterForTest(t, app.WithExporterKernel(kernel))

	require.NoError(t, exp.Unbind(context.Background(), testBusID()))
	require.Len(t, kernel.UnbindCalls(), 1)
	require.Equal(t, testBusID(), kernel.UnbindCalls()[0].BusID)
}

// TestExporterUnbindKernelFailure asserts kernel unbind errors surface
// wrapped with the busid context.
func TestExporterUnbindKernelFailure(t *testing.T) {
	t.Parallel()

	kernel := &ExporterKernelMock{
		UnbindFunc: func(_ context.Context, _ domain.BusID) error { return domain.ErrDeviceNotBound },
	}

	exp := newExporterForTest(t, app.WithExporterKernel(kernel))

	err := exp.Unbind(context.Background(), testBusID())
	require.ErrorIs(t, err, domain.ErrDeviceNotBound)
	require.Contains(t, err.Error(), string(testBusID()))
}

// TestExporterListAvailableHappyPath asserts ListAvailable forwards to
// kernel.ListLocalDevices and returns the slice verbatim.
func TestExporterListAvailableHappyPath(t *testing.T) {
	t.Parallel()

	want := []domain.Device{
		{BusID: domain.BusID("1-1"), Path: "/sys/devices/pci/usb1/1-1"},
		{BusID: domain.BusID("2-1"), Path: "/sys/devices/pci/usb2/2-1"},
	}

	kernel := &ExporterKernelMock{
		ListLocalDevicesFunc: func(_ context.Context) ([]domain.Device, error) { return want, nil },
	}

	exp := newExporterForTest(t, app.WithExporterKernel(kernel))

	got, err := exp.ListAvailable(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Len(t, kernel.ListLocalDevicesCalls(), 1)
}

// TestExporterListAvailableKernelFailure asserts kernel errors surface
// with a descriptive wrap.
func TestExporterListAvailableKernelFailure(t *testing.T) {
	t.Parallel()

	kernel := &ExporterKernelMock{
		ListLocalDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return nil, errBoom
		},
	}

	exp := newExporterForTest(t, app.WithExporterKernel(kernel))

	devs, err := exp.ListAvailable(context.Background())
	require.Nil(t, devs)
	require.ErrorIs(t, err, errBoom)
}
