package app

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sys/unix"
)

// Metric-label typed enums. The spec §11.5.5 catalog fixes the allowed
// values for every label; call sites pass these constants so a typo
// drops to a compile error instead of a silent unbounded-cardinality
// label. The string-valued representation is intentional — prometheus
// ultimately wants strings and a fmt.Stringer wrapper would add an
// allocation per metric call on the hot path.

// SessionOutcome labels usbip_exporter_sessions_accepted_total. Closed
// set per spec §11.5.5.
type SessionOutcome string

// SessionOutcome values. Keeping the string constants grouped makes the
// closed-set contract visually enforced in source review.
const (
	OutcomeHandshakeOK     SessionOutcome = "handshake_ok"
	OutcomeRejectedACL     SessionOutcome = "rejected_acl"
	OutcomeRejectedRate    SessionOutcome = "rejected_rate"
	OutcomeRejectedCap     SessionOutcome = "rejected_cap"
	OutcomeHandshakeFailed SessionOutcome = "handshake_failed"
)

// HandshakeOp labels usbip_exporter_handshake_duration_seconds. Closed
// set per spec §11.5.5.
type HandshakeOp string

// HandshakeOp values.
const (
	HandshakeOpDevlist HandshakeOp = "devlist"
	HandshakeOpImport  HandshakeOp = "import"
)

// BindOutcome labels usbip_exporter_bind_total. Closed set per spec
// §11.5.5.
type BindOutcome string

// BindOutcome values.
const (
	BindOutcomeOK           BindOutcome = "ok"
	BindOutcomeAlreadyBound BindOutcome = "already_bound"
	BindOutcomeNotFound     BindOutcome = "not_found"
	BindOutcomePermission   BindOutcome = "permission"
	BindOutcomeError        BindOutcome = "error"
)

// UnbindOutcome labels usbip_exporter_unbind_total. Closed set per spec
// §11.5.5.
type UnbindOutcome string

// UnbindOutcome values.
const (
	UnbindOutcomeOK         UnbindOutcome = "ok"
	UnbindOutcomeNotBound   UnbindOutcome = "not_bound"
	UnbindOutcomePermission UnbindOutcome = "permission"
	UnbindOutcomeError      UnbindOutcome = "error"
)

// DisconnectReason labels usbip_exporter_disconnect_total. Closed set
// per spec §11.5.5.
type DisconnectReason string

// DisconnectReason values.
const (
	DisconnectReasonGraceful    DisconnectReason = "graceful"
	DisconnectReasonClientGone  DisconnectReason = "client_gone"
	DisconnectReasonKernelError DisconnectReason = "kernel_error"
	DisconnectReasonShutdown    DisconnectReason = "shutdown"
)

// AttachOutcome labels usbip_importer_attaches_total. Closed set per
// spec §11.5.5.
type AttachOutcome string

// AttachOutcome values.
const (
	AttachOutcomeOK               AttachOutcome = "ok"
	AttachOutcomePermission       AttachOutcome = "permission"
	AttachOutcomeNoFreePort       AttachOutcome = "no_free_port"
	AttachOutcomeProtocolMismatch AttachOutcome = "protocol_mismatch"
	AttachOutcomeDialFailed       AttachOutcome = "dial_failed"
	AttachOutcomeKernelError      AttachOutcome = "kernel_error"
)

// DetachOutcome labels usbip_importer_detaches_total. Closed set per
// spec §11.5.5.
type DetachOutcome string

// DetachOutcome values.
const (
	DetachOutcomeOK       DetachOutcome = "ok"
	DetachOutcomeNotFound DetachOutcome = "not_found"
	DetachOutcomeError    DetachOutcome = "error"
)

// ReconnectOutcome labels usbip_importer_reconnect_attempts_total.
// Closed set per spec §11.5.5.
type ReconnectOutcome string

// ReconnectOutcome values.
const (
	ReconnectOutcomeOK        ReconnectOutcome = "ok"
	ReconnectOutcomeBackoff   ReconnectOutcome = "backoff"
	ReconnectOutcomeExhausted ReconnectOutcome = "exhausted"
	ReconnectOutcomeCanceled  ReconnectOutcome = "canceled"
)

// SysfsWritePath labels usbip_adapter_sysfs_write_failures_total{path}.
// The value universe is the seven sysfs write targets the adapter
// touches (spec §5.4) plus "other" for anything that doesn't match the
// whitelist. Clamping here keeps cardinality bounded regardless of
// what raw path strings flow through the emission site.
type SysfsWritePath string

// SysfsWritePath values.
const (
	SysfsWritePathBind         SysfsWritePath = "bind"
	SysfsWritePathUnbind       SysfsWritePath = "unbind"
	SysfsWritePathMatchBusID   SysfsWritePath = "match_busid"
	SysfsWritePathRebind       SysfsWritePath = "rebind"
	SysfsWritePathAttach       SysfsWritePath = "attach"
	SysfsWritePathDetach       SysfsWritePath = "detach"
	SysfsWritePathUsbipSockfd  SysfsWritePath = "usbip_sockfd"
	SysfsWritePathOther        SysfsWritePath = "other"
)

// SysfsErrno labels usbip_adapter_sysfs_write_failures_total{errno}.
// Values are the POSIX-named errnos the sysfs write path surfaces
// (ENOENT / EACCES / EPERM / EBUSY / ENODEV / EIO) plus "other" for
// anything else. Closed set per spec §11.5.5.
type SysfsErrno string

// SysfsErrno values.
const (
	SysfsErrnoENOENT SysfsErrno = "ENOENT"
	SysfsErrnoEACCES SysfsErrno = "EACCES"
	SysfsErrnoEPERM  SysfsErrno = "EPERM"
	SysfsErrnoEBUSY  SysfsErrno = "EBUSY"
	SysfsErrnoENODEV SysfsErrno = "ENODEV"
	SysfsErrnoEIO    SysfsErrno = "EIO"
	SysfsErrnoOther  SysfsErrno = "other"
)

// SysfsErrnoFromError collapses an arbitrary error into the closed
// SysfsErrno set. Uses errors.As to walk the chain so wrapped errnos
// (fmt.Errorf("...%w...", unix.EACCES)) still classify correctly; any
// non-errno error returns SysfsErrnoOther. A nil error also returns
// SysfsErrnoOther — call sites only ever reach this helper on the
// failure branch.
func SysfsErrnoFromError(err error) SysfsErrno {
	if err == nil {
		return SysfsErrnoOther
	}

	var errno unix.Errno
	if !errors.As(err, &errno) {
		return SysfsErrnoOther
	}

	switch errno {
	case unix.ENOENT:
		return SysfsErrnoENOENT
	case unix.EACCES:
		return SysfsErrnoEACCES
	case unix.EPERM:
		return SysfsErrnoEPERM
	case unix.EBUSY:
		return SysfsErrnoEBUSY
	case unix.ENODEV:
		return SysfsErrnoENODEV
	case unix.EIO:
		return SysfsErrnoEIO
	default:
		return SysfsErrnoOther
	}
}

// SysfsWritePathFromAbs maps an absolute sysfs path to its closed-set
// label. Matching is by suffix on the final path segment for the usbip
// driver files because the busid-dependent per-device paths
// (/sys/bus/usb/devices/<busid>/usbip_sockfd) share a file name but
// not a parent. Anything that doesn't match collapses to
// SysfsWritePathOther so ad-hoc paths cannot explode cardinality.
func SysfsWritePathFromAbs(path string) SysfsWritePath {
	switch {
	case hasSysfsSuffix(path, "/usbip-host/bind"):
		return SysfsWritePathBind
	case hasSysfsSuffix(path, "/usbip-host/unbind"):
		return SysfsWritePathUnbind
	case hasSysfsSuffix(path, "/usbip-host/match_busid"):
		return SysfsWritePathMatchBusID
	case hasSysfsSuffix(path, "/usbip-host/rebind"):
		return SysfsWritePathRebind
	case hasSysfsSuffix(path, "/vhci_hcd.0/attach"):
		return SysfsWritePathAttach
	case hasSysfsSuffix(path, "/vhci_hcd.0/detach"):
		return SysfsWritePathDetach
	case hasSysfsSuffix(path, "/usbip_sockfd"):
		return SysfsWritePathUsbipSockfd
	default:
		return SysfsWritePathOther
	}
}

// hasSysfsSuffix returns true when path ends with suffix. Exists so
// the switch above stays readable without importing strings for a
// single helper; a separate func keeps the intent self-documenting.
func hasSysfsSuffix(path, suffix string) bool {
	if len(path) < len(suffix) {
		return false
	}

	return path[len(path)-len(suffix):] == suffix
}

// KernelModule labels usbip_kernel_modules_loaded. Closed set per spec
// §11.5.4 / §11.5.5.
type KernelModule string

// KernelModule values. Kept as string constants (not the ModuleState
// enum) because the label universe is the module name, not the probe
// outcome.
const (
	ModuleUsbipCore KernelModule = "usbip_core"
	ModuleVhciHcd   KernelModule = "vhci_hcd"
	ModuleUsbipHost KernelModule = "usbip_host"
	ModuleUsbipVudc KernelModule = "usbip_vudc"
)

// Metrics wraps the §11.5.5 Prometheus collector bundle. A single
// *Metrics is shared across the Importer, Exporter, and adapter layer;
// the embedded vectors are concurrency-safe under the client_golang
// contract so call sites do not need a local mutex.
//
// The zero value is deliberately NOT usable — MustNewMetrics is the
// only constructor. Using the zero value would silently publish nothing
// because every vector pointer would be nil and the typed methods guard
// on that; callers should thread the constructed bundle through their
// option funcs or explicitly opt out by passing nil (which MustNewMetrics
// accepts to produce a nop bundle).
type Metrics struct {
	// nop is true when the registerer is nil. Every typed method early-
	// returns in that case so call sites do not need a pre-call guard.
	nop bool

	exporterSessionsActive       prometheus.Gauge
	exporterSessionsAcceptedTotal *prometheus.CounterVec
	exporterHandshakeDuration    *prometheus.HistogramVec
	exporterBindTotal            *prometheus.CounterVec
	exporterUnbindTotal          *prometheus.CounterVec
	exporterDisconnectTotal      *prometheus.CounterVec

	importerAttachesTotal          *prometheus.CounterVec
	importerDetachesTotal          *prometheus.CounterVec
	importerPortsActive            prometheus.Gauge
	importerReconnectAttemptsTotal *prometheus.CounterVec

	adapterSysfsWriteFailuresTotal *prometheus.CounterVec
	kernelModulesLoaded            *prometheus.GaugeVec

	buildInfoRegisterer prometheus.Registerer
}

// MustNewMetrics constructs a §11.5.5 metric bundle and registers every
// entry against r. Duplicate-registration errors panic so a misconfigured
// caller fails at process startup instead of silently publishing half
// the catalog.
//
// A nil r yields a nop bundle: every typed accessor is a no-op. That
// lets callers pass nil when --metrics-addr is disabled without wrapping
// every call site in `if m != nil`.
func MustNewMetrics(r prometheus.Registerer) *Metrics {
	if r == nil {
		return &Metrics{nop: true}
	}

	m := &Metrics{buildInfoRegisterer: r}

	buildExporterCollectors(m, r)
	buildImporterCollectors(m, r)
	buildAdapterCollectors(m, r)
	buildGlobalCollectors(m, r)

	return m
}

// ExporterSessionAccepted increments
// usbip_exporter_sessions_accepted_total with the given outcome.
func (m *Metrics) ExporterSessionAccepted(outcome SessionOutcome) {
	if m.nop {
		return
	}

	m.exporterSessionsAcceptedTotal.WithLabelValues(string(outcome)).Inc()
}

// ExporterSessionsActive sets the usbip_exporter_sessions_active gauge
// to n. Called from the exporter whenever a session registers or
// unregisters.
func (m *Metrics) ExporterSessionsActive(n int) {
	if m.nop {
		return
	}

	m.exporterSessionsActive.Set(float64(n))
}

// ExporterHandshakeDuration observes d seconds on
// usbip_exporter_handshake_duration_seconds for the given op.
func (m *Metrics) ExporterHandshakeDuration(op HandshakeOp, d float64) {
	if m.nop {
		return
	}

	m.exporterHandshakeDuration.WithLabelValues(string(op)).Observe(d)
}

// ExporterBind increments usbip_exporter_bind_total with the given
// outcome. Called from the exporter Bind path on every completion.
func (m *Metrics) ExporterBind(outcome BindOutcome) {
	if m.nop {
		return
	}

	m.exporterBindTotal.WithLabelValues(string(outcome)).Inc()
}

// ExporterUnbind increments usbip_exporter_unbind_total with the given
// outcome.
func (m *Metrics) ExporterUnbind(outcome UnbindOutcome) {
	if m.nop {
		return
	}

	m.exporterUnbindTotal.WithLabelValues(string(outcome)).Inc()
}

// ExporterDisconnect increments usbip_exporter_disconnect_total with
// the given reason, called once per session end.
func (m *Metrics) ExporterDisconnect(reason DisconnectReason) {
	if m.nop {
		return
	}

	m.exporterDisconnectTotal.WithLabelValues(string(reason)).Inc()
}

// ImporterAttached increments usbip_importer_attaches_total with the
// given outcome. Called once per Attach completion regardless of
// success.
func (m *Metrics) ImporterAttached(outcome AttachOutcome) {
	if m.nop {
		return
	}

	m.importerAttachesTotal.WithLabelValues(string(outcome)).Inc()
}

// ImporterDetached increments usbip_importer_detaches_total with the
// given outcome. Called once per Detach completion regardless of
// success.
func (m *Metrics) ImporterDetached(outcome DetachOutcome) {
	if m.nop {
		return
	}

	m.importerDetachesTotal.WithLabelValues(string(outcome)).Inc()
}

// ImporterPortsActive sets the usbip_importer_ports_active gauge.
func (m *Metrics) ImporterPortsActive(n int) {
	if m.nop {
		return
	}

	m.importerPortsActive.Set(float64(n))
}

// ImporterReconnectAttempt increments
// usbip_importer_reconnect_attempts_total with the given outcome.
func (m *Metrics) ImporterReconnectAttempt(outcome ReconnectOutcome) {
	if m.nop {
		return
	}

	m.importerReconnectAttemptsTotal.WithLabelValues(string(outcome)).Inc()
}

// AdapterSysfsWriteFailure increments
// usbip_adapter_sysfs_write_failures_total with the given closed-set
// path + errno labels. Both arguments are typed enums so a call site
// cannot leak an unbounded raw string into the label space; see
// SysfsWritePathFromAbs and SysfsErrnoFromError for the raw-to-typed
// helpers adapters use at emission time.
func (m *Metrics) AdapterSysfsWriteFailure(path SysfsWritePath, errno SysfsErrno) {
	if m.nop {
		return
	}

	m.adapterSysfsWriteFailuresTotal.WithLabelValues(string(path), string(errno)).Inc()
}

// KernelModuleLoaded sets usbip_kernel_modules_loaded{module=name} to 1
// (loaded) or 0 (missing / unknown). Called from the status polling
// loop on every probe cycle.
func (m *Metrics) KernelModuleLoaded(name KernelModule, loaded bool) {
	if m.nop {
		return
	}

	val := 0.0
	if loaded {
		val = 1.0
	}

	m.kernelModulesLoaded.WithLabelValues(string(name)).Set(val)
}

// SetBuildInfo registers a labelled usbip_build_info gauge with value 1.
// Intended to be called exactly once from main. Calling it a second
// time with different label values is permitted but leaves the previous
// {version,commit,go_version} sample in place (standard prometheus
// GaugeVec semantics).
func (m *Metrics) SetBuildInfo(version, commit, goVersion string) {
	if m.nop {
		return
	}

	gv := m.resolveBuildInfoVec()
	if gv == nil {
		return
	}

	gv.WithLabelValues(version, commit, goVersion).Set(1)
}

// resolveBuildInfoVec registers a fresh build_info GaugeVec against the
// bundle's registerer on first call and reuses the existing collector on
// repeat calls. A name clash from another bundle's build_info (different
// label shape) returns nil so the SetBuildInfo call is a silent no-op
// rather than a process-kill panic.
func (m *Metrics) resolveBuildInfoVec() *prometheus.GaugeVec {
	gv := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "usbip_build_info",
			Help: "1-valued gauge whose labels carry build metadata.",
		},
		[]string{"version", "commit", "go_version"},
	)

	err := m.buildInfoRegisterer.Register(gv)
	if err == nil {
		return gv
	}

	var are prometheus.AlreadyRegisteredError
	if !errors.As(err, &are) {
		return nil
	}

	existing, ok := are.ExistingCollector.(*prometheus.GaugeVec)
	if !ok {
		return nil
	}

	return existing
}

// buildExporterCollectors populates and registers the `usbip_exporter_*`
// family vectors on m. Package-level func (not a method) so funcorder's
// "unexported methods after exported" rule doesn't apply; this is pure
// plumbing, not part of the Metrics public or private method surface.
func buildExporterCollectors(m *Metrics, r prometheus.Registerer) {
	m.exporterSessionsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "usbip_exporter_sessions_active",
		Help: "Current number of accepted sessions in the exporter.",
	})

	m.exporterSessionsAcceptedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "usbip_exporter_sessions_accepted_total",
			Help: "Cumulative accept events.",
		},
		[]string{"outcome"},
	)

	m.exporterHandshakeDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "usbip_exporter_handshake_duration_seconds",
			Help:    "Wall time for OP handshake completion.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"op"},
	)

	m.exporterBindTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "usbip_exporter_bind_total",
			Help: "Bind attempts by outcome.",
		},
		[]string{"outcome"},
	)

	m.exporterUnbindTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "usbip_exporter_unbind_total",
			Help: "Unbind attempts by outcome.",
		},
		[]string{"outcome"},
	)

	m.exporterDisconnectTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "usbip_exporter_disconnect_total",
			Help: "Session end reasons.",
		},
		[]string{"reason"},
	)

	r.MustRegister(
		m.exporterSessionsActive,
		m.exporterSessionsAcceptedTotal,
		m.exporterHandshakeDuration,
		m.exporterBindTotal,
		m.exporterUnbindTotal,
		m.exporterDisconnectTotal,
	)
}

// buildImporterCollectors populates and registers the `usbip_importer_*`
// family vectors on m.
func buildImporterCollectors(m *Metrics, r prometheus.Registerer) {
	m.importerAttachesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "usbip_importer_attaches_total",
			Help: "Attach attempts by outcome.",
		},
		[]string{"outcome"},
	)

	m.importerDetachesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "usbip_importer_detaches_total",
			Help: "Detach attempts by outcome.",
		},
		[]string{"outcome"},
	)

	m.importerPortsActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "usbip_importer_ports_active",
		Help: "Currently-attached vhci ports.",
	})

	m.importerReconnectAttemptsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "usbip_importer_reconnect_attempts_total",
			Help: "Reconnect attempts by auto-reconnect watcher.",
		},
		[]string{"outcome"},
	)

	r.MustRegister(
		m.importerAttachesTotal,
		m.importerDetachesTotal,
		m.importerPortsActive,
		m.importerReconnectAttemptsTotal,
	)
}

// buildAdapterCollectors populates and registers the `usbip_adapter_*`
// family on m. The {path, errno} label pair is cardinality-bounded
// because path is drawn from a hard-coded sysfs set and errno is a POSIX
// label.
func buildAdapterCollectors(m *Metrics, r prometheus.Registerer) {
	m.adapterSysfsWriteFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "usbip_adapter_sysfs_write_failures_total",
			Help: "Sysfs write errors. Path + errno are both drawn from closed sets.",
		},
		[]string{"path", "errno"},
	)

	r.MustRegister(m.adapterSysfsWriteFailuresTotal)
}

// buildGlobalCollectors populates and registers the cross-role
// `usbip_*` families on m. SetBuildInfo registers the build_info gauge
// lazily the first time it is called so the version label is known.
func buildGlobalCollectors(m *Metrics, r prometheus.Registerer) {
	m.kernelModulesLoaded = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "usbip_kernel_modules_loaded",
			Help: "1 when /sys/module/<m> exists, 0 otherwise.",
		},
		[]string{"module"},
	)

	r.MustRegister(m.kernelModulesLoaded)
}
