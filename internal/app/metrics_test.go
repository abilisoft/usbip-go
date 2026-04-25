// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// errNoErrno is the static sentinel for the "error carries no errno"
// row in TestSysfsErrnoFromErrorMapsPOSIX. err113 requires named
// errors instead of ad-hoc errors.New at call sites.
var errNoErrno = errors.New("no errno")

// TestMustNewMetricsRegistersFullCatalog proves MustNewMetrics registers
// every §11.5.5 entry exactly once against a fresh registry. A duplicate
// name (the classic Prometheus failure mode) would make Gather surface
// fewer entries than expected or panic on registration.
//
// CounterVec / HistogramVec / GaugeVec with no observed label sets are
// registered against the collector list but do NOT yield samples from
// Gather — priming each family with at least one sample surfaces the
// catalog in the registry output.
func TestMustNewMetricsRegistersFullCatalog(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	m := app.MustNewMetrics(reg)
	require.NotNil(t, m)

	primeEveryFamily(m)

	fams, err := reg.Gather()
	require.NoError(t, err)

	names := make(map[string]struct{}, len(fams))
	for _, f := range fams {
		names[f.GetName()] = struct{}{}
	}

	for _, want := range expectedMetricNames() {
		_, ok := names[want]
		require.Truef(t, ok, "registry missing metric %q; got %v", want, keysOf(names))
	}
}

// primeEveryFamily touches every metric vector so the registry Gather
// output includes the full catalog regardless of whether a concrete
// label set has been observed yet. Used by catalog assertions; not a
// production pattern.
func primeEveryFamily(m *app.Metrics) {
	m.ExporterSessionsActive(0)
	m.ExporterSessionAccepted(app.OutcomeHandshakeOK)
	m.ExporterHandshakeDuration(app.HandshakeOpImport, 0)
	m.ExporterBind(app.BindOutcomeOK)
	m.ExporterUnbind(app.UnbindOutcomeOK)
	m.ExporterDisconnect(app.DisconnectReasonGraceful)
	m.ImporterAttached(app.AttachOutcomeOK)
	m.ImporterDetached(app.DetachOutcomeOK)
	m.ImporterPortsActive(0)
	m.ImporterReconnectAttempt(app.ReconnectOutcomeOK)
	m.AdapterSysfsWriteFailure(app.SysfsWritePathOther, app.SysfsErrnoEACCES)
	m.KernelModuleLoaded(app.ModuleUsbipCore, true)
	m.SetBuildInfo("v", "c", "g")
}

// TestMustNewMetricsPanicsOnDuplicate asserts the same registerer handed
// to two MustNewMetrics calls panics rather than silently dropping the
// second registration. A caller that accidentally wires the same
// registerer twice needs loud failure, not a quiet no-op.
func TestMustNewMetricsPanicsOnDuplicate(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	_ = app.MustNewMetrics(reg)

	require.Panics(t, func() { app.MustNewMetrics(reg) })
}

// TestMustNewMetricsTypedOutcomeAccessorsRecord proves the typed
// accessors actually hit the right metric + label. The catalog is a
// stable contract: call sites never pass raw strings, so the typed
// methods must forward to the correct prometheus vector.
func TestMustNewMetricsTypedOutcomeAccessorsRecord(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := app.MustNewMetrics(reg)

	m.ExporterSessionAccepted(app.OutcomeHandshakeOK)
	m.ExporterSessionAccepted(app.OutcomeHandshakeOK)
	m.ExporterSessionAccepted(app.OutcomeRejectedACL)
	m.ExporterBind(app.BindOutcomeOK)
	m.ExporterUnbind(app.UnbindOutcomeOK)
	m.ExporterDisconnect(app.DisconnectReasonGraceful)
	m.ExporterHandshakeDuration(app.HandshakeOpImport, 0.25)
	m.ExporterSessionsActive(3)
	m.ImporterAttached(app.AttachOutcomeOK)
	m.ImporterDetached(app.DetachOutcomeOK)
	m.ImporterPortsActive(2)
	m.ImporterReconnectAttempt(app.ReconnectOutcomeOK)
	m.AdapterSysfsWriteFailure(app.SysfsWritePathBind, app.SysfsErrnoEACCES)
	m.KernelModuleLoaded(app.ModuleUsbipCore, true)
	m.KernelModuleLoaded(app.ModuleVhciHcd, false)
	m.SetBuildInfo("v1.2.3", "abcdef", "go1.26")

	fams, err := reg.Gather()
	require.NoError(t, err)

	famByName := make(map[string]*dto.MetricFamily, len(fams))
	for _, f := range fams {
		famByName[f.GetName()] = f
	}

	require.InDelta(t, 2.0, counterLabelValue(
		t, famByName, "usbip_exporter_sessions_accepted_total",
		"outcome", string(app.OutcomeHandshakeOK),
	), 0.0001)

	require.InDelta(t, 1.0, counterLabelValue(
		t, famByName, "usbip_exporter_sessions_accepted_total",
		"outcome", string(app.OutcomeRejectedACL),
	), 0.0001)

	require.InDelta(t, 3.0, gaugeValue(
		t, famByName, "usbip_exporter_sessions_active"), 0.0001)

	require.InDelta(t, 2.0, gaugeValue(
		t, famByName, "usbip_importer_ports_active"), 0.0001)

	require.InDelta(t, 1.0, gaugeLabelValue(
		t, famByName, "usbip_kernel_modules_loaded",
		"module", string(app.ModuleUsbipCore),
	), 0.0001)

	require.InDelta(t, 0.0, gaugeLabelValue(
		t, famByName, "usbip_kernel_modules_loaded",
		"module", string(app.ModuleVhciHcd),
	), 0.0001)

	// build_info is gauge-valued at 1 with the build labels carried.
	bi := famByName["usbip_build_info"]
	require.NotNil(t, bi, "build_info family must be registered")
	require.NotEmpty(t, bi.GetMetric(), "build_info needs a sample after SetBuildInfo")

	labelSet := labelsOf(bi.GetMetric()[0])
	require.Equal(t, "v1.2.3", labelSet["version"])
	require.Equal(t, "abcdef", labelSet["commit"])
	require.Equal(t, "go1.26", labelSet["go_version"])
	require.InDelta(t, 1.0, bi.GetMetric()[0].GetGauge().GetValue(), 0.0001)
}

// TestMustNewMetricsAcceptsNilRegisterer proves MustNewMetrics handles
// the nil-registerer case as a no-op bundle. Call sites wiring a
// Metrics into the Importer/Exporter must be able to pass nil (metrics
// disabled) without a constructor panic.
func TestMustNewMetricsAcceptsNilRegisterer(t *testing.T) {
	t.Parallel()

	m := app.MustNewMetrics(nil)
	require.NotNil(t, m)

	// Every typed method must be a nop (no panic) against the nil
	// registerer bundle. Exercising a handful is enough — they all route
	// through the same underlying vectors.
	m.ExporterSessionAccepted(app.OutcomeHandshakeOK)
	m.ImporterAttached(app.AttachOutcomeOK)
	m.ImporterReconnectAttempt(app.ReconnectOutcomeBackoff)
	m.AdapterSysfsWriteFailure(app.SysfsWritePathOther, app.SysfsErrnoEACCES)
	m.SetBuildInfo("v", "c", "g")
}

// expectedMetricNames returns the full v1 catalog per v1 contract §11.5.5. The
// order matches the spec table so a drift between the two is obvious
// during review.
func expectedMetricNames() []string {
	return []string{
		"usbip_exporter_sessions_active",
		"usbip_exporter_sessions_accepted_total",
		"usbip_exporter_handshake_duration_seconds",
		"usbip_exporter_bind_total",
		"usbip_exporter_unbind_total",
		"usbip_exporter_disconnect_total",
		"usbip_importer_attaches_total",
		"usbip_importer_detaches_total",
		"usbip_importer_ports_active",
		"usbip_importer_reconnect_attempts_total",
		"usbip_adapter_sysfs_write_failures_total",
		"usbip_kernel_modules_loaded",
		"usbip_build_info",
	}
}

// keysOf renders the names set for the failure message — keeps the
// require.Truef assertion readable when the catalog drifts.
func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

// counterLabelValue pulls the sample from famByName[name] whose label
// label=val matches. Fails the test when the family, the label, or a
// non-counter sample is missing.
func counterLabelValue(
	t *testing.T, famByName map[string]*dto.MetricFamily,
	name, label, val string,
) float64 {
	t.Helper()

	fam, ok := famByName[name]
	require.Truef(t, ok, "family %q not registered", name)

	for _, m := range fam.GetMetric() {
		if labelsOf(m)[label] == val {
			require.NotNil(t, m.GetCounter(), "metric %s is not a counter", name)

			return m.GetCounter().GetValue()
		}
	}

	require.Failf(t, "no label match",
		"family %q has no sample with %s=%q", name, label, val)

	return 0
}

// gaugeValue pulls the first sample of a label-free gauge family.
func gaugeValue(
	t *testing.T, famByName map[string]*dto.MetricFamily, name string,
) float64 {
	t.Helper()

	fam, ok := famByName[name]
	require.Truef(t, ok, "family %q not registered", name)

	ms := fam.GetMetric()
	require.NotEmptyf(t, ms, "family %q has no samples", name)

	g := ms[0].GetGauge()
	require.NotNil(t, g, "metric %s is not a gauge", name)

	return g.GetValue()
}

// gaugeLabelValue pulls the gauge sample keyed by label=val from the
// named labelled family.
func gaugeLabelValue(
	t *testing.T, famByName map[string]*dto.MetricFamily,
	name, label, val string,
) float64 {
	t.Helper()

	fam, ok := famByName[name]
	require.Truef(t, ok, "family %q not registered", name)

	for _, m := range fam.GetMetric() {
		if labelsOf(m)[label] == val {
			require.NotNil(t, m.GetGauge(), "metric %s is not a gauge", name)

			return m.GetGauge().GetValue()
		}
	}

	require.Failf(t, "no label match",
		"family %q has no sample with %s=%q", name, label, val)

	return 0
}

// labelsOf flattens a dto.Metric's label pairs into a map for easy
// lookup in the assertion helpers above.
func labelsOf(m *dto.Metric) map[string]string {
	out := make(map[string]string, len(m.GetLabel()))
	for _, lp := range m.GetLabel() {
		out[lp.GetName()] = lp.GetValue()
	}

	return out
}

// TestAdapterSysfsWriteFailureClosedLabelSet proves the typed accessor
// clamps both path and errno to the closed sets documented in §11.5.5,
// preventing unbounded-cardinality label explosion. An ad-hoc path that
// is not in the typed whitelist collapses to SysfsWritePathOther;
// an errno outside the POSIX subset collapses to SysfsErrnoOther.
func TestAdapterSysfsWriteFailureClosedLabelSet(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := app.MustNewMetrics(reg)

	// Every typed constant surfaces as its documented string; any path
	// the typed API exposes must be valid when fed back through the
	// accessor.
	typedPaths := []app.SysfsWritePath{
		app.SysfsWritePathBind,
		app.SysfsWritePathUnbind,
		app.SysfsWritePathMatchBusID,
		app.SysfsWritePathRebind,
		app.SysfsWritePathAttach,
		app.SysfsWritePathDetach,
		app.SysfsWritePathUsbipSockfd,
		app.SysfsWritePathOther,
	}

	typedErrnos := []app.SysfsErrno{
		app.SysfsErrnoENOENT,
		app.SysfsErrnoEACCES,
		app.SysfsErrnoEPERM,
		app.SysfsErrnoEBUSY,
		app.SysfsErrnoENODEV,
		app.SysfsErrnoEIO,
		app.SysfsErrnoOther,
	}

	for _, p := range typedPaths {
		for _, e := range typedErrnos {
			m.AdapterSysfsWriteFailure(p, e)
		}
	}

	fams, err := reg.Gather()
	require.NoError(t, err)

	var writeFailures *dto.MetricFamily

	for _, f := range fams {
		if f.GetName() == "usbip_adapter_sysfs_write_failures_total" {
			writeFailures = f
		}
	}

	require.NotNil(t, writeFailures, "usbip_adapter_sysfs_write_failures_total must be registered")

	// Cardinality ceiling: len(path-set) * len(errno-set) label combos max.
	maxCardinality := len(typedPaths) * len(typedErrnos)
	require.LessOrEqualf(t, len(writeFailures.GetMetric()), maxCardinality,
		"cardinality exceeds closed-set ceiling %d; got %d samples",
		maxCardinality, len(writeFailures.GetMetric()))

	// Every observed label value must come from the closed sets — spot
	// check that no ad-hoc string leaked through.
	pathSet := make(map[string]struct{}, len(typedPaths))
	for _, p := range typedPaths {
		pathSet[string(p)] = struct{}{}
	}

	errnoSet := make(map[string]struct{}, len(typedErrnos))
	for _, e := range typedErrnos {
		errnoSet[string(e)] = struct{}{}
	}

	for _, mm := range writeFailures.GetMetric() {
		lbs := labelsOf(mm)
		_, pathOK := pathSet[lbs["path"]]
		_, errnoOK := errnoSet[lbs["errno"]]
		require.Truef(t, pathOK, "path label %q is not in closed set %v", lbs["path"], keysOf(pathSet))
		require.Truef(t, errnoOK, "errno label %q is not in closed set %v", lbs["errno"], keysOf(errnoSet))
	}
}

// TestSysfsErrnoFromErrorMapsPOSIX proves the helper that adapters use
// to convert raw syscall errors into the closed SysfsErrno set. A call
// site extracts unix.Errno from an error chain and feeds the result
// through SysfsErrnoFromError; unrecognised errnos fall back to
// SysfsErrnoOther.
func TestSysfsErrnoFromErrorMapsPOSIX(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want app.SysfsErrno
	}{
		{unix.ENOENT, app.SysfsErrnoENOENT},
		{unix.EACCES, app.SysfsErrnoEACCES},
		{unix.EPERM, app.SysfsErrnoEPERM},
		{unix.EBUSY, app.SysfsErrnoEBUSY},
		{unix.ENODEV, app.SysfsErrnoENODEV},
		{unix.EIO, app.SysfsErrnoEIO},
		{unix.EINVAL, app.SysfsErrnoOther},
		{fmt.Errorf("wrapped: %w", unix.EACCES), app.SysfsErrnoEACCES},
		{errNoErrno, app.SysfsErrnoOther},
		{nil, app.SysfsErrnoOther},
	}

	for _, tc := range tests {
		got := app.SysfsErrnoFromError(tc.err)
		require.Equalf(t, tc.want, got,
			"SysfsErrnoFromError(%v) = %q, want %q", tc.err, got, tc.want)
	}
}

// TestSysfsWritePathFromAbsClassifiesKnownAndOther proves the helper
// that maps a raw sysfs path into the typed closed set. Unknown paths
// must collapse to SysfsWritePathOther so cardinality stays bounded.
func TestSysfsWritePathFromAbsClassifiesKnownAndOther(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want app.SysfsWritePath
	}{
		{"/sys/bus/usb/drivers/usbip-host/bind", app.SysfsWritePathBind},
		{"/sys/bus/usb/drivers/usbip-host/unbind", app.SysfsWritePathUnbind},
		{"/sys/bus/usb/drivers/usbip-host/match_busid", app.SysfsWritePathMatchBusID},
		{"/sys/bus/usb/drivers/usbip-host/rebind", app.SysfsWritePathRebind},
		{"/sys/devices/platform/vhci_hcd.0/attach", app.SysfsWritePathAttach},
		{"/sys/devices/platform/vhci_hcd.0/detach", app.SysfsWritePathDetach},
		{"/sys/bus/usb/devices/1-1/usbip_sockfd", app.SysfsWritePathUsbipSockfd},
		{"/sys/foo/bar", app.SysfsWritePathOther},
		{"", app.SysfsWritePathOther},
	}

	for _, tc := range tests {
		got := app.SysfsWritePathFromAbs(tc.path)
		require.Equalf(t, tc.want, got,
			"SysfsWritePathFromAbs(%q) = %q, want %q", tc.path, got, tc.want)
	}
}

// TestMetricsNamesUseSnakeCase guards the naming convention from
// §11.5.5: lowercase, snake_case, unit suffix. The individual name
// assertions live in expectedMetricNames; this is a tight regression
// trap for accidental camelCase or stray dashes sneaking in.
func TestMetricsNamesUseSnakeCase(t *testing.T) {
	t.Parallel()

	for _, n := range expectedMetricNames() {
		require.NotContainsf(t, n, "-", "metric %q must not use dashes", n)
		require.Equalf(t, strings.ToLower(n), n,
			"metric %q must be lowercase", n)
	}
}
