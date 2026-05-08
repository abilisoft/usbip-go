# Remove the metrics layer; observability via journald + sysfs + systemd

The library no longer ships a Prometheus metrics layer. There is no
`internal/app/metrics.go`, no `*Metrics` field on Importer or Exporter,
no `WithImporterMetricsRegisterer` / `WithExporterMetricsRegisterer`
public option, and no `prometheus/client_golang` dependency.

A pre-existing draft of this project carried twelve typed metric
methods (`ImporterAttached`, `ExporterSessionsActive`,
`ImporterReconnectAttempt`, etc.), a Prometheus registerer plumbed
through the public facade, and ~600 lines of metrics + tests. After
inventorying what each metric actually told an operator that journald
and sysfs did not already tell them, only two — handshake-latency
histogram and aggregated reconnect-failure rate — were genuinely
Prometheus-only. The other ten duplicated information already visible
through `usbip-go list`, `/sys/devices/.../vhci_hcd*/status`, `lsmod`, and
the structured slog calls that wrap every emission site.

For a USB forwarder running on a host (not a 10k-rps service), the
realistic operator toolchain is:

- `systemctl status usbip-go` — service liveness, restart count, last
  failure
- `journalctl -u usbip-go` — structured slog records with outcome,
  busid, port id, error fields
- `usbip-go list -e` / sysfs — active sessions, ports, kernel module
  state
- `ss -tlnp` — listening on 3240
- node_exporter — host-level CPU, memory, fd, socket metrics

PromQL p99 latencies and per-label rate aggregations are overkill at
this scale, and adding them costs every library consumer ~30
transitive packages and an abstraction layer in the public API.

Kept on the daemon binary: minimal `/healthz` and `/readyz` HTTP
handlers over a Unix socket, written against `net/http` only. They
return 200 / 503 based on systemd-readable state (kernel modules
loaded, listener bound). No prometheus dep.

Kept in the codebase: the typed outcome enums (`AttachOutcome`,
`ReconnectOutcome`, `BindOutcome`, `UnbindOutcome`, `DetachOutcome`,
`SessionOutcome`, `DisconnectReason`, `HandshakeOp`, `KernelModule`).
They are domain language used as `slog.String("outcome",
string(outcome))` fields so journald queries stay structured.

`SysfsWritePath` and `SysfsErrno` were inventoried alongside the
above during the metrics removal and removed as dead — no slog call
site referenced them. The kernel-adapter classifies sysfs-write
failures inline (e.g. `classifyKernelAttachErr`) at the boundary
where the errno surfaces, so a separate enum carried no benefit.

If a downstream operator genuinely needs Prometheus metrics, the
recommended path is a sidecar that parses `journalctl --output=json`
or scrapes sysfs, not adding the library back into a dependency
graph.

## Migration note

The previous `--metrics-addr` flag is removed and replaced by
`--health-addr`, which serves the same `/healthz` and `/readyz`
endpoints (no `/metrics`). Operators upgrading from a build that
used `--metrics-addr` will see an unknown-flag error at startup and
must update systemd units, container manifests, and config-management
templates. The flag rename is a deliberate hard break: silently
mapping `--metrics-addr` to `--health-addr` would mask the loss of
the `/metrics` endpoint and leave Prometheus scrape configs failing
silently with stale data.
