# Architecture

This document summarises the usbip-go layering for operators and
contributors.

## Layers

```text
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

These rules are enforced mechanically by the Bazel-backed lint suite
run from [`ci.yml`](../.github/workflows/ci.yml) via `make lint`.

## Package responsibilities

### `pkg/usbip`

The primary public service facade; consumers may also import `pkg/domain` for
domain values. It declares `Importer`, `Exporter`, `AttachOptions`, every
`With*` option constructor, the public backoff strategies, and sentinel errors.
Methods forward to `internal/app` after public/internal translation, while
constructors compose the production adapters. The facade exists so the
internal layer can evolve without breaking the public API;
`openspec/specs/public-library-api/spec.md` documents this surface.

### `pkg/domain`

Pure value objects: `Device`, `Port`, `Session`, `BusID`,
`RemoteEndpoint`, `Event` and its eight concrete variants,
`EventKind`, `USBClass`, `Speed`, `Status`, and the sentinel errors
(`ErrDeviceNotFound`, `ErrBusIDInvalid`, `ErrKernelModuleMissing`,
etc.). No I/O, no goroutines, no third-party imports at all —
`SessionID` is a UUIDv7 generated inline against `crypto/rand` +
`encoding/binary` + `encoding/hex` so the value-object surface stays
pure-stdlib. The `domain-boundary` CI gate uses `go list` to
enumerate every import under `pkg/domain` and rejects any path that
is not stdlib or a self-module reference, so a future contributor
cannot reintroduce a third-party value-object dependency.

Because `pkg/domain` types are returned across the package boundary,
they participate in the `apidiff` baseline. Any incompatible change
requires a Conventional Commit breaking marker (`!` in the subject or a
`BREAKING CHANGE:` footer) and a baseline regeneration.

### `internal/app`

Implements the use-case services:

- `Importer` — `ListRemote`, `Attach`, `Detach`, `ListPorts`, `Watch`,
  `Close`.
- `Exporter` — `ListAvailable`, `Bind`, `Unbind`, `Serve`, `Sessions`,
  `WatchSessions`, `Shutdown`.
- Reconnect watcher with per-port generation tokens (see `openspec/specs/importer-lifecycle/spec.md`).
- Importer-level `BackoffFactory` state is created once per logical Attachment
  after lifecycle, validation, and duplicate checks; reconnect generations keep
  that same instance. Legacy shared custom strategies are serialized at the
  public facade.
- Session accounting, ACL enforcement, and accept rate limiting, specified in
  `openspec/specs/security-release-quality/spec.md`.
- `ListenAndServe` reserves the authoritative Serve lifecycle before the
  transport adapter binds. Shutdown cancels listener setup through the
  reservation context before waiting for the accept loop.
- Closed-set outcome enums (AttachOutcome, ReconnectOutcome, etc.) used
  as `slog.String("outcome", …)` field values for journald queries
  (no Prometheus dependency — see `openspec/specs/operations-observability/spec.md`).

`internal/app` declares every adapter interface it consumes
(`ImporterKernel`, `ExporterKernel`, `KernelEvents`, `WireCodec`,
`Transport`, `Clock`). Adapter packages implement the interfaces and
are composed in via `pkg/usbip`'s constructors. The app layer
imports `internal/adapter/wire` because the codec value types appear
on those interface signatures, but it does NOT import
`internal/adapter/kernel` or `internal/adapter/transport` —
the Bazel-backed `make lint` CI gate verifies that latter rule.

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
(see `openspec/specs/cli-interface/spec.md`). The binary consumes `pkg/usbip` only; it does not
import `internal/*` directly. `usbip-go serve` runs the production
daemon; `usbip-go {list,attach,detach,bind,unbind,watch,drain,
version,completion}` cover the client and operator commands.

### `examples/`

Four minimal library-embed programs that each demonstrate one public
API pattern. Every example builds with `go build ./examples/...` and
is covered by the Bazel build in `make build`.

## Concurrency model

- Every accepted exporter session runs in its own goroutine.
- `Importer` and `Exporter` public methods are safe for concurrent
  use.
- Kernel adapter writes serialise on a per-adapter mutex.
- `Watch` / `WatchSessions` return compatibility `iter.Seq[Event]` values
  backed by buffered channels; `WatchWithErrors` adds terminal subscription
  and source failures for assured importer monitoring. Subscriber closure is a
  publication barrier: terminal Close/Shutdown drains events accepted before
  that barrier, while caller context cancellation stops immediately.
- Background watcher goroutines are owned by their service and
  terminate on `Close()` / `Shutdown()`.
- Goroutine-owning test packages (`internal/app`,
  `internal/adapter/kernel`, `test/integration`,
  `test/conformance`) install `goleak.VerifyTestMain` to catch
  goroutine leaks at the package boundary.

See `openspec/specs/importer-lifecycle/spec.md` and
`openspec/specs/exporter-daemon/spec.md` for the authoritative lifecycle
semantics (double-detach idempotency, shutdown drain semantics, runtime module
disappearance, and related behavior).

Kernel-module observation is shape-stable across platforms. Common facade code
starts with the canonical `usbip_core`, `vhci_hcd`, and `usbip_host` entries as
`Unknown`; Linux replaces entries as each sysfs observation completes. A
cancelled probe therefore returns a complete map plus the wrapped context error,
not an ambiguous missing key.

## Error strategy

Adapters preserve underlying syscall, protocol, and network errors while
mapping documented domain conditions to sentinels. Application paths add
operation context, including values such as `busid`, `remote`, `port_id`, and
`attempt`, with `fmt.Errorf` wrapping or `github.com/samber/oops` attributes.

Documented domain and lifecycle conditions are classifiable via `errors.Is`
against sentinels in `pkg/usbip/errors.go`. The kernel-to-domain error map is
covered by `openspec/specs/domain-model/spec.md` and package tests.

## Layering rules (enforced)

1. **Go `internal/` rule** — packages outside the module cannot
   import anything under `internal/`. This is a compiler-level
   check.
2. **`pkg/domain` must not import `internal/`** — enforced by the Bazel-backed lint suite run by `make lint`. The domain
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
5. **No cgo anywhere** — enforced by the Bazel-backed lint suite via both package metadata
   checks and source grep for `import "C"`.
