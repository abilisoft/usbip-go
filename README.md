# usbip-go

[![CI](https://github.com/abilisoft/usbip-go/actions/workflows/ci.yml/badge.svg)](https://github.com/abilisoft/usbip-go/actions/workflows/ci.yml)
[![CodeQL](https://github.com/abilisoft/usbip-go/actions/workflows/codeql.yml/badge.svg)](https://github.com/abilisoft/usbip-go/actions/workflows/codeql.yml)
[![Trivy](https://github.com/abilisoft/usbip-go/actions/workflows/trivy.yml/badge.svg)](https://github.com/abilisoft/usbip-go/actions/workflows/trivy.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/abilisoft/usbip-go/badge)](https://scorecard.dev/viewer/?uri=github.com/abilisoft/usbip-go)
[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/12654/badge)](https://www.bestpractices.dev/projects/12654)
[![codecov](https://codecov.io/gh/abilisoft/usbip-go/branch/main/graph/badge.svg)](https://codecov.io/gh/abilisoft/usbip-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/abilisoft/usbip-go)](https://goreportcard.com/report/github.com/abilisoft/usbip-go)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](./LICENSE)

Pure-Go reimplementation of USB/IP userspace for Linux. Ships two
artefacts from a single code base:

- `pkg/usbip` — embeddable library for importers and exporters.
- `usbip-go` — single binary with flat subcommands for all roles:
  importer CLI, exporter CLI, and daemon (`usbip-go serve`).

No cgo, no dependencies on `usbip-utils`. Upstream wire compatibility
is pinned by conformance tests against real captures.

## How this compares to upstream `usbip-utils`

The upstream reference is the C client/daemon shipped under
`linux/tools/usb/usbip` (built and packaged as `usbip` /
`usbipd` / `libusbip`). usbip-go re-uses the same wire format and
the same kernel sysfs interface, so it interoperates with upstream
peers in either direction. What differs is the userspace surface
around the protocol:

| Capability                                                                | upstream `usbip-utils` | `usbip-go` |
| ------------------------------------------------------------------------- | ---------------------- | ---------- |
| Wire-compatible with `usbip-utils` peers                                  | ✅                     | ✅         |
| Uses kernel `vhci_hcd` / `usbip_host` / `usbip_vudc`                      | ✅                     | ✅         |
| Pure Go, no cgo                                                           | ❌                     | ✅         |
| Cross-compile supported Linux arches in one command                       | ❌                     | ✅         |
| Embeddable as a library (`pkg/usbip`)                                     | ❌                     | ✅         |
| Auto-reconnect on detach (exponential backoff + jitter)                   | ❌                     | ✅         |
| Concurrent-attach deduplication (per `(remote, busid)`)                   | ❌                     | ✅         |
| Per-attach `MaxAttempts` / `OnReconnect` callback                         | ❌                     | ✅         |
| Graceful drain + bounded `ShutdownTimeout`                                | ❌                     | ✅         |
| Structured logging (`slog` JSON / text)                                   | ❌                     | ✅         |
| Event subscription API (port / session / reconnect)                       | ❌                     | ✅         |
| systemd socket activation (`usbip-go.socket`)                             | ❌                     | ✅         |
| Status UDS for live introspection                                         | ❌                     | ✅         |
| JSON output mode with versioned schema                                    | ❌                     | ✅         |
| Allow-list CIDR / rate-limit / session caps                               | ❌                     | ✅         |
| `TCP_NODELAY` on dialed connections                                       | ✅                     | ✅         |
| Configurable connect timeout                                              | ❌                     | ✅         |
| Tunable TCP keepalive (idle / interval / probes)                          | ❌                     | ✅         |
| Tunable `SO_SNDBUF` / `SO_RCVBUF` for WAN bandwidth-delay                 | ❌                     | ✅         |
| Static read / write deadlines per `Importer` / `Exporter`                 | ❌                     | ✅         |
| Tolerance for high-latency / lossy links (50–800 ms RTT)                  | ❌                     | ✅         |
| Reproducible builds (`-trimpath`, no cgo)                                 | ❌                     | ✅         |
| Static analysis + vuln scanning (CodeQL, `golangci-lint`, `govulncheck`, Trivy) | ❌               | ✅         |
| Conformance tests against real wire captures                              | ❌                     | ✅         |
| Fuzz targets on the wire codec                                            | ❌                     | ✅         |
| Mutation testing on protocol-critical packages                            | ❌                     | ✅         |
| Coverage gate (90%+ for pure-logic packages)                              | ❌                     | ✅         |
| SBOM + `cosign` keyless signed releases                                   | ❌                     | ✅         |
| SLSA Build Provenance on every release                                    | ❌                     | ✅         |
| OpenSSF Scorecard / Best Practices                                        | ❌                     | ✅         |
| Shell completion install (`bash`/`zsh`/`fish`/`pwsh`)                          | ❌                | ✅         |
| `watch` subcommand for live event observation                                  | ❌                | ✅         |
| `port` subcommand for active VHCI port introspection                           | ❌                | ✅         |
| HTTP `/healthz` + `/readyz` endpoints                                          | ❌                | ✅         |
| Operator-stable versioned exit codes                                           | ❌                | ✅         |
| Drain admin API (`POST /drain` over the status UDS)                            | ❌                | ✅         |
| UUIDv7 session IDs for traceability                                            | ❌                | ✅         |
| `iter.Seq` (Go 1.23+) range-over-func event streams                            | ❌                | ✅         |
| Dual BusID validation (lenient on the wire, strict in the CLI)                 | ❌                | ✅         |
| `-race` enforced on every CI run                                               | ❌                | ✅         |
| `gosec` static-analysis rules in CI (via `golangci-lint`)                      | ❌                | ✅         |
| TLS or authentication on the wire (out of scope — tunnel via WG/SSH/Tailscale) | ❌                | ❌         |

The underlying invariant — wire-compatible with upstream — does not
change as new features land.

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
sudo install -m 0755 usbip-go /usr/bin/
```

GoReleaser archive names follow
`usbip-go_<version>_<os>_<arch>.tar.gz` (see `.goreleaser.yml`). Pick
the architecture that matches your host (`amd64`, `arm64`, or
`armv7`).

### Verifying a release

Every release ships a SLSA Build Provenance bundle
(`multiple.intoto.jsonl`) and a cosign keyless signature on the
checksums file (Sigstore bundle format). Verify both before
installing — `name_template` in `.goreleaser.yml` produces the
checksum filename as `usbip-go_<version>_checksums.txt`, so the
matching cosign bundle is `usbip-go_<version>_checksums.txt.sigstore.json`:

```
VERSION=1.0.0
ARCHIVE=usbip-go_${VERSION}_linux_amd64.tar.gz
CHECKSUMS=usbip-go_${VERSION}_checksums.txt
BUNDLE=${CHECKSUMS}.sigstore.json
BASE=https://github.com/abilisoft/usbip-go/releases/download/v${VERSION}

curl -LO "${BASE}/${ARCHIVE}"
curl -LO "${BASE}/${CHECKSUMS}"
curl -LO "${BASE}/${BUNDLE}"
curl -LO "${BASE}/multiple.intoto.jsonl"

# 1. Provenance: prove the artifact came out of the abilisoft/usbip-go
#    GitHub Actions release workflow at the matching tag.
slsa-verifier verify-artifact "${ARCHIVE}" \
  --provenance-path multiple.intoto.jsonl \
  --source-uri github.com/abilisoft/usbip-go \
  --source-tag "v${VERSION}"

# 2. Checksum signature: prove the checksums file was signed by a
#    Sigstore keyless cert whose OIDC subject is the
#    .github/workflows/release.yml workflow at the same v*.*.* tag —
#    matches the exact workflow path so a different workflow in this
#    repo cannot satisfy the check. The Sigstore bundle (--bundle)
#    carries the leaf certificate, the signature, and the rekor
#    inclusion proof in a single file.
cosign verify-blob \
  --bundle "${BUNDLE}" \
  --certificate-identity-regexp '^https://github\.com/abilisoft/usbip-go/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "${CHECKSUMS}"

# 3. Per-binary integrity: confirm the archive's sha256 is in the
#    checksums file.
sha256sum -c --ignore-missing "${CHECKSUMS}"
```

Install [`slsa-verifier`](https://github.com/slsa-framework/slsa-verifier#installation)
and [`cosign`](https://docs.sigstore.dev/cosign/system_config/installation/)
once; both are statically linked single binaries.

### Systemd

The release archive and packages both include systemd units and a
modules-load snippet. Drop them in place and enable the socket unit:

```
sudo install -Dm 0644 contrib/systemd/usbip-go.service /etc/systemd/system/usbip-go.service
sudo install -Dm 0644 contrib/systemd/usbip-go.socket  /etc/systemd/system/usbip-go.socket
sudo install -Dm 0644 contrib/modules-load.d/usbip-go.conf /etc/modules-load.d/usbip-go.conf
sudo systemctl daemon-reload
sudo systemctl enable --now usbip-go.socket
```

Socket activation means the daemon starts on the first inbound
connection. See [`docs/ops.md`](docs/ops.md) for the full systemd
hardening recipe, status/health endpoints, and drain procedure.

### `go install`

```
go install github.com/abilisoft/usbip-go/cmd/usbip-go@latest
```

Requires Go 1.26 or newer.

### Kernel modules

Every host running `usbip-go serve` (exporter) or the `usbip-go`
client needs the relevant kernel modules:

```
sudo modprobe usbip_core vhci_hcd usbip_host
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
events, and reconnect.

### 2. CLI attach

```
sudo usbip-go attach 10.0.0.5 1-1.2
sudo usbip-go port
sudo usbip-go detach 0
```

### 3. Daemon via systemd

```
sudo systemctl enable --now usbip-go.socket
sudo usbip-go bind 1-1.2           # export a local device
sudo systemctl status usbip-go
```

Status, drain, health, and readiness endpoints are in
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
  health/readiness, structured logs, and drain.
- [`docs/troubleshooting.md`](docs/troubleshooting.md) — decision
  tree for attach failures.
- [`docs/wire-trace.md`](docs/wire-trace.md) — pcap recipe for bug
  reports.
- [`docs/json-schema.md`](docs/json-schema.md) — v1 JSON schema
  contract.
- [`openspec/specs/`](openspec/specs/) — source-of-truth
  requirements for current behavior; update these instead of ADRs.
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — dev setup, TDD discipline,
  commit conventions.

## Status

v1 surface is under active development. APIs under `pkg/usbip` and
`pkg/domain` are guarded by `apidiff` baselines and require a
`BREAKING:` commit for any incompatible change — see
[`CONTRIBUTING.md`](CONTRIBUTING.md#api-surface-baselines).

## License

Apache-2.0. See [`LICENSE`](LICENSE).
