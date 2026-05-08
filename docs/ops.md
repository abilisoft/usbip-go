# Operating the `usbip-go` daemon

This document covers installation, systemd integration, the status
UDS, and health/readiness endpoints for production deployments of
`usbip-go serve` (the daemon subcommand of the unified `usbip-go` binary;
see ADR-0011).

## Installation

Three supported install paths:

1. **Pre-built release archive** from GitHub Releases:

   ```
   curl -LO https://github.com/abilisoft/usbip-go/releases/download/vX.Y.Z/usbip-go_vX.Y.Z_linux_amd64.tar.gz
   tar xzf usbip-go_vX.Y.Z_linux_amd64.tar.gz
   sudo install -m 0755 usbip-go /usr/local/bin/
   sudo install -Dm 0644 contrib/systemd/usbip-go.service /etc/systemd/system/usbip-go.service
   sudo install -Dm 0644 contrib/systemd/usbip-go.socket  /etc/systemd/system/usbip-go.socket
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

### `usbip-go.socket`

```ini
[Unit]
Description=USB/IP (Go) daemon socket

[Socket]
ListenStream=0.0.0.0:3240
Accept=no
FileDescriptorName=usbip

[Install]
WantedBy=sockets.target
```

Socket activation means systemd binds the TCP port. The daemon
receives the listener via `LISTEN_FDS` + `LISTEN_FDNAMES` and never
races with a previous daemon over port 3240 during upgrades. The
`FileDescriptorName=usbip` directive lets the Go
`activation.ListenersWithNames` helper disambiguate if multiple
sockets are ever passed to the same unit.

### `usbip-go.service`

```ini
[Unit]
Description=USB/IP (Go) daemon
Requires=usbip-go.socket

[Service]
Type=simple
ExecStart=/usr/bin/usbip-go serve
Restart=on-failure
CapabilityBoundingSet=CAP_SYS_ADMIN CAP_DAC_OVERRIDE

[Install]
WantedBy=multi-user.target
```

Copy, then customise:

- Add `--allow-cidr=10.0.0.0/8` or similar `ExecStart` flags for
  your network (see [`security.md`](security.md)).
- Add `--health-addr=127.0.0.1:9240` to expose `/healthz` and
  `/readyz` on localhost for orchestrator probes.
- Add `--status-socket-group=usbip-go` (the daemon's default) or any
  group of your choice; create that group and add the operators who
  need `usbip-go drain` to it.
- Pin additional hardening directives:
  `NoNewPrivileges=yes`, `ProtectSystem=strict`,
  `ProtectHome=true`, `PrivateTmp=yes`, `RestrictSUIDSGID=yes`,
  `RestrictNamespaces=yes`, `SystemCallFilter=@system-service`.

Enable:

```
sudo systemctl daemon-reload
sudo systemctl enable --now usbip-go.socket
```

Socket activation means the daemon starts on the first inbound
connection, not at boot. `systemctl status usbip-go` reports the
daemon's state; `systemctl status usbip-go.socket` reports the
listener.

## Daemon flags

Authoritative list in v1 contract §7.7. Full flag set:

| Flag | Default | When to change |
|---|---|---|
| `--listen` | `0.0.0.0:3240` | Ignored when systemd socket-activates the daemon. |
| `--allow-cidr` | `[]` | Always set when exposing beyond localhost. |
| `--max-sessions` | `128` | Bump for high-fanout scenarios. |
| `--max-sessions-per-peer` | `8` | Lower for strict isolation. |
| `--accept-rate-limit` | `10/s` | Lower for probe-heavy networks. |
| `--max-handshake-bytes` | `64 KiB` | Hard cap per OP request/response; raise only for non-standard peers. |
| `--handshake-timeout` | `10s` | Slowloris defence; drop a peer that stalls mid-handshake. |
| `--shutdown-timeout` | `30s` | Graceful drain budget before force-close. |
| `--status-socket` | `/run/usbip-go/status.sock` | Change to an alternate runtime dir or `""` to disable. |
| `--status-socket-group` | `usbip-go` | Match your operator group. |
| `--health-addr` | `""` | Set to `127.0.0.1:9240` (or similar) to expose `/healthz` and `/readyz` for orchestrator probes. |
| `--log-level` | `info` | `debug` or `trace` during incident response. |
| `--log-format` | `auto` | `json` for log-aggregation pipelines. |
| `--verbose` / `-v` | `0` | Count flag: `-v` raises log level to `debug`, `-vv` to `trace`. Wins over `--log-level` when set. |

Run `usbip-go serve --help` for the up-to-date set.

## Exit codes

The `usbip-go` binary uses a stable numeric exit-code catalog (v1
contract §7.4). Operators / supervisors can grep on these values:

| Code | Symbol | Meaning |
|---|---|---|
| `0` | `ExitOK` | Operation succeeded |
| `1` | `ExitGeneric` | Unclassified error — see stderr / journald |
| `2` | `ExitUsage` | Bad flag / argument; the cobra-level usage message goes to stderr |
| `3` | `ExitPermission` | Operation needs CAP_SYS_ADMIN (importer-side commands like `usbip-go attach`) |
| `4` | `ExitKernelModule` | Required kernel module not loaded (`vhci_hcd`, `usbip_host`, ...) |
| `5` | `ExitDeviceNotFound` | Device with the supplied BusID not present |
| `6` | `ExitDeviceBusy` | Device already bound or port already in use |
| `7` | `ExitProtocolMismatch` | Peer speaks a different USB/IP version |
| `8` | `ExitNetwork` | Dial / read / write error against the remote |
| `9` | `ExitTimeout` | Operation deadline exceeded; also returned by `usbip-go drain --drain-timeout` overruns |
| `10` | `ExitNoFreePort` | Importer has no free vhci port |
| `11` | `ExitProtocolError` | Peer reported a protocol-level error |
| `12` | `ExitAlreadyRunning` | `usbip-go serve` could not start because another daemon owns the status UDS |
| `130` | `ExitInterrupted` | SIGINT / context.Canceled (Unix convention: 128 + signal 2) |

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
sudo usbip-go drain --status-socket /run/usbip-go/status.sock
```

Drain instructs the running daemon to refuse new accepts, wait for
in-flight sessions to end, and exit cleanly. `systemctl restart usbip-go`
then starts the new version against the same socket-activated
listener without a connect-refused window. See ADR-0012 for the
mechanism (HTTP-over-UDS) and the rejected alternatives (signals,
sd_notify, gRPC, custom binary protocol).

### Two timeouts, server-authoritative

Two timeouts coexist:

| Flag | Side | Default | What it bounds |
|---|---|---|---|
| `--shutdown-timeout` | `usbip-go serve` (server) | `30s` | Actual in-flight session drain. AUTHORITATIVE — daemon refuses to wait beyond this. |
| `--drain-timeout` | `usbip-go drain` (client) | `60s` | UI-only bound on how long the client polls `GET /` waiting for `sessions == []`. |

Recommended: keep `--drain-timeout > --shutdown-timeout` so the
operator-visible result reflects the daemon's actual behavior. When
the daemon hits its cap first, the client's next poll sees
`ECONNREFUSED` and treats it as drain success. When the client times
out first (`--drain-timeout < --shutdown-timeout`), the client
reports failure but the daemon keeps draining and exits when its own
timeout fires — operator sees a misleading "drain timed out" but
the daemon completes correctly.

Concurrent / repeated `usbip-go drain` calls are idempotent: only the
first POST spawns the drain goroutine; subsequent POSTs return 200
immediately without re-triggering shutdown.

## Health endpoints

Enable with `--health-addr`:

```
usbip-go serve --health-addr 127.0.0.1:9240
```

The endpoint exposes two paths, served by `net/http` from the standard
library (no third-party dependency):

- `GET /healthz` — unconditional 200 OK while the daemon's HTTP
  server is reachable. Pure liveness — does NOT inspect the accept
  loop, kernel modules, or status socket; the readiness signals
  belong to `/readyz`.
- `GET /readyz` — 200 only when: required kernel modules loaded,
  listener bound, accept loop armed, status socket writable.
  Readiness gate for Kubernetes-style orchestrators.

Per ADR-0010, this daemon does NOT export Prometheus metrics. Operator
observability is structured slog (journald) + sysfs + `systemctl
status`. Every operation that previously emitted a metric now emits a
slog record with an `outcome` field carrying the closed-set
classification, so journald queries cover the same dashboards.

Recommended journald signal queries:

```
# Bind / unbind failures by outcome
journalctl -u usbip-go --output=json \
  | jq 'select(.MESSAGE | startswith("exporter bind failed"))
        | {time: .__REALTIME_TIMESTAMP, busid, outcome, err}'

# Reconnect storms (watcher backoff churn)
journalctl -u usbip-go --output=json \
  | jq 'select(.outcome == "backoff")'

# Sessions rejected by ACL or rate limit
journalctl -u usbip-go --output=json \
  | jq 'select(.outcome == "rejected_acl" or .outcome == "rejected_rate")'
```

Operators who genuinely need Prometheus metrics should run a sidecar
that parses `journalctl --output=json` and republishes counters in the
exposition format. The library deliberately does not ship that
adapter.

## Drain-and-upgrade

For seamless upgrades:

```
sudo usbip-go drain --status-socket /run/usbip-go/status.sock
sudo install -m 0755 /tmp/new-usbip /usr/bin/usbip
sudo systemctl start usbip-go
```

Kernel-owned sessions survive the daemon restart because the kernel
holds the socket refs (v1 contract §5.4 item 7). Socket activation keeps
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
  usbip-go version
  sudo usbip-go serve --log-level=trace --status-socket=/run/usbip-go/status.sock
  sudo curl --unix-socket /run/usbip-go/status.sock http://unused/ | jq .
  curl -s http://127.0.0.1:9240/readyz
  journalctl -u usbip-go --output=json --since '-15min'
  ```

  in any issue you file.
