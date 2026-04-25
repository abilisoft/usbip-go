# usbip-go

[![CI](https://github.com/abilisoft/usbip-go/actions/workflows/ci.yml/badge.svg)](https://github.com/abilisoft/usbip-go/actions/workflows/ci.yml)
[![CodeQL](https://github.com/abilisoft/usbip-go/actions/workflows/codeql.yml/badge.svg)](https://github.com/abilisoft/usbip-go/actions/workflows/codeql.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/abilisoft/usbip-go/badge)](https://scorecard.dev/viewer/?uri=github.com/abilisoft/usbip-go)
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

## How this compares to upstream `usbip-utils`

The upstream reference is the C client/daemon shipped under
`linux/tools/usb/usbip` (built and packaged as `usbip` /
`usbipd` / `libusbip`). usbip-go re-uses the same wire format and
the same kernel sysfs interface, so it interoperates with upstream
peers in either direction. What differs is the userspace surface
around the protocol:

| Capability                                                     | upstream `usbip-utils` | usbip-go                                          |
| -------------------------------------------------------------- | ---------------------- | ------------------------------------------------- |
| Wire-compatible with `usbip-utils` peers                       | :white_check_mark:     | :white_check_mark:                                |
| Uses kernel `vhci_hcd` / `usbip_host` / `usbip_vudc`           | :white_check_mark:     | :white_check_mark:                                |
| Pure Go, no cgo                                                | :x:                    | :white_check_mark:                                |
| Cross-compile to all Linux arches in one command               | :x:                    | :white_check_mark: (`GOOS`/`GOARCH`)              |
| Embeddable as a library (`pkg/usbip`)                          | :x:                    | :white_check_mark:                                |
| Auto-reconnect on detach                                       | :x:                    | :white_check_mark: (exponential backoff + jitter) |
| Concurrent-Attach deduplication (per `(remote, busid)`)        | :x:                    | :white_check_mark:                                |
| Per-attach `MaxAttempts` / `OnReconnect` callback              | :x:                    | :white_check_mark:                                |
| Graceful drain + bounded `ShutdownTimeout`                     | :x:                    | :white_check_mark:                                |
| Structured logging (`slog` JSON / text)                        | :x:                    | :white_check_mark:                                |
| Prometheus metrics on importer + exporter                      | :x:                    | :white_check_mark:                                |
| Event subscription API (port / session / reconnect)            | :x:                    | :white_check_mark:                                |
| systemd socket activation                                      | :x:                    | :white_check_mark: (`usbipd-go.socket`)           |
| Status UDS for live introspection                              | :x:                    | :white_check_mark:                                |
| JSON output mode with versioned schema                         | :x:                    | :white_check_mark:                                |
| Allow-list CIDR / rate-limit / session caps                    | :x:                    | :white_check_mark:                                |
| TCP_NODELAY on dialed connections                              | :white_check_mark:     | :white_check_mark:                                |
| Configurable connect timeout                                   | :x:                    | :hourglass_flowing_sand: ([plan][1])              |
| Tunable TCP keepalive (idle / interval / probes)               | :x:                    | :hourglass_flowing_sand: ([plan][1])              |
| Tunable `SO_SNDBUF` / `SO_RCVBUF` for WAN bandwidth-delay      | :x:                    | :hourglass_flowing_sand: ([plan][1])              |
| Static read / write deadlines per Importer/Exporter            | :x:                    | :hourglass_flowing_sand: ([plan][1])              |
| Tolerance for high-latency / lossy links (50–800 ms RTT)       | :x:                    | :hourglass_flowing_sand: ([plan][1])              |
| WAN / satellite backoff constructors                           | :x:                    | :hourglass_flowing_sand: ([plan][1])              |
| Reproducible builds (`-trimpath`, no cgo)                      | :x:                    | :white_check_mark:                                |
| Static analysis (CodeQL, `govulncheck`, `golangci-lint`)       | :x:                    | :white_check_mark:                                |
| Conformance tests against real wire captures                   | :x:                    | :white_check_mark:                                |
| Fuzz targets on the wire codec                                 | :x:                    | :white_check_mark:                                |
| Mutation testing on protocol-critical packages                 | :x:                    | :white_check_mark:                                |
| Coverage gate (90%+ for pure-logic packages)                   | :x:                    | :white_check_mark:                                |
| SBOM + cosign keyless signed releases                          | :x:                    | :white_check_mark:                                |
| OpenSSF Scorecard / Best Practices                             | :x:                    | :white_check_mark:                                |
| TLS or authentication on the wire                              | :x:                    | :x: (out of scope — tunnel via WG/SSH/Tailscale)  |

[1]: ./docs/high-latency-plan.md

`:hourglass_flowing_sand:` marks work scoped for v1.x and tracked in
[`docs/high-latency-plan.md`](docs/high-latency-plan.md). The
underlying invariant — wire-compatible with upstream — does not
change.

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
VERSION=1.0.0  # replace with the tag you want; see the Releases page
curl -LO "https://github.com/abilisoft/usbip-go/releases/download/v${VERSION}/usbip-go_${VERSION}_linux_amd64.tar.gz"
tar xzf "usbip-go_${VERSION}_linux_amd64.tar.gz"
sudo install -m 0755 usbip-go usbipd-go /usr/bin/
```

GoReleaser archive names follow
`usbip-go_<version>_<os>_<arch>.tar.gz` (see `.goreleaser.yml`). Pick
the architecture that matches your host (`amd64`, `arm64`, or
`armv7`).

### Verifying a release

Every release ships a SLSA Build Provenance bundle
(`multiple.intoto.jsonl`) and a cosign keyless signature on
`checksums.txt`. Verify both before installing:

```
VERSION=1.0.0
ARCHIVE=usbip-go_${VERSION}_linux_amd64.tar.gz
BASE=https://github.com/abilisoft/usbip-go/releases/download/v${VERSION}

curl -LO "${BASE}/${ARCHIVE}"
curl -LO "${BASE}/checksums.txt"
curl -LO "${BASE}/checksums.txt.sig"
curl -LO "${BASE}/checksums.txt.pem"
curl -LO "${BASE}/multiple.intoto.jsonl"

# 1. Provenance: prove the artifact came out of the abilisoft/usbip-go
#    GitHub Actions release workflow at the matching tag.
slsa-verifier verify-artifact "${ARCHIVE}" \
  --provenance-path multiple.intoto.jsonl \
  --source-uri github.com/abilisoft/usbip-go \
  --source-tag "v${VERSION}"

# 2. Checksum signature: prove checksums.txt was signed by a Sigstore
#    keyless cert whose OIDC subject is the .github/workflows/release.yml
#    workflow at the same v*.*.* tag — matches the exact workflow path
#    so a different workflow in this repo cannot satisfy the check.
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp '^https://github\.com/abilisoft/usbip-go/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt

# 3. Per-binary integrity: confirm the archive's sha256 is in checksums.txt.
sha256sum -c --ignore-missing checksums.txt
```

Install [`slsa-verifier`](https://github.com/slsa-framework/slsa-verifier#installation)
and [`cosign`](https://docs.sigstore.dev/cosign/system_config/installation/)
once; both are statically linked single binaries.

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
sudo usbip-go attach 10.0.0.5 1-1.2
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
