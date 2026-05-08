# Operating the usbip-go daemon

This document covers installation, systemd integration, the status
UDS, metrics scraping, and health/readiness endpoints for
production deployments of `usbipd`.

## Installation

Three supported install paths:

1. **Pre-built release archive** from GitHub Releases:

   ```
   curl -LO https://github.com/abilisoft/usbip-go/releases/download/vX.Y.Z/usbip-go_vX.Y.Z_linux_amd64.tar.gz
   tar xzf usbip-go_vX.Y.Z_linux_amd64.tar.gz
   sudo install -m 0755 usbip-go usbipd-go /usr/bin/
   sudo install -Dm 0644 contrib/systemd/usbipd-go.service /etc/systemd/system/usbipd-go.service
   sudo install -Dm 0644 contrib/systemd/usbipd-go.socket  /etc/systemd/system/usbipd-go.socket
   ```

2. **Debian / RPM package** from the release assets. Install via
   your package manager:

   ```
   sudo dpkg -i usbip-go_X.Y.Z_amd64.deb
   # or
   sudo rpm -i usbip-go-X.Y.Z.x86_64.rpm
   ```

   Packages drop the binaries under `/usr/bin`, the systemd units
   under `/usr/lib/systemd/system`.

3. **Source build** for development:

   ```
   go install github.com/abilisoft/usbip-go/cmd/usbip-go@latest
   go install github.com/abilisoft/usbip-go/cmd/usbipd-go@latest
   ```

Kernel modules must be loadable on the target host:

```
sudo modprobe usbip_core vhci-hcd usbip-host
```

Add the modules to `/etc/modules-load.d/usbip-go.conf` for
persistence across reboots.

## Systemd units

The project ships two unit files under
[`contrib/systemd/`](../contrib/systemd/). Both are intentionally
minimal — operators are expected to copy and customise.

### `usbipd-go.socket`

```ini
[Unit]
Description=USB/IP (Go) daemon socket

[Socket]
ListenStream=0.0.0.0:3240
Accept=no
FileDescriptorName=usbipd-go

[Install]
WantedBy=sockets.target
```

Socket activation means systemd binds the TCP port. The daemon
receives the listener via `LISTEN_FDS` + `LISTEN_FDNAMES` and never
races with a previous daemon over port 3240 during upgrades. The
`FileDescriptorName=usbipd-go` directive lets the Go
`activation.ListenersWithNames` helper disambiguate if multiple
sockets are ever passed to the same unit.

### `usbipd-go.service`

```ini
[Unit]
Description=USB/IP (Go) daemon
Requires=usbipd-go.socket

[Service]
Type=simple
ExecStart=/usr/bin/usbipd-go
Restart=on-failure
CapabilityBoundingSet=CAP_SYS_ADMIN CAP_DAC_OVERRIDE

[Install]
WantedBy=multi-user.target
```

Copy, then customise:

- Add `--allow-cidr=10.0.0.0/8` or similar `ExecStart` flags for
  your network (see [`security.md`](security.md)).
- Add `--metrics-addr=127.0.0.1:9240` to expose Prometheus
  scraping on localhost.
- Add `--status-socket-group=usbip` and create the `usbip-go` group
  for the operators who need `usbipd-go drain`.
- Pin additional hardening directives:
  `NoNewPrivileges=yes`, `ProtectSystem=strict`,
  `ProtectHome=true`, `PrivateTmp=yes`, `RestrictSUIDSGID=yes`,
  `RestrictNamespaces=yes`, `SystemCallFilter=@system-service`.

Enable:

```
sudo systemctl daemon-reload
sudo systemctl enable --now usbipd-go.socket
```

Socket activation means the daemon starts on the first inbound
connection, not at boot. `systemctl status usbipd-go` reports the
daemon's state; `systemctl status usbipd-go.socket` reports the
listener.

## Daemon flags

Authoritative list in spec §7.7. Most operators only touch:

| Flag | Default | When to change |
|---|---|---|
| `--listen` | `0.0.0.0:3240` | Ignored when systemd socket-activates the daemon. |
| `--allow-cidr` | `[]` | Always set when exposing beyond localhost. |
| `--max-sessions` | `128` | Bump for high-fanout scenarios. |
| `--max-sessions-per-peer` | `8` | Lower for strict isolation. |
| `--accept-rate-limit` | `10/s` | Lower for probe-heavy networks. |
| `--status-socket` | `/run/usbip-go/status.sock` | Change to an alternate runtime dir or `""` to disable. |
| `--status-socket-group` | `usbip-go` | Match your operator group. |
| `--metrics-addr` | `""` | Set to `127.0.0.1:9240` (or similar) to enable scraping. |
| `--log-level` | `info` | `debug` or `trace` during incident response. |
| `--log-format` | `auto` | `json` for log-aggregation pipelines. |

Run `usbipd --help` for the full list.

## Status UDS

When `--status-socket` is non-empty, the daemon serves a Unix-domain
socket HTTP endpoint with the live status document.

```
sudo curl --unix-socket /run/usbip-go/status.sock http://unused/ | jq .
```

Output includes:

- `schema` — stability discriminator (`"v1"`; see
  [`json-schema.md`](json-schema.md)).
- `version`, `commit`, `uptime_sec`.
- `listening` — TCP `addr` and whether it was `activation`-received.
- `bound_devices` — every exported BusID with `vid` / `pid`.
- `kernel_modules` — per-module `loaded` / `missing` / `unknown`.
- `sessions` — every accepted session with `id`, `remote`, `busid`,
  `started_at`, byte counters.

The status socket is also the channel for the drain command:

```
sudo usbipd-go drain --status-socket /run/usbip-go/status.sock
```

Drain instructs the running daemon to refuse new accepts, wait for
in-flight sessions up to `--drain-timeout`, and exit cleanly.
`systemctl restart usbipd-go` then starts the new version against the
same socket-activated listener without a connect-refused window.

## Metrics scraping

Enable with `--metrics-addr`:

```
usbipd --metrics-addr 127.0.0.1:9240
```

The endpoint exposes three paths:

- `GET /metrics` — Prometheus text format, with the spec §11.5.5
  metric catalog.
- `GET /healthz` — 200 while the process is up and the accept loop
  is running. Liveness only.
- `GET /readyz` — 200 only when: process up, required kernel
  modules loaded, listener bound, status socket writable. Readiness
  gate for Kubernetes-style orchestrators.

Prometheus scrape configuration:

```yaml
- job_name: usbip-go
  static_configs:
    - targets: ['usbipd-host:9240']
  scrape_interval: 15s
  scrape_timeout: 10s
```

Recommended alerting signals:

- `usbip_kernel_modules_loaded{module="usbip_host"} == 0` — the
  export kernel module disappeared.
- `rate(usbip_exporter_sessions_accepted_total{outcome=~"rejected_.*"}[5m])`
  high — ambient abuse or misconfigured clients.
- `usbip_exporter_sessions_active > 0.8 * <your --max-sessions>` —
  approaching the session cap.

Every metric in the catalog is defined in
[`json-schema.md`](json-schema.md#metrics) and spec §11.5.5. Labels
are drawn from closed small sets; no unbounded cardinality.

## Drain-and-upgrade

For seamless upgrades:

```
sudo usbipd-go drain --status-socket /run/usbip-go/status.sock
sudo install -m 0755 /tmp/new-usbipd-go /usr/bin/usbipd-go
sudo systemctl start usbipd-go
```

Kernel-owned sessions survive the daemon restart because the kernel
holds the socket refs (spec §5.4 item 7). Socket activation keeps
port 3240 bound across the restart so new clients do not see
connect-refused.

The new daemon process does **not** reclaim accounting state for
pre-existing sessions. They appear in the kernel's view (via sysfs)
but not in the new daemon's `Sessions()` snapshot. Operators who
need accounting continuity should drain before upgrading.

## Troubleshooting entry points

- **Device won't attach** — start at
  [`troubleshooting.md`](troubleshooting.md) and follow the decision
  tree.
- **Capture wire traffic for a bug report** — recipe in
  [`wire-trace.md`](wire-trace.md).
- **Something else** — include the output of:

  ```
  usbipd version
  sudo usbipd --log-level=trace --status-socket=/run/usbip-go/status.sock
  sudo curl --unix-socket /run/usbip-go/status.sock http://unused/ | jq .
  curl -s http://127.0.0.1:9240/metrics | grep usbip_
  ```

  in any issue you file.
