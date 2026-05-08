# usbip-go

[![CI](https://github.com/abilisoft/usbip-go/actions/workflows/ci.yml/badge.svg)](https://github.com/abilisoft/usbip-go/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/badge/coverage-see%20testcoverage.yaml-blue)](./.testcoverage.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/abilisoft/usbip-go)](https://goreportcard.com/report/github.com/abilisoft/usbip-go)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](./LICENSE)

Pure-Go reimplementation of USB/IP userspace for Linux. Ships three
artefacts from a single code base:

- `pkg/usbip` — embeddable library for importers (client) and
  exporters (server).
- `usbip-go` — operator/client CLI (cobra + jsonlines).
- `usbipd-go` — production daemon with socket activation, Prometheus
  metrics, status UDS, and graceful drain.

No cgo, no dependencies on `usbip-utils`. Upstream wire compatibility
is pinned by conformance tests against real captures.

## Security posture

> **USB/IP is a plaintext, unauthenticated protocol.**
>
> Anyone who can TCP-connect to port 3240 can list exported devices
> and attach them. usbip-go does not wrap the wire in TLS — TLS is out
> of scope (see [`docs/security.md`](docs/security.md)). Deploy only
> over already-trusted networks: private LAN, Wireguard, Tailscale,
> or SSH tunnels. Never expose port 3240 on the public internet.
>
> The daemon ships defence-in-depth flags — `--allow-cidr`,
> `--max-sessions`, `--accept-rate-limit` — but these are enforcement
> seams, not authentication. Read
> [`docs/security.md`](docs/security.md) before deploying.

## Install

### Release binaries

Prebuilt binaries, `.deb`, and `.rpm` packages are published to
[GitHub Releases](https://github.com/abilisoft/usbip-go/releases).

```
VERSION=0.1.0  # replace with the tag you want; see the Releases page
curl -LO "https://github.com/abilisoft/usbip-go/releases/download/v${VERSION}/usbip-go_${VERSION}_linux_amd64.tar.gz"
tar xzf "usbip-go_${VERSION}_linux_amd64.tar.gz"
sudo install -m 0755 usbip-go usbipd-go /usr/bin/
```

GoReleaser archive names follow
`usbip-go_<version>_<os>_<arch>.tar.gz` (see `.goreleaser.yml`). Pick
the architecture that matches your host (`amd64`, `arm64`, or
`armv7`).

### Systemd

The release archive and packages both include systemd units. Drop
them in place and enable the socket unit:

```
sudo install -Dm 0644 contrib/systemd/usbipd-go.service /etc/systemd/system/usbipd-go.service
sudo install -Dm 0644 contrib/systemd/usbipd-go.socket  /etc/systemd/system/usbipd-go.socket
sudo systemctl daemon-reload
sudo systemctl enable --now usbipd-go.socket
```

Socket activation means the daemon starts on the first inbound
connection. See [`docs/ops.md`](docs/ops.md) for the full systemd
hardening recipe, metrics wiring, and drain procedure.

### `go install`

```
go install github.com/abilisoft/usbip-go/cmd/usbip-go@latest
go install github.com/abilisoft/usbip-go/cmd/usbipd-go@latest
```

Requires Go 1.26 or newer.

### Kernel modules

Every host running `usbipd-go` (exporter) or the `usbip-go` client needs
the relevant kernel modules:

```
sudo modprobe usbip_core vhci-hcd usbip-host
echo -e 'usbip_core\nvhci_hcd\nusbip_host' \
  | sudo tee /etc/modules-load.d/usbip-go.conf
```

## Quickstart

### 1. Embed the library

```go
package main

import (
    "context"
    "log/slog"

    "github.com/abilisoft/usbip-go/pkg/domain"
    "github.com/abilisoft/usbip-go/pkg/usbip"
)

func main() {
    imp, _ := usbip.NewImporter(usbip.WithImporterLogger(slog.Default()))
    defer imp.Close()

    remote, _ := domain.ParseRemote("10.0.0.5:3240")
    busid, _ := domain.ParseBusID("1-1.2")

    port, _ := imp.Attach(context.Background(), remote, busid, usbip.AttachOptions{
        AutoReconnect: true,
    })
    _ = port // detach later with imp.Detach(ctx, port.ID)
}
```

More patterns under [`examples/`](examples/) — client, server,
events, reconnect, metrics.

### 2. CLI attach

```
sudo usbip-go attach -r 10.0.0.5 -b 1-1.2
sudo usbip-go port
sudo usbip-go detach 0
```

### 3. Daemon via systemd

```
sudo systemctl enable --now usbipd-go.socket
sudo usbip-go bind 1-1.2           # export a local device
sudo systemctl status usbipd-go
```

Metrics, drain, and readiness endpoints are in
[`docs/ops.md`](docs/ops.md).

## Development

The dev toolchain is hermetic: the only host-side prerequisites are
**Docker** and **[Task](https://taskfile.dev)**. Go, linters,
formatters, and every release tool (goreleaser, syft, cosign, nfpm,
git-cliff) are pinned in `flake.nix` and delivered through a Nix
container — `task setup` seeds the store once, then `task test`,
`task lint`, and friends reuse it.

See [`CONTRIBUTING.md`](CONTRIBUTING.md#prerequisites) for the full
onboarding flow, the hermetic-cache layout under `./build/`, and
the microVM path that runs integration tests against a real Linux
kernel without requiring the USBIP modules on your host.

## Documentation

- [`docs/architecture.md`](docs/architecture.md) — layering and
  package responsibilities.
- [`docs/protocol.md`](docs/protocol.md) — USB/IP wire byte layouts.
- [`docs/security.md`](docs/security.md) — threat model, privilege,
  allow-CIDR, `setcap`.
- [`docs/ops.md`](docs/ops.md) — daemon install, systemd, status UDS,
  metrics, drain.
- [`docs/troubleshooting.md`](docs/troubleshooting.md) — decision
  tree for attach failures.
- [`docs/wire-trace.md`](docs/wire-trace.md) — pcap recipe for bug
  reports.
- [`docs/json-schema.md`](docs/json-schema.md) — v1 JSON schema
  contract.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — dev setup, TDD discipline,
  commit conventions.

## Status

v1 surface is under active development. APIs under `pkg/usbip` and
`pkg/domain` are guarded by `apidiff` baselines and require a
`BREAKING:` commit for any incompatible change — see
[`CONTRIBUTING.md`](CONTRIBUTING.md#api-surface-baselines).

## License

Apache-2.0. See [`LICENSE`](LICENSE).
