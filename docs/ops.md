# Operating the `usbip-go` daemon

This document covers installation, systemd integration, the status
UDS, and health/readiness endpoints for production deployments of
`usbip-go serve` (the daemon subcommand of the unified `usbip-go` binary;
see `openspec/specs/cli-interface/spec.md`).

## Installation

Use the OS package for production/systemd installs. The `.deb` and
`.rpm` release assets install the binary, systemd units, modules-load
config, and runtime-directory wiring used by the default status socket:

Choose the latest non-retracted stable tag listed in
[`../SECURITY.md`](../SECURITY.md) and on the
[GitHub Releases page](https://github.com/abilisoft/usbip-go/releases). If no
such tag is present, the commands below are templates rather than an
immediately installable version.

```text
sudo dpkg -i usbip-go_X.Y.Z_linux_amd64.deb
# or
sudo rpm -i usbip-go_X.Y.Z_linux_amd64.rpm
```

Archive and `go install` builds are best for manual foreground use or
development:

```text
curl -LO https://github.com/abilisoft/usbip-go/releases/download/vX.Y.Z/usbip-go_X.Y.Z_linux_amd64.tar.gz
tar xzf usbip-go_X.Y.Z_linux_amd64.tar.gz
sudo install -m 0755 usbip-go /usr/local/bin/

# or, after setting VERSION to a supported v-prefixed tag
go install "github.com/abilisoft/usbip-go/cmd/usbip-go@${VERSION}"
```

Kernel modules must be loadable on the target host. Packages install
persistent module-loading config; archive and source installs should
load the role-specific modules before starting commands:

```text
sudo modprobe usbip_core usbip_host      # exporter/server
sudo modprobe usbip_core vhci_hcd        # importer/client
```

## Systemd units

Packages install the service and socket units under
`/usr/lib/systemd/system`. The copies under
[`contrib/systemd/`](../contrib/systemd/) are references for packagers
and operators who intentionally maintain custom units.

### `usbip-go.socket`

```ini
[Unit]
Description=USB/IP (Go) daemon socket

[Socket]
ListenStream=0.0.0.0:3240
Accept=no
FileDescriptorName=usbip-go

[Install]
WantedBy=sockets.target
```

Socket activation means systemd binds the TCP port. The daemon
receives the listener via `LISTEN_FDS` + `LISTEN_FDNAMES` and never
races with a previous daemon over port 3240 during upgrades. The
`FileDescriptorName=usbip-go` directive lets the Go
`activation.ListenersWithNames` helper disambiguate if multiple
sockets are ever passed to the same unit.

### `usbip-go.service`

```ini
[Unit]
Description=USB/IP (Go) daemon
Requires=usbip-go.socket
After=systemd-modules-load.service

[Service]
Type=simple
ExecStartPre=-/sbin/modprobe usbip_core
ExecStartPre=-/sbin/modprobe usbip_host
ExecStart=/usr/bin/usbip-go serve
Restart=on-failure
RuntimeDirectory=usbip-go
RuntimeDirectoryMode=0755
CapabilityBoundingSet=CAP_SYS_ADMIN CAP_DAC_OVERRIDE CAP_CHOWN

[Install]
WantedBy=multi-user.target
```

Customise package-installed units with drop-ins:

- Add `--allow-cidr=10.0.0.0/8` or similar `ExecStart` flags for
  your network (see [`security.md`](security.md)).
- Add `--health-addr=127.0.0.1:9240` to expose `/healthz` and
  `/readyz` on localhost for orchestrator probes.
- Add `--status-socket-group=usbip-go` (the daemon's default) or any
  group of your choice; create that group and add the operators who
  need `usbip-go drain` to it. The reference unit retains `CAP_CHOWN` so a
  root-run daemon can apply that group after binding the socket.
- Pin additional hardening directives:
  `NoNewPrivileges=yes`, `ProtectSystem=strict`,
  `ProtectHome=true`, `PrivateTmp=yes`, `RestrictSUIDSGID=yes`,
  `RestrictNamespaces=yes`, `SystemCallFilter=@system-service`.

If you intentionally copy the reference unit by hand, keep
`RuntimeDirectory=usbip-go`: it creates `/run/usbip-go` before
`usbip-go serve` binds the default status socket.

Enable:

```text
sudo systemctl daemon-reload
sudo systemctl enable --now usbip-go.socket
```

Socket activation means the daemon starts on the first inbound
connection, not at boot. `systemctl status usbip-go` reports the
daemon's state; `systemctl status usbip-go.socket` reports the
listener.

## Daemon flags

Authoritative behavior is captured in
`openspec/specs/cli-interface/spec.md` and
`openspec/specs/operations-observability/spec.md`. Full daemon flag set:

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

For manual foreground runs from an archive or `go install`, either
disable the status UDS:

```text
sudo usbip-go serve --status-socket=
```

or choose a status-socket path whose parent directory already exists.
The packaged systemd unit handles `/run/usbip-go` with
`RuntimeDirectory=usbip-go`.

## Exit codes

The `usbip-go` binary uses a stable numeric exit-code catalog
documented in `openspec/specs/cli-interface/spec.md`. Operators /
supervisors can grep on these values:

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

```text
sudo curl --unix-socket /run/usbip-go/status.sock http://unused/ | jq .
```

Output includes:

- `schema` — stability discriminator (`"v1"`; see
  [`json-schema.md`](json-schema.md)).
- `version`, `commit`, `uptime_sec`.
- `listening` — TCP `addr` and whether it was `activation`-received.
- `bound_devices` — BusIDs bound to `usbip_host` and currently available to a
  new importer, with `vid` / `pid`. A device in `SDEV_ST_USED` is excluded;
  its active ownership appears under `sessions` instead.
- `bound_devices_error` — optional diagnostic text when listing
  bound devices failed and `bound_devices` would otherwise be empty.
- `kernel_modules` — per-module `loaded` / `missing` / `unknown`.
- `sessions` — every accepted session with `id`, `remote`, `busid`,
  `started_at`, and reserved `bytes_in` / `bytes_out` counters. The
  counters are currently `0` because kernel-owned URB forwarding is not
  metered in user space.

The status socket is also the channel for the drain command:

```text
sudo usbip-go drain --status-socket /run/usbip-go/status.sock
```

Drain instructs the running daemon to refuse new accepts, wait for
in-flight sessions to end, and exit cleanly. `systemctl restart usbip-go`
then starts the new version against the same socket-activated
listener without a connect-refused window. See
`openspec/specs/operations-observability/spec.md` for the
HTTP-over-UDS drain behavior.

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

```text
usbip-go serve --health-addr 127.0.0.1:9240
```

The listener exposes two paths, served by `net/http` from the standard
library (no third-party dependency):

- `GET /healthz` — unconditional 200 OK while the daemon's HTTP
  server is reachable. Pure liveness — does NOT inspect the accept
  loop, kernel modules, or status socket; the readiness signals
  belong to `/readyz`.
- `GET /readyz` — 200 only when: required kernel modules loaded,
  listener bound, accept loop armed, status socket writable.
  Readiness gate for Kubernetes-style orchestrators.

Per `openspec/specs/operations-observability/spec.md`, this daemon
does NOT export Prometheus metrics. Operator observability is
structured slog (journald) + sysfs + `systemctl status`. Every
important operation emits a
slog record with an `outcome` field carrying the closed-set
classification, so journald queries cover the same dashboards.

Recommended journald signal queries:

```text
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
that derives them from journald or sysfs. The library deliberately does
not ship that adapter.

## Drain-and-upgrade

For seamless upgrades:

```text
sudo usbip-go drain --status-socket /run/usbip-go/status.sock
sudo install -m 0755 /tmp/new-usbip-go /usr/bin/usbip-go
sudo systemctl start usbip-go
```

Active USB/IP sessions do not migrate to the replacement daemon.
Graceful drain deliberately disconnects them, and an abrupt daemon exit
causes remote VHCI ports to leave the used state. An abrupt exit can leave the
exporter-side kernel session marked used; reconcile each affected device before
starting replacement traffic:

```text
printf '%s' -1 | sudo tee /sys/bus/usb/devices/BUSID/usbip_sockfd
```

Clients must attach again after the replacement daemon is ready. Socket
activation keeps port 3240 bound across the restart so new connection attempts
avoid a listener gap; it does not preserve established USB/IP sessions.

## Troubleshooting entry points

- **Device won't attach** — start at
  [`troubleshooting.md`](troubleshooting.md) and follow the decision
  tree.
- **Capture wire traffic for a bug report** — recipe in
  [`wire-trace.md`](wire-trace.md).
- **Something else** — include the output of:

  ```text
  usbip-go version
  sudo usbip-go serve --log-level=trace --status-socket=/run/usbip-go/status.sock
  sudo curl --unix-socket /run/usbip-go/status.sock http://unused/ | jq .
  curl -s http://127.0.0.1:9240/readyz
  journalctl -u usbip-go --output=json --since '-15min'
  ```

  in any issue you file.
