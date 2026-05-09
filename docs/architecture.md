# Architecture

This document summarises the usbip-go layering for operators and
contributors.

## Layers

```
+-------------------------+  cmd/usbip-go, examples/*
|   Command entrypoints   |
+-----------+-------------+
            |
            v
+-------------------------+  pkg/usbip  (stable public facade)
|    Public Go surface    |  pkg/domain (value objects, events, errors)
+-----------+-------------+
            |
            v
+-------------------------+  internal/app  (use-case services:
|   Application services  |   Importer, Exporter, watchers)
+-----------+-------------+   Declares the adapter interfaces.
            |
            v
+-------------------------+  internal/adapter/kernel     (sysfs + netlink, Linux)
|    Adapter layer        |  internal/adapter/wire       (USB/IP protocol codec)
+-------------------------+  internal/adapter/transport  (net.Dial / net.Listen)
```

Dependencies flow top-down only:

- `pkg/domain` is a pure-stdlib value-object package; it never
  imports from `internal/`.
- `pkg/usbip` is the public facade. It is the *one* package allowed
  to import from `internal/` (it composes `internal/app` services
  on top of `internal/adapter/*` defaults). Consumers of the library
  never reach `internal/` directly.
- `internal/app` declares the adapter interfaces it consumes and may
  import `internal/adapter/wire` because the codec types appear on
  those interface signatures. It does NOT import
  `internal/adapter/kernel` or `internal/adapter/transport`; those
  are injected via interface, never through a direct package
  dependency.
- No adapter package imports `internal/app`.

These rules are enforced mechanically by the `domain-boundary` job in
the [`_arch-checks.yml`](../.github/workflows/_arch-checks.yml)
reusable workflow (called from `ci.yml`'s `arch:` job).

## Package responsibilities

### `pkg/usbip`

The only package external consumers import. It declares `Importer`,
`Exporter`, `AttachOptions`, every `With*` option constructor, the
public backoff strategies, and the sentinel errors. All method bodies
are 1:1 forwards to `internal/app` after trivial argument translation.
The facade exists so the internal layer can evolve without breaking
the public API; v1 contract §5.7 documents this surface.

### `pkg/domain`

Pure value objects: `Device`, `Port`, `Session`, `BusID`,
`RemoteEndpoint`, `Event` and its nine concrete variants,
`EventKind`, `USBClass`, `Speed`, `Status`, and the sentinel errors
(`ErrDeviceNotFound`, `ErrBusIDInvalid`, `ErrKernelModuleMissing`,
etc.). No I/O, no goroutines, no third-party imports — only stdlib
and `github.com/google/uuid` for `SessionID`.

Because `pkg/domain` types are returned across the package boundary,
they participate in the `apidiff` baseline. Any incompatible change
requires a `BREAKING:` commit and a baseline regeneration.

### `internal/app`

Implements the use-case services:

- `Importer` — `ListRemote`, `Attach`, `Detach`, `ListPorts`, `Watch`,
  `Close`.
- `Exporter` — `ListAvailable`, `Bind`, `Unbind`, `Serve`, `Sessions`,
  `WatchSessions`, `Shutdown`.
- Reconnect watcher with per-port generation tokens (v1 contract §5.5).
- Session accounting, ACL enforcement, accept rate limiting (spec
  §11.5.3).
- Closed-set outcome enums (AttachOutcome, ReconnectOutcome, etc.) used
  as `slog.String("outcome", …)` field values for journald queries
  (no Prometheus dependency — see ADR-0010).

`internal/app` declares every adapter interface it consumes
(`ImporterKernel`, `ExporterKernel`, `KernelEvents`, `WireCodec`,
`Transport`, `Clock`). Adapter packages implement the interfaces and
are composed in via `pkg/usbip`'s constructors. The app layer
imports `internal/adapter/wire` because the codec value types appear
on those interface signatures, but it does NOT import
`internal/adapter/kernel` or `internal/adapter/transport` —
the `domain-boundary` CI job (in `_arch-checks.yml`) verifies that
latter rule.

### `internal/adapter/kernel`

Linux-only (`//go:build linux` in the top-level files and
`_linux.go` suffix on syscall code). Owns every sysfs read/write
under `/sys/bus/usb/drivers/` and `/sys/devices/platform/vhci_hcd.*`,
plus the netlink uevent subscription used by the `KernelEvents`
interface. All writes are serialised on a per-adapter mutex.

Non-Linux builds compile a stub that returns `ErrKernelModuleMissing`
from every operation. This keeps `pkg/usbip` portable — a macOS
developer can `go build ./...` and `go test ./...` without needing
Linux-only tags everywhere.

### `internal/adapter/wire`

Pure encode/decode of the USB/IP wire protocol. No I/O. The codec
owns opcode constants, the 8-byte OP header, the
`OP_REQ_DEVLIST`/`OP_REP_DEVLIST`/`OP_REQ_IMPORT`/`OP_REP_IMPORT`
layouts, and the 312-byte device descriptor. All integers are
network-byte-order via `encoding/binary.BigEndian`. Byte-level
layouts are duplicated in [`protocol.md`](protocol.md) for easy
lookup without reading Go source.

### `internal/adapter/transport`

Thin wrappers around `net.Dialer.DialContext` and `net.ListenConfig`.
The seam exists so tests can inject a fake transport without going
through the Linux kernel stack. `TCP_NODELAY` is enabled on every
accepted connection to minimise handshake latency.

### `cmd/usbip-go`

Cobra-based single-binary CLI entrypoint with flat top-level verbs
(see ADR-0011). The binary consumes `pkg/usbip` only; it does not
import `internal/*` directly. `usbip-go serve` runs the production
daemon; `usbip-go {list,attach,detach,bind,unbind,watch,drain,
version,completion}` cover the client and operator commands.

### `examples/`

Five minimal library-embed programs that each demonstrate one public
API pattern. Every example builds with `go build ./examples/...` and
is covered by the `cross-compile` CI job (in `_arch-checks.yml`).

## Concurrency model

- Every accepted exporter session runs in its own goroutine.
- `Importer` and `Exporter` public methods are safe for concurrent
  use.
- Kernel adapter writes serialise on a per-adapter mutex.
- `Watch` / `WatchSessions` return `iter.Seq[Event]` backed by
  buffered channels; cancelling context closes the sequence.
- Background watcher goroutines are owned by their service and
  terminate on `Close()` / `Shutdown()`.
- Goroutine-owning test packages (`internal/app`,
  `internal/adapter/kernel`, `test/integration`,
  `test/conformance`) install `goleak.VerifyTestMain` to catch
  goroutine leaks at the package boundary.

See v1 contract §3.4 for the authoritative lifecycle-semantics list
(double-detach idempotency, Shutdown drain semantics, runtime module
disappearance, etc.).

## Error strategy

Adapter layer returns raw errors (syscall errno, protocol decode
failure, `net` error). The application layer wraps with
`github.com/samber/oops` to attach context attributes (`busid`,
`remote`, `port_id`, `attempt`). `slog` emits those attributes
automatically in structured output.

Every returned error is classifiable via `errors.Is` against one of
the sentinels in `pkg/usbip/errors.go`. The kernel-to-domain error
map is in v1 contract §6.4.

## Layering rules (enforced)

1. **Go `internal/` rule** — packages outside the module cannot
   import anything under `internal/`. This is a compiler-level
   check.
2. **`pkg/domain` must not import `internal/`** — enforced by the
   `domain-boundary` CI job (in `_arch-checks.yml`). The domain
   package stays a pure-stdlib value-object surface — the same
   job also rejects any third-party import (anything whose path
   contains a dot in the host segment) so consumers of the library
   never inherit our supply-chain risk via the value-object surface.
3. **`pkg/usbip` is the sole `pkg/` package allowed to import
   `internal/`** — by design: it is the public facade that
   composes `internal/app` services on top of `internal/adapter/*`
   defaults. Consumers of the library never reach `internal/`
   directly.
4. **`internal/app` must not import `internal/adapter/kernel` or
   `internal/adapter/transport`** — enforced by the same CI job.
   The app layer reaches kernel and transport adapters only
   through the interfaces it declares. It DOES import
   `internal/adapter/wire` because codec value types appear on
   those interface signatures.
5. **No cgo anywhere** — enforced by the `pure-go` CI job (in
   `_arch-checks.yml`) via both `go list -f '{{.CgoFiles}}'` and
   source grep for `import "C"`.
