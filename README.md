# usbip-go

[![CI](https://github.com/abilisoft/usbip-go/actions/workflows/ci.yml/badge.svg)](https://github.com/abilisoft/usbip-go/actions/workflows/ci.yml)
[![CodeQL](https://github.com/abilisoft/usbip-go/actions/workflows/codeql.yml/badge.svg)](https://github.com/abilisoft/usbip-go/actions/workflows/codeql.yml)
[![Trivy](https://github.com/abilisoft/usbip-go/actions/workflows/trivy.yml/badge.svg)](https://github.com/abilisoft/usbip-go/actions/workflows/trivy.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/abilisoft/usbip-go/badge)](https://scorecard.dev/viewer/?uri=github.com/abilisoft/usbip-go)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/12654/badge)](https://www.bestpractices.dev/projects/12654)
[![codecov](https://codecov.io/gh/abilisoft/usbip-go/branch/main/graph/badge.svg)](https://codecov.io/gh/abilisoft/usbip-go)
[![Go Reference](https://pkg.go.dev/badge/github.com/abilisoft/usbip-go.svg)](https://pkg.go.dev/github.com/abilisoft/usbip-go)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](./LICENSE)

## What it is

**Pure-Go USB/IP userspace for Linux.**

`usbip-go` gives you a single binary for USB/IP client, exporter,
daemon, and operator workflows — plus an embeddable Go library for
building USB/IP-aware tools.

- ✅ Wire-compatible with upstream `usbip` / `usbipd` peers.
- ✅ No cgo and no runtime dependency on `usbip-utils`.
- ✅ One flat CLI for importers, exporters, daemon ops, and completions.
- ✅ Go API under `pkg/usbip` and domain values under `pkg/domain`.
- ✅ Reconnect, graceful drain, status socket, health checks, and WAN TCP tuning.

> [!WARNING]
> USB/IP is plaintext and unauthenticated. Use `usbip-go` only on
> trusted networks or inside WireGuard, Tailscale, SSH, or equivalent
> tunnels. Do not expose TCP/3240 to the public internet.

## Comparison

Legend: ✅ built in · ⚠️ varies by distro/package · ❌ not provided

| Capability | `usbip-utils` | `usbip-go` |
| --- | --- | --- |
| USB/IP wire compatibility | ✅ | ✅ |
| Linux kernel USB/IP modules | ✅ | ✅ |
| Single client/exporter/daemon binary | ❌ | ✅ |
| Flat command set | ❌ | ✅ |
| Embeddable Go library | ❌ | ✅ |
| Typed Go domain model | ❌ | ✅ |
| Pure Go, no cgo | ❌ | ✅ |
| Non-Linux build stubs | ❌ | ✅ |
| JSON output with versioned schema | ❌ | ✅ |
| Shell completion installer | ⚠️ | ✅ |
| Active port inspection | ✅ | ✅ |
| Auto-reconnect | ❌ | ✅ |
| Event stream | ❌ | ✅ |
| Bind safety guardrails | ⚠️ | ✅ |
| Graceful drain + status UDS | ❌ | ✅ |
| Health/readiness endpoints | ❌ | ✅ |
| systemd socket activation | ⚠️ | ✅ |
| CIDR/rate/session limits | ❌ | ✅ |
| WAN TCP tuning | ❌ | ✅ |
| Release SBOM + cosign + SLSA provenance | ⚠️ | ✅ |
| OpenSpec behavior specs | ❌ | ✅ |
| TLS/auth on the USB/IP wire | ❌ | ❌ |

## Installation

### Release archive

Choose the latest non-retracted stable tag listed in
[`SECURITY.md`](SECURITY.md) and on the
[GitHub Releases page](https://github.com/abilisoft/usbip-go/releases). If no
such tag is present, there is no supported archive to install.

```sh
VERSION=X.Y.Z # replace with a supported release version
curl -LO "https://github.com/abilisoft/usbip-go/releases/download/v${VERSION}/usbip-go_${VERSION}_linux_amd64.tar.gz"
tar xzf "usbip-go_${VERSION}_linux_amd64.tar.gz"
sudo install -m 0755 usbip-go /usr/local/bin/usbip-go
```

Archives are published for Linux `amd64`, `arm64`, and `armv7`.
Releases also include `.deb` and `.rpm` packages, checksums, SBOMs,
Sigstore bundles, and SLSA provenance. See
[`docs/security-posture.md`](docs/security-posture.md) for release
integrity details.

### Go install

```sh
VERSION=vX.Y.Z # replace with a supported release tag
go install "github.com/abilisoft/usbip-go/cmd/usbip-go@${VERSION}"
```

Use an explicit supported tag rather than `@latest` so installation follows
the support policy. Building the current development branch requires Go 1.26.5
or newer.

### Kernel modules

Load modules for the role each machine plays:

| Machine | Needs | Why |
| --- | --- | --- |
| exporter/server | `usbip_core`, `usbip_host` | shares physical USB devices |
| importer/client | `usbip_core`, `vhci_hcd` | attaches remote devices locally |

The `.deb` and `.rpm` packages install persistent module-loading
configuration. For archive or `go install` setups, load modules before
running commands:

Copy/paste for an **exporter/server**:

```sh
sudo modprobe usbip_core usbip_host
```

Copy/paste for an **importer/client**:

```sh
sudo modprobe usbip_core vhci_hcd
```

If a host does both:

```sh
sudo modprobe usbip_core vhci_hcd usbip_host
```

The systemd service also runs `modprobe usbip_core usbip_host` before
starting the exporter daemon.

## Usage

USB/IP has two sides:

- **exporter/server** — owns the physical USB device and runs the daemon.
- **importer/client** — attaches that remote device to a local VHCI port.

### Export a local USB device

Manual foreground flow on the machine with the physical USB device:

```sh
sudo modprobe usbip_core usbip_host
usbip-go list
sudo usbip-go bind 1-1.2
sudo usbip-go serve --status-socket=
```

By default, `serve` listens on `0.0.0.0:3240`; use
`--listen HOST:PORT` to override it. The manual foreground example
disables the status socket because packaged/systemd installs create
`/run/usbip-go` for that socket. Keep `usbip-go serve` running while
clients are attached. When you are done:

```sh
sudo usbip-go unbind 1-1.2
```

### Import that device from another machine

```sh
sudo modprobe usbip_core vhci_hcd
usbip-go list 10.0.0.5
sudo usbip-go attach 10.0.0.5 1-1.2
usbip-go port
sudo usbip-go detach 0
```

### Run as a daemon with systemd

Use the `.deb` or `.rpm` package when you want systemd. Packages
install the binary, socket/service units, module-loading config, and
runtime-directory wiring.

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now usbip-go.socket
```

Socket activation starts `usbip-go serve` on the first inbound
connection. See [`docs/ops.md`](docs/ops.md) for package install
commands, status, readiness, health checks, logs, and graceful
`usbip-go drain` rollouts.

After enabling the socket, bind the devices you want to export:

```sh
sudo modprobe usbip_core usbip_host
sudo usbip-go bind 1-1.2
```

### JSON for scripts

```sh
usbip-go list 10.0.0.5 --output json
usbip-go port --output json
usbip-go watch --output json
```

The JSON contract is documented in [`docs/json-schema.md`](docs/json-schema.md).

## Go example

```go
package main

import (
    "context"
    "log/slog"

    "github.com/abilisoft/usbip-go/pkg/domain"
    "github.com/abilisoft/usbip-go/pkg/usbip"
)

func main() {
    imp, err := usbip.NewImporter(usbip.WithImporterLogger(slog.Default()))
    if err != nil {
        panic(err)
    }
    defer imp.Close()

    remote, err := domain.ParseRemote("10.0.0.5:3240")
    if err != nil {
        panic(err)
    }

    busid, err := domain.ParseBusID("1-1.2")
    if err != nil {
        panic(err)
    }

    port, err := imp.Attach(context.Background(), remote, busid, usbip.AttachOptions{
        AutoReconnect: true,
    })
    if err != nil {
        panic(err)
    }

    _ = port // later: imp.Detach(ctx, port.ID)
}
```

More examples:

- [`examples/client`](examples/client) — importer flow.
- [`examples/server`](examples/server) — exporter flow.
- [`examples/events`](examples/events) — event streams.
- [`examples/reconnect`](examples/reconnect) — reconnect behavior.

## Documentation

- [`docs/security.md`](docs/security.md) — threat model and safe deployment.
- [`docs/ops.md`](docs/ops.md) — daemon operations, systemd, drain, health/status.
- [`docs/protocol.md`](docs/protocol.md) — USB/IP wire layout.
- [`docs/architecture.md`](docs/architecture.md) — package boundaries and design.
- [`docs/troubleshooting.md`](docs/troubleshooting.md) — attach/export failure guide.
- [`openspec/specs/`](openspec/specs/) — current behavior requirements.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — development workflow and QA gates.

## License

Apache-2.0. See [`LICENSE`](LICENSE).
