// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

// TestWithExporterBuildInfoStampsMetricAtConstruction proves the
// build-info option wires through to SetBuildInfo at Exporter
// construction, landing a sample on the metrics bundle before any
// workload runs. Locks in the single-shot registration path so callers
// do not need a second MustNewMetrics call against the same registry.
func TestWithExporterBuildInfoStampsMetricAtConstruction(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := app.MustNewMetrics(reg)

	_ = newExporterForTest(t,
		app.WithExporterMetrics(metrics),
		app.WithExporterBuildInfo("v0.0.1", "deadbeef", "go1.26"),
	)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	var found bool

	for _, mf := range mfs {
		if mf.GetName() != "usbip_build_info" {
			continue
		}

		labels := map[string]string{}

		for _, lp := range mf.GetMetric()[0].GetLabel() {
			labels[lp.GetName()] = lp.GetValue()
		}

		require.Equal(t, "v0.0.1", labels["version"])
		require.Equal(t, "deadbeef", labels["commit"])
		require.Equal(t, "go1.26", labels["go_version"])

		found = true
	}

	require.True(t, found,
		"usbip_build_info must appear after NewExporter with WithExporterBuildInfo")
}

// TestImporterAttachIncrementsAttachCounter proves a successful Attach
// increments usbip_importer_attaches_total{outcome="ok"} and sets the
// ports_active gauge.
func TestImporterAttachIncrementsAttachCounter(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := app.MustNewMetrics(reg)

	conn := newFakeConn()

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
			return conn, nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}
	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return domain.PortID(7), nil
		},
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterMetrics(metrics),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.NoError(t, err)

	require.InDelta(t, 1.0, counterOutcomeValue(
		t, reg, "usbip_importer_attaches_total", string(app.AttachOutcomeOK),
	), 0.0001)

	require.InDelta(t, 1.0, gaugeOnlyValue(
		t, reg, "usbip_importer_ports_active",
	), 0.0001)
}

// TestImporterAttachDialFailureIncrementsOutcomeDial proves a dial
// failure routes through the `dial_failed` outcome label. The error
// path must still emit the attaches_total increment, otherwise the
// metric hides transport-level outages entirely.
func TestImporterAttachDialFailureIncrementsOutcomeDial(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := app.MustNewMetrics(reg)

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
			return nil, errBoom
		},
	}
	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterMetrics(metrics),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.Error(t, err)

	require.InDelta(t, 1.0, counterOutcomeValue(
		t, reg, "usbip_importer_attaches_total", string(app.AttachOutcomeDialFailed),
	), 0.0001)
}

// TestImporterAttachKernelErrorIncrementsKernelOutcome proves the
// AttachRemote failure path routes through `kernel_error`.
func TestImporterAttachKernelErrorIncrementsKernelOutcome(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := app.MustNewMetrics(reg)

	conn := newFakeConn()

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) { return conn, nil },
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}
	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return 0, errBoom
		},
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterMetrics(metrics),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.Error(t, err)

	require.InDelta(t, 1.0, counterOutcomeValue(
		t, reg, "usbip_importer_attaches_total", string(app.AttachOutcomeKernelError),
	), 0.0001)
}

// TestImporterDetachIncrementsDetachCounter proves a successful Detach
// increments usbip_importer_detaches_total{outcome="ok"} and decrements
// the ports_active gauge back to zero.
func TestImporterDetachIncrementsDetachCounter(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := app.MustNewMetrics(reg)

	conn := newFakeConn()

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) { return conn, nil },
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}
	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return domain.PortID(11), nil
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error { return nil },
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterMetrics(metrics),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.NoError(t, err)

	require.NoError(t, imp.Detach(context.Background(), port.ID))

	require.InDelta(t, 1.0, counterOutcomeValue(
		t, reg, "usbip_importer_detaches_total", string(app.DetachOutcomeOK),
	), 0.0001)

	require.InDelta(t, 0.0, gaugeOnlyValue(
		t, reg, "usbip_importer_ports_active",
	), 0.0001)
}

// TestExporterBindIncrementsOutcome proves Bind records
// usbip_exporter_bind_total{outcome="ok"} on success.
func TestExporterBindIncrementsOutcome(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := app.MustNewMetrics(reg)

	kernel := &ExporterKernelMock{
		BindFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterMetrics(metrics),
	)

	err := exp.Bind(context.Background(), domain.BusID("1-1"))
	require.NoError(t, err)

	require.InDelta(t, 1.0, counterOutcomeValue(
		t, reg, "usbip_exporter_bind_total", string(app.BindOutcomeOK),
	), 0.0001)
}

// TestExporterBindFailureIncrementsErrorOutcome proves a Bind failure
// routes through the `error` outcome rather than silently swallowing
// the event.
func TestExporterBindFailureIncrementsErrorOutcome(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := app.MustNewMetrics(reg)

	kernel := &ExporterKernelMock{
		BindFunc: func(_ context.Context, _ domain.BusID) error { return errBoom },
	}

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterMetrics(metrics),
	)

	err := exp.Bind(context.Background(), domain.BusID("1-1"))
	require.Error(t, err)

	require.InDelta(t, 1.0, counterOutcomeValue(
		t, reg, "usbip_exporter_bind_total", string(app.BindOutcomeError),
	), 0.0001)
}

// TestExporterUnbindIncrementsOutcome mirrors the Bind proof on the
// opposite transition.
func TestExporterUnbindIncrementsOutcome(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := app.MustNewMetrics(reg)

	kernel := &ExporterKernelMock{
		UnbindFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterMetrics(metrics),
	)

	err := exp.Unbind(context.Background(), domain.BusID("1-1"))
	require.NoError(t, err)

	require.InDelta(t, 1.0, counterOutcomeValue(
		t, reg, "usbip_exporter_unbind_total", string(app.UnbindOutcomeOK),
	), 0.0001)
}

// counterOutcomeValue is a localised version of counterLabelValue that
// takes a *prometheus.Registry + metric name + outcome label value,
// returning the observed counter value. Returns 0 on missing label
// match so the test can assert an uninitialised counter reads zero.
func counterOutcomeValue(
	t *testing.T, reg *prometheus.Registry, name, outcome string,
) float64 {
	t.Helper()

	fams, err := reg.Gather()
	require.NoError(t, err)

	for _, f := range fams {
		if f.GetName() != name {
			continue
		}

		for _, m := range f.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetValue() == outcome {
					return m.GetCounter().GetValue()
				}
			}
		}
	}

	return 0
}

// gaugeOnlyValue pulls a label-free gauge's value by name.
func gaugeOnlyValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()

	fams, err := reg.Gather()
	require.NoError(t, err)

	for _, f := range fams {
		if f.GetName() != name {
			continue
		}

		ms := f.GetMetric()
		if len(ms) == 0 {
			return 0
		}

		return ms[0].GetGauge().GetValue()
	}

	return 0
}

// Keep a reference to dto so the import survives even when parts of the
// file are refactored away; the tests above only use it transitively.
var _ = (*dto.MetricFamily)(nil)
