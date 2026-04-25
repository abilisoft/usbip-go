Iteration 2 — awaiting review

## 1. Problem + scope

Harden the importer and exporter for WAN, satellite, ground-to-rover, and mesh-radio links with roughly 50-800 ms RTT, intermittent packet loss, and 1-10 s outages, without changing v1.0.0 behavior for existing callers. The source basis for the network profile is the task profile plus challenged-network guidance in RFC 4838, which calls out high-delay links, disruption, disconnection, satellite networks, and intermittent connectivity ([RFC 4838](https://www.rfc-editor.org/rfc/rfc4838)). Socket-option recommendations use Go's `net.TCPConn` APIs for keepalive, deadlines, and buffers ([Go net package](https://pkg.go.dev/net)) and Linux TCP/socket option semantics for keepalive and buffer sizing ([tcp(7)](https://man7.org/linux/man-pages/man7/tcp.7.html), [socket(7)](https://man7.org/linux/man-pages/man7/socket.7.html)).

Files requested but not present in this tree:

- `pkg/usbip/attach_options.go`; current public attach options live in `pkg/usbip/usbip.go:94` and public option functions live in `pkg/usbip/options.go:18`.
- `pkg/usbip/importer.go`; current public importer facade lives in `pkg/usbip/usbip.go:145`.
- `pkg/usbip/exporter.go`; current public exporter facade lives in `pkg/usbip/usbip.go:219`.
- `internal/app/attach_options.go`; current internal attach options live in `internal/app/options.go:247`.

Traced behavior that drives the plan:

- Import path: `pkg/usbip.Importer.Attach` in `pkg/usbip/usbip.go:170` converts options and calls `internal/app.Importer.Attach` in `internal/app/importer.go:304`; `attachOverDialed` in `internal/app/importer.go:615` dials, sends `OP_REQ_IMPORT`, reads `OP_REP_IMPORT`, and calls `ImporterKernel.AttachRemote`; `internal/adapter/kernel.AttachRemote` in `internal/adapter/kernel/attach.go:49` passes the socket fd to the kernel sysfs `attach` path.
- Export path: `pkg/usbip.Exporter.Serve` in `pkg/usbip/usbip.go:250` calls `internal/app.Exporter.Serve` in `internal/app/exporter.go:250`; `handleConn` in `internal/app/session.go:64` decodes `OP_REQ_DEVLIST` or `OP_REQ_IMPORT`; `serveImport` in `internal/app/session.go:247` calls `ExporterKernel.ExportOnConn`; `internal/adapter/kernel.ExportOnConn` in `internal/adapter/kernel/export.go:23` passes the socket fd to `usbip_sockfd`.
- The kernel adapters do not call `dup`: `attachAtPort` extracts the fd at `internal/adapter/kernel/attach.go:121`, writes it at `internal/adapter/kernel/attach.go:128`, then closes the caller's conn after the kernel has obtained its own ref at `internal/adapter/kernel/attach.go:134`; `extractFD` only reads the raw fd at `internal/adapter/kernel/attach.go:193`; exporter extracts and writes the fd at `internal/adapter/kernel/export.go:29` and `internal/adapter/kernel/export.go:34`. Go `net.Conn` deadlines are therefore only useful for userspace OP handshakes, not for the kernel-owned URB session.
- URB traffic is not decoded by `internal/adapter/wire`; that package only handles operation headers, device-list messages, and import request/response messages. After fd handoff, kernel USB/IP owns the URB stream, so userspace cannot currently count in-flight URBs or measure URB RTT.
- Current transport behavior in `internal/adapter/transport/transport.go:72` uses a zero `net.Dialer{}` and applies `TCP_NODELAY` to dialed TCP connections. `Listen` in `internal/adapter/transport/transport.go:139` uses a zero `net.ListenConfig{}` and does not tune accepted connections.
- Current reconnect defaults are LAN-shaped and also protect against missed local kernel events: `defaultStatusPollInterval = 5s`, backoff floor `1s`, ceiling `60s`, jitter `0.2` in `internal/app/reconnect.go:19`.

Scope for v1.x is additive configuration through option functions, static deadlines for visible OP handshakes, socket tuning, reconnect backoff constructors, exporter half-open cleanup, and bounded metrics. All defaults and all zero-valued new fields must preserve current behavior.

## 2. Non-goals

- No `pkg/usbip` API breaks: no renames, removals, changed meanings for existing options, or required constructor parameters.
- No new fields on existing exported structs, including `pkg/usbip.AttachOptions`, because the API gate uses `apidiff` and this repo already has many keyed public struct literal uses.
- No string-based public `BackoffPreset`; use typed constructor functions such as `NewWANBackoff()` and existing option functions instead.
- No network knobs in `pkg/domain`; domain types such as `domain.RemoteEndpoint`, `domain.Session`, `domain.Port`, and `domain.Event` remain transport-agnostic.
- No `internal/app` imports of `internal/adapter/kernel` or `internal/adapter/transport`; app continues to declare interfaces and adapters continue to implement them.
- No adaptive deadline controller in v1.x. Use static, user-configured read/write deadlines only.
- No userspace URB pipelining implementation in v1.x. The current userspace code does not own URB packet ordering after fd handoff, and adding a userspace proxy would be a protocol and architecture change.
- No application-layer USB/IP ping in v1.x. The protocol path visible here has no ping opcode, and inventing one would not interoperate with existing peers.
- No automatic WAN or satellite increase of `StatusPollInterval`; the current `5s` poll is the detection backstop when local kernel events are missed.
- No new domain tests for this work. Domain is intentionally untouched.

## 3. Per-layer changes (Domain / App / Adapter / Public facade), each with file:line + proposed type signature + RED-test names

Domain:

- Target: `pkg/domain/endpoint.go:38`, `pkg/domain/session.go:33`, `pkg/domain/events.go:10`, and the rest of `pkg/domain/*.go`.
- Proposed signature or field: no code change. Keep domain entities free of TCP keepalive, socket buffer, deadline, retry, polling, and metrics configuration.
- RED-test names: none for domain by design; compliance gate should instead assert no `pkg/domain` diff and no new non-domain imports.
- Risk and rollback: accidentally adding network state to domain would violate the DDD boundary. Rollback is to move any such field to `pkg/usbip` facade types or `internal/app` config before merge.

App:

- Target: `internal/app/interfaces.go:82`.
- Proposed type signature:

  ```go
  type TransportOptions struct {
      DialConnectTimeout   time.Duration
      TCPKeepAliveIdle     time.Duration
      TCPKeepAliveInterval time.Duration
      TCPKeepAliveProbes   int
      SendBufferBytes      int
      ReceiveBufferBytes   int
      ReadDeadline         time.Duration
      WriteDeadline        time.Duration
  }

  type Transport interface {
      Dial(ctx context.Context, endpoint domain.RemoteEndpoint, opts TransportOptions) (net.Conn, error)
      Listen(ctx context.Context, addr string, opts TransportOptions) (net.Listener, error)
  }
  ```

- RED-test names: `TestImporterListRemotePassesTransportOptions` asserts a fake transport records the exact options from importer config; `TestImporterAttachPassesImporterTransportOptions` asserts attach uses importer-level transport options; `TestImporterTransportOptionsZeroValuePreservesDialCall` asserts the zero struct is passed and does not force behavior in fakes.
- Risk and rollback: this changes an internal interface and all app fakes. Rollback is mechanical: remove the `opts` parameter and keep the public options inert until a later PR.

- Target: `internal/app/options.go:20`, `internal/app/options.go:82`, and `internal/app/options.go:247`.
- Proposed fields:

  ```go
  type importerConfig struct {
      transportOptions TransportOptions
  }

  type exporterConfig struct {
      transportOptions TransportOptions
      idleTimeout      time.Duration
  }

  // AttachOptions remains unchanged for transport in v1.x.
  ```

- RED-test names: `TestImporterConfigTransportOptionsDefaultZero` asserts default internal config is all zero; `TestExporterConfigTransportOptionsDefaultZero` asserts default exporter config is all zero; `TestAppTransportOptionsRejectNegativeValues` asserts validation rejects negative durations, probes, and buffer sizes before adapters are constructed.
- Risk and rollback: option plumbing can silently drop values. Rollback is to keep `TransportOptions` internal-only through PR 1a until conversion tests pass.

- Target: `internal/app/importer.go:248` and `internal/app/importer.go:621`.
- Proposed call-site changes:

  ```go
  conn, err := i.transport.Dial(ctx, endpoint, i.transportOptions)
  ```

  Do not add per-attach transport merge semantics in v1.x. Read/write deadlines apply only to Go-visible OP handshakes; do not add a deadline-clearing step before `AttachRemote` because the kernel adapter passes the fd to sysfs and the Go deadline is not part of the kernel-owned session.

- RED-test names: `TestImporterAttachSlowHandshakeHonorsReadDeadline` uses a fake conn that blocks past the configured read deadline and asserts attach fails before kernel handoff; `TestImporterListRemoteSlowPeerHonorsReadDeadline` asserts `ListRemote` returns a timeout error instead of blocking indefinitely; `TestImporterAttachDoesNotRequireDeadlineClearBeforeKernelHandoff` documents that no pre-handoff clear call is required.
- Risk and rollback: deadline tests can become timing-sensitive. Rollback is to implement deadline behavior in fake-clock/fake-conn unit tests only and keep adapter integration tests separate.

- Target: `internal/app/reconnect.go:19`, `internal/app/reconnect.go:96`, and `internal/app/reconnect.go:364`.
- Proposed change: keep `StatusPollInterval` default at `5s` for LAN, WAN, and satellite examples; use new public backoff constructors to change retry spacing only. Recommended WAN backoff: min `2s`, max `2m`, jitter `0.35`. Recommended satellite backoff: min `5s`, max `5m`, jitter `0.5`. Do not add `MaxAttempts` changes to presets; attempts are policy and remain caller-controlled through existing `AttachOptions.MaxAttempts`.
- RED-test names: `TestResolveReconnectOptionsDefaultPollIntervalUnchangedForWANDocs` asserts helper examples still leave poll at `5s`; `TestNewWANBackoffConfig` and `TestNewSatelliteBackoffConfig` assert constructor shapes; `TestRunReconnectLoopCustomPollIntervalStillHonored` asserts callers can explicitly raise or lower poll cadence.
- Risk and rollback: keeping `5s` polling may cost local sysfs reads on very low-power systems, but avoids a 15-30s blind detach window. Rollback is documentation-only: advise manual `StatusPollInterval` tuning when local polling cost is measured.

- Target: `internal/app/exporter.go:26`, `internal/app/exporter.go:667`, and `internal/app/session.go:300`.
- Proposed fields and enum:

  ```go
  type Exporter struct {
      transportOptions TransportOptions
      idleTimeout      time.Duration
  }

  const DisconnectReasonIdleTimeout DisconnectReason = "idle_timeout"
  ```

  Add an optional exporter idle reaper in `runRegisteredSession`: when `idleTimeout > 0`, close the session connection and call `kernel.Disconnect(busID)` if no app-visible end event arrives before the timer. Zero disables it. Do not add an importer idle watcher based on last URB completion in v1.x because userspace cannot observe URB completions after `AttachRemote`.

- RED-test names: `TestExporterIdleTimeoutZeroDisabled` asserts a blocked session is not reaped when zero; `TestExporterIdleTimeoutDisconnectsKernelSession` asserts timeout closes the connection and calls `Disconnect`; `TestExporterSessionReapedMetricRecordsIdleTimeout` asserts the reaped counter increments with reason `idle_timeout`.
- Risk and rollback: exporter idle timeout can terminate a valid but quiet kernel-owned USB/IP session. Rollback is to leave the option documented but disabled by default, or to move it to Deferred if field tests show false positives.

- Target: `internal/app/metrics.go:41`, `internal/app/metrics.go:483`, and `internal/app/metrics.go:551`.
- Proposed fields:

  ```go
  transportDialDuration metricHistogramVec
  transportSocketOptions metricCounterVec
  reconnectDelay metricHistogramVec
  exporterSessionsReaped metricCounterVec
  ```

- RED-test names: `TestMetricsTransportSocketOptionsCounterLabelsBounded` asserts labels are in a fixed set; `TestMetricsReconnectDelayHistogramBuckets` asserts WAN delay observations land in configured buckets; `TestMetricsExporterSessionsReapedCounter` asserts idle reaping records `reason="idle_timeout"`.
- Risk and rollback: metric cardinality can grow if labels include peer addresses or bus IDs. Rollback is to remove all dynamic labels and keep only role, option, operation, outcome, preset, and reason.

Adapter:

- Target: `internal/adapter/transport/transport.go:28`, `internal/adapter/transport/transport.go:72`, and `internal/adapter/transport/transport.go:139`.
- Proposed implementation signatures:

  ```go
  func (t *NetTransport) Dial(ctx context.Context, r domain.RemoteEndpoint, opts app.TransportOptions) (net.Conn, error)
  func (t *NetTransport) Listen(ctx context.Context, addr string, opts app.TransportOptions) (net.Listener, error)
  func tuneTCPConn(ctx context.Context, conn *net.TCPConn, opts app.TransportOptions, role string) error
  ```

  `DialConnectTimeout > 0` maps to `net.Dialer.Timeout`. Positive buffer values call `SetWriteBuffer` and `SetReadBuffer`; zero inherits kernel defaults. Positive keepalive fields enable TCP keepalive and call `SetKeepAliveConfig` directly; `go.mod:3` requires Go `1.26.2`, so no fallback path is needed. `Listen` returns a wrapper listener whose `Accept` tunes accepted TCP connections with `TCP_NODELAY`, keepalive, buffers, and initial deadlines. Zero-valued options preserve today's dial and listen behavior except for the existing dial-side `TCP_NODELAY`.

- RED-test names: `TestDialAppliesConnectTimeout` asserts the dialer honors configured timeout through an injected dial hook; `TestDialAppliesSocketBuffersLinuxIntegration` asserts `SO_SNDBUF` and `SO_RCVBUF` on loopback with Linux doubling tolerance; `TestDialAppliesKeepAliveConfigLinuxIntegration` asserts `SO_KEEPALIVE` and TCP keepalive values; `TestListenAcceptAppliesSocketOptionsLinuxIntegration` asserts accepted conns receive keepalive and buffers; `TestTransportOptionsZeroValueLeavesAcceptedConnUntuned` asserts optional knobs are not applied when opts are zero.
- Risk and rollback: low-level socket assertions can be flaky on busy CI. Rollback is to gate syscall-pinning tests behind `//go:build integration && linux` and keep unit tests on injected tuning functions.

- Target: `internal/adapter/kernel/attach.go:49` and `internal/adapter/kernel/export.go:23`.
- Proposed implementation change: no socket-tuning fields in kernel adapters. Add no API. Do not add deadline-clearing behavior around kernel handoff; deadlines are userspace handshake concerns only.
- RED-test names: no kernel-specific RED test required; app tests verify handshake timeout behavior and absence of a required pre-handoff clear call.
- Risk and rollback: adding socket settings here would couple kernel sysfs code to transport policy. Rollback is to keep all tuning in transport/app layers.

- Target: `internal/adapter/wire/*.go`.
- Proposed implementation change: no URB parser and no pipelining change. Keep wire focused on OP headers, devlist, and import request/response.
- RED-test names: none in v1.x for URB pipelining; Deferred work would start with `TestDecodeSubmitURBTracksSequence` only if a future userspace URB observer/proxy is accepted.
- Risk and rollback: adding partial URB decoding without ownership of the stream would be misleading and fragile. Rollback is to leave URB metrics Deferred.

Public facade:

- Target: `pkg/usbip/options.go:18`, `pkg/usbip/options.go:87`, and `pkg/usbip/usbip.go:94`.
- Proposed public API:

  ```go
  type TransportOptions struct {
      DialConnectTimeout   time.Duration
      TCPKeepAliveIdle     time.Duration
      TCPKeepAliveInterval time.Duration
      TCPKeepAliveProbes   int
      SendBufferBytes      int
      ReceiveBufferBytes   int
      ReadDeadline         time.Duration
      WriteDeadline        time.Duration
  }

  func WithImporterTransportOptions(opts TransportOptions) ImporterOption
  func WithExporterTransportOptions(opts TransportOptions) ExporterOption
  func WithExporterIdleTimeout(d time.Duration) ExporterOption
  ```

  Do not add `Transport` to `pkg/usbip.AttachOptions`; existing public uses include struct literals in `README.md:114`, `examples/reconnect/main.go:97`, `cmd/usbip-go/attach.go:214`, and multiple tests. Validation fires at constructor time: `NewImporter` and `NewExporter` return an error before constructing adapters if transport options or idle timeout are invalid.

- RED-test names: `TestWithImporterTransportOptionsStoresOptions` asserts facade config conversion into app config; `TestWithExporterTransportOptionsStoresOptions` asserts exporter conversion; `TestNewImporterRejectsNegativeTransportOptions` asserts invalid importer transport options fail at constructor time; `TestNewExporterRejectsNegativeTransportOptions` asserts invalid exporter transport options fail at constructor time; `TestZeroTransportOptionsMatchesCurrentDefaults` asserts zero options produce no nonzero internal fields.
- Risk and rollback: adding public fields to the new `TransportOptions` type is permanent once released. Rollback before v1.0.0 is to make transport tuning purely option-function based and leave the struct unexported.

- Target: `pkg/usbip/backoff.go:61` and `pkg/usbip/options.go:42`.
- Proposed public API:

  ```go
  func NewWANBackoff() BackoffStrategy
  func NewSatelliteBackoff() BackoffStrategy
  ```

  Drop `BackoffPreset` and `WithImporterBackoffPreset`. Callers compose existing APIs instead: `WithImporterBackoff(NewWANBackoff())`, `WithImporterStatusPollInterval(d)` when they intentionally want a non-default poll interval, and existing per-attach `AttachOptions.Backoff` when a single attach needs different retry behavior.

- RED-test names: `TestNewWANBackoffConfig` asserts min `2s`, max `2m`, jitter `0.35`; `TestNewSatelliteBackoffConfig` asserts min `5s`, max `5m`, jitter `0.5`; `TestNewBackoffConstructorsDoNotChangeDefaultImporterBackoff` asserts defaults remain current until a caller opts in.
- Risk and rollback: constructor functions are narrower than presets but require callers to compose poll intervals manually. Rollback is documentation-only because the existing `WithImporterBackoff` and `WithImporterStatusPollInterval` options already support composition.

- Target: `pkg/usbip/defaults_linux.go:22` and `pkg/usbip/defaults_linux.go:73`.
- Proposed construction change: convert public `TransportOptions` to `internal/app.TransportOptions` and pass through app constructors; keep `transport.New()` construction isolated in the public Linux default factory.
- RED-test names: `TestNewImporterWithTransportOptionsBuildsDefaultLinuxTransport` and `TestNewExporterWithTransportOptionsBuildsDefaultLinuxTransport` under Linux assert options survive facade-to-app conversion.
- Risk and rollback: default factories are where public facade and concrete adapters meet; a bad conversion can silently drop options. Rollback is to unit-test conversion as a pure helper and keep factory changes tiny.

## 4. New options / metrics / docs

New public option semantics:

| Field or option | Zero-value behavior | Nonzero behavior |
|---|---|---|
| `TransportOptions.DialConnectTimeout` | Use today's zero `net.Dialer{}` connect behavior. | Set connect timeout on outbound importer dials. |
| `TransportOptions.TCPKeepAliveIdle` | Do not override current Go/OS keepalive behavior. | Configure idle time before TCP keepalive probes. |
| `TransportOptions.TCPKeepAliveInterval` | Do not override current Go/OS keepalive interval. | Configure interval between TCP keepalive probes. |
| `TransportOptions.TCPKeepAliveProbes` | Do not override current Go/OS probe count. | Configure probe count. |
| `TransportOptions.SendBufferBytes` | Inherit kernel `SO_SNDBUF` default. | Call `SetWriteBuffer` with this value. |
| `TransportOptions.ReceiveBufferBytes` | Inherit kernel `SO_RCVBUF` default. | Call `SetReadBuffer` with this value. |
| `TransportOptions.ReadDeadline` | No configured read deadline. | Set static read deadline for visible userspace OP handshakes only. |
| `TransportOptions.WriteDeadline` | No configured write deadline. | Set static write deadline for visible userspace OP handshakes only. |
| `WithImporterTransportOptions` | Existing importer transport behavior. | Applies to `ListRemote` and all attaches on that importer. |
| `WithExporterTransportOptions` | Existing exporter behavior for caller-provided listeners; no tuning unless exporter creates/listens through the transport adapter. | Tunes accepted connections when the transport adapter owns `Listen`; document that caller-owned listeners must be pre-tuned by the caller. |
| `WithExporterIdleTimeout` | Disabled. | Reap app-visible exporter sessions that never receive a disconnect event before the timeout. |
| `NewWANBackoff()` | Not active unless passed to existing backoff options or `AttachOptions.Backoff`. | Backoff min `2s`, max `2m`, jitter `0.35`; poll remains current `5s` unless caller sets it. |
| `NewSatelliteBackoff()` | Not active unless passed to existing backoff options or `AttachOptions.Backoff`. | Backoff min `5s`, max `5m`, jitter `0.5`; poll remains current `5s` unless caller sets it. |

Recommended static deadline table for docs:

| Link class | RTT guide | Connect timeout | Read deadline | Write deadline | Keepalive guide | Status poll |
|---|---:|---:|---:|---:|---|---:|
| LAN/current default | `<10 ms` | zero/current | zero/current | zero/current | zero/current | `5s` |
| WAN | `50-150 ms` | `10-15s` | `30s` | `30s` | idle `30s`, interval `10s`, probes `6` | `5s` default; tune manually only if local poll cost is measured |
| High-latency satellite/mesh | `150-800 ms` | `30s` | `60-120s` | `60-120s` | idle `60s`, interval `10s`, probes `9` | `5s` default; tune manually only if local poll cost is measured |
| Intermittent 1-10 s outage | variable | `30-60s` | `120s` | `120s` | idle `60s`, interval `10s`, probes `9` | `5s` default |

Metrics to add in v1.x:

| Metric | Type | Labels | Scrape cost note |
|---|---|---|---|
| `usbip_transport_dial_duration_seconds` | histogram | `role` (`importer`, `exporter` if future outbound exporter exists), `outcome` (`success`, `error`, `timeout`) | One histogram per bounded role/outcome combination; no peer or bus labels. |
| `usbip_transport_socket_options_total` | counter | `role`, `operation` (`dial`, `accept`), `option` (`nodelay`, `keepalive`, `sndbuf`, `rcvbuf`, `deadline`), `outcome` (`success`, `error`) | At most 2 x 2 x 5 x 2 series; no dynamic labels. |
| `usbip_importer_reconnect_delay_seconds` | histogram | `profile` (`default`, `wan`, `satellite`, `custom`) | One observation per reconnect sleep, low volume. |
| `usbip_exporter_sessions_reaped_total` | counter | `reason` (`idle_timeout`, `shutdown`) | Bounded labels; this subsumes a separate idle-timeout-only counter. |

Metrics explicitly Deferred:

| Metric | Type | Labels | Scrape cost note |
|---|---|---|---|
| `usbip_importer_urbs_inflight` | gauge | ideally `direction` only; no bus ID or peer labels | Deferred because userspace does not see URB submit/completion after fd handoff. |
| `usbip_importer_urb_rtt_seconds` | histogram | ideally `direction` and coarse transfer type only | Deferred because userspace cannot time URB completion without kernel instrumentation, eBPF, or a userspace proxy. |

Docs to update:

- Target: `docs/architecture.md:117`. Clarify that current code sets `TCP_NODELAY` on dialed TCP connections; PR 1b will extend tuning to accepted connections when the transport adapter owns `Accept`.
- Target: `docs/architecture.md:29` and `docs/architecture.md:72`. Add a short DDD note: public facade owns user-facing knobs, app owns abstract transport options and policy, adapter transport applies socket settings, domain remains unchanged.
- Target: new docs section or README snippet. Add the deadline table above, a compatibility paragraph stating that zero-valued transport options inherit current behavior/kernel defaults, and examples that use option functions rather than adding fields to `AttachOptions`.

## 5. Phasing — PR 1 vs PR 2 vs Deferred

PR 1a: internal plumbing with no public surface.

- Add internal `app.TransportOptions`, change the app `Transport` interface, and update all app fakes with RED tests first.
- Thread zero-valued options through importer/exporter internals and default Linux factories, but do not expose `pkg/usbip.TransportOptions` yet.
- Add validation helpers and pure conversion tests.
- Keep adapter behavior unchanged except accepting the new internal options parameter and ignoring zero values.

PR 1b: public transport surface and adapter implementation.

- Add public `pkg/usbip.TransportOptions`, `WithImporterTransportOptions`, and `WithExporterTransportOptions`.
- Implement `DialConnectTimeout`, `SO_SNDBUF`, `SO_RCVBUF`, keepalive via `SetKeepAliveConfig`, accepted-connection tuning, and static read/write deadlines for userspace OP handshakes.
- Add transport metrics and docs.
- Add Linux socket-option integration tests behind `//go:build integration && linux`, with unit tests covering injected tuning paths.

PR 2: WAN backoff constructors, exporter cleanup, and remaining metrics.

- Add `NewWANBackoff` and `NewSatelliteBackoff`; do not add string presets.
- Preserve the current `5s` status poll in examples; document manual poll tuning tradeoffs.
- Add exporter idle reaper behind `WithExporterIdleTimeout`; keep zero disabled.
- Add reconnect delay and exporter session reaped metrics.
- Add WAN documentation examples that combine transport options, backoff constructors, and existing attach options.

Deferred:

- Userspace URB pipelining or URB metrics. Rationale: the app hands the fd to the kernel; `internal/adapter/wire` does not own the URB stream, and protocol ordering/session ownership would require a larger design.
- Adaptive deadlines. Rationale: adaptive control can turn transient loss into self-inflicted disconnects and needs production feedback loops; v1.x should use explicit static knobs.
- Application-layer heartbeat/ping. Rationale: visible USB/IP operations here do not define a ping, and nonstandard heartbeats would not interoperate with existing peers.
- Importer idle watcher based on last URB completion. Rationale: the required timestamp is not available in userspace today.
- Per-attach transport options. Rationale: adding a field to `pkg/usbip.AttachOptions` is avoidable public API churn before v1.0.0.
- Public exporter `Listen` helper. Rationale: current public API is `Serve(ctx, net.Listener)`; adding a facade-owned listener helper is useful but not required to harden existing users.

## 6. Risk register

| Risk | Impact | Mitigation | Rollback |
|---|---|---|---|
| Zero-value options accidentally change current behavior | v1.0.0 regression | Add explicit zero-value tests at public, app, and transport layers | Revert option plumbing while keeping docs branch out of release |
| Public struct growth trips API gate | CI failure or real source compatibility risk | Do not add fields to existing exported structs; document option-function usage | Drop public field additions before merge |
| New `TransportOptions` struct becomes a future compatibility trap | Future fields may require BREAKING commits | Keep the field set narrow; prefer adding option functions later instead of extending structs | Convert to unexported config plus public option functions before v1.0.0 |
| Socket option tests are flaky | CI instability | Gate syscall-pinning tests behind `integration && linux`; use injected unit tests for normal CI | Remove integration job from required checks while keeping manual coverage |
| Deadlines are mistaken for kernel-session timeouts | Misconfigured users expect URB liveness control | Document deadlines as userspace OP handshake only | Rename docs/examples to `HandshakeReadDeadline`/`HandshakeWriteDeadline` before release if needed |
| Exporter idle timeout kills valid quiet sessions | Data-plane disruption | Keep zero disabled; document as WAN half-open cleanup only; metric every reap | Remove `WithExporterIdleTimeout` before release or leave option undocumented until proven |
| Poll cadence tuned too high | Longer blind window after missed local event | Keep `5s` default in WAN/satellite docs | Documentation-only rollback |
| Metric cardinality grows | Prometheus scrape and storage pressure | No peer, bus ID, endpoint, or error-string labels | Drop dynamic labels and keep counters process-wide |
| App interface churn breaks tests broadly | PR noise | Isolate in PR 1a and update fakes mechanically | Use transport construction options instead of per-call interface options |

## 7. Compliance Gate impact (coverage, api-surface diff, etc.)

TDD matrix:

| Layer | RED test name | Assertion |
|---|---|---|
| Domain | none | No domain behavior change; gate checks no `pkg/domain` diff for this work. |
| Public facade | `TestWithImporterTransportOptionsStoresOptions` | Importer option stores and converts every `TransportOptions` field. |
| Public facade | `TestWithExporterTransportOptionsStoresOptions` | Exporter option stores and converts every `TransportOptions` field. |
| Public facade | `TestNewImporterRejectsNegativeTransportOptions` | Invalid transport options fail from `NewImporter`, not later during `Attach`. |
| Public facade | `TestNewExporterRejectsNegativeTransportOptions` | Invalid transport options fail from `NewExporter`, not later during `Serve`. |
| Public facade | `TestZeroTransportOptionsMatchesCurrentDefaults` | Zero-valued public options convert to zero-valued internal options. |
| Public facade | `TestNewWANBackoffConfig` | WAN backoff min/max/jitter match `2s`/`2m`/`0.35`. |
| Public facade | `TestNewSatelliteBackoffConfig` | Satellite backoff min/max/jitter match `5s`/`5m`/`0.5`. |
| App | `TestImporterListRemotePassesTransportOptions` | Fake transport receives importer transport options. |
| App | `TestImporterAttachPassesImporterTransportOptions` | Attach uses importer-level transport options. |
| App | `TestImporterAttachSlowHandshakeHonorsReadDeadline` | Slow fake transport causes deadline error before kernel handoff. |
| App | `TestImporterListRemoteSlowPeerHonorsReadDeadline` | Slow fake transport causes deadline error, not a hang. |
| App | `TestResolveReconnectOptionsDefaultPollIntervalUnchangedForWANDocs` | WAN/satellite examples do not change the `5s` poll default. |
| App | `TestRunReconnectLoopCustomPollIntervalStillHonored` | Caller-supplied poll interval still drives the fake-clock loop. |
| App | `TestExporterIdleTimeoutZeroDisabled` | Zero idle timeout never reaps a blocked session. |
| App | `TestExporterIdleTimeoutDisconnectsKernelSession` | Nonzero timeout closes conn and calls `Disconnect`. |
| App | `TestMetricsTransportSocketOptionsCounterLabelsBounded` | Metrics avoid unbounded labels. |
| Adapter transport | `TestDialAppliesConnectTimeout` | Unit test with injected dial hook proves timeout plumbing. |
| Adapter transport | `TestDialAppliesSocketBuffersLinuxIntegration` | Loopback conn has requested `SO_SNDBUF`/`SO_RCVBUF` with `actual >= requested` tolerance. |
| Adapter transport | `TestDialAppliesKeepAliveConfigLinuxIntegration` | Dialed TCP conn has keepalive enabled and requested idle/interval/probe values where supported by Linux. |
| Adapter transport | `TestListenAcceptAppliesSocketOptionsLinuxIntegration` | Accepted TCP conn receives keepalive and buffer tuning. |
| Adapter transport | `TestTransportOptionsZeroValueLeavesAcceptedConnUntuned` | Optional knobs are not applied when options are zero. |
| Wire | none in v1.x | No URB parser/pipelining change. |
| Kernel adapter | none directly | Kernel adapter API stays unchanged; app tests cover visible handshake deadline behavior. |

Existing test patterns to reuse:

- Use table-driven tests and `t.Parallel` as in `internal/adapter/transport/transport_test.go` and `pkg/usbip/backoff_test.go`.
- Use `stretchr/testify/require` as in `internal/app/importer_test.go`, `internal/app/reconnect_test.go`, and `pkg/usbip/options_test.go`.
- Use fake clocks and event registries from `internal/app/reconnect_test.go` for poll-cadence assertions.
- Use fake conns with deadline hooks from `internal/app/importer_test.go` for read/write deadline tests.
- Use loopback listeners and deadline-protected accepts from `internal/app/exporter_limits_test.go` and `internal/adapter/transport/transport_test.go`.
- Keep syscall-pinning socket tests in transport under `//go:build integration && linux`; normal CI should exercise injected socket tuning units instead.

Compliance commands:

- Unit scope: `go test ./pkg/domain ./pkg/usbip ./internal/app ./internal/adapter/transport ./internal/adapter/wire`.
- Race-sensitive scope after PR 2: `go test -race ./internal/app ./internal/adapter/transport`.
- Linux socket option integration scope: `go test -tags integration ./internal/adapter/transport`.
- API surface diff: `.github/workflows/ci.yml:126` runs `apidiff` against `api/pkg_usbip.json` and `api/pkg_domain.json`, failing on `Incompatible` or `Removed` output unless the commit subject begins `BREAKING:` at `.github/workflows/ci.yml:159`. Avoid that path by adding option functions and new types only; do not add fields to existing exported structs such as `pkg/usbip.AttachOptions`.
- Public struct literal evidence: `pkg/usbip.AttachOptions` is currently used with keyed struct literals in `README.md:114`, `examples/reconnect/main.go:97`, `cmd/usbip-go/attach.go:214`, and public tests, so the plan must not require expanding it.
- Coverage expectation: app and transport coverage should rise around option conversion, reconnect constructors, deadline handling, and socket tuning; domain coverage is unchanged because domain has no behavior change.
- Architecture gate: static review or grep should confirm `internal/app` does not import `internal/adapter/kernel` or `internal/adapter/transport`, and `pkg/domain` receives no network/backoff/metrics knobs.
