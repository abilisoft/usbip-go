# Single binary `usbip-go` uses flat verbs, not grouped subcommands

The `usbip-go` binary exposes its full command surface as flat
top-level verbs (`usbip-go attach`, `usbip-go serve`, `usbip-go
drain`, ...), not grouped under categories (`usbip-go device attach`,
`usbip-go server start`, ...).

| Command | Role | Notes |
|---|---|---|
| `usbip-go list` | Importer | `-l` for local, `-r <host>` for remote |
| `usbip-go attach` | Importer | `-r <host> -b <busid>` |
| `usbip-go detach` | Importer | `-p <port>` |
| `usbip-go port` | Importer | list currently-attached vhci ports; `--id <N>` filters |
| `usbip-go watch` | Importer | stream domain events until interrupted |
| `usbip-go bind` | Exporter | `-b <busid>` |
| `usbip-go unbind` | Exporter | `-b <busid>` |
| `usbip-go serve` | Exporter | run the daemon (replaces upstream `usbipd`) |
| `usbip-go drain` | Operator | tell a running daemon to refuse new sessions and exit |
| `usbip-go version` | — | build provenance |

## Rejected alternatives

**Grouped subcommands** (`usbip-go device attach`, `usbip-go server
start`): the ergonomic case for `kubectl` / `gh` style depends on a
large object space that requires taxonomy. With seven verbs whose role
is already clear from the verb itself (you bind to export and attach
to import), grouping adds typing without reducing ambiguity. Operators
who already know upstream `usbip attach -r host -b busid` would face
a breaking rename of the verb position for no gain — the upstream
verbs map 1:1 to ours, only the binary name is namespaced.

**Hybrid** (some verbs flat, others grouped): inconsistent — every
operator has to learn which verbs sit where.

## DDD note

`CONTEXT.md` defines two roles: **Exporter** (host sharing devices)
and **Importer** (host attaching remote devices). The CLI does NOT
surface those role names as command groups, because:

1. The verb already encodes the role unambiguously (`bind` is exporter-
   only, `attach` is importer-only). Operators reach for the verb,
   not the role.
2. The role split is internal architecture. Promoting it to the user
   surface would leak an implementation detail.
3. Upstream `usbip` (linux/tools/usb/usbip) has never grouped this
   way, and operators with muscle memory must not be forced to relearn
   the verb surface — only the binary name changes.

## Cobra layout

The single-binary `cmd/usbip-go/main.go` registers each verb as a top-
level `*cobra.Command` under the root. There is no intermediate group
command. Persistent flags (`--log-level`, `--log-format`, `--verbose`,
`--config`) live on the root; per-verb flags (`-r`, `-b`, `-p`,
`--listen`, `--health-addr`) live on each subcommand. Hidden helpers
like `completion`, `__complete` follow Cobra's defaults.

## Migration

The previous two-binary tree (`cmd/usbip-go/` for the importer CLI
and `cmd/usbipd-go/` for the daemon) collapsed into `cmd/usbip-go/`.
The daemon's existing logic (`serve.go`, `drain.go`, `health.go`,
`status.go`, etc.) became the implementation of `usbip-go serve` and
`usbip-go drain`. Build artefacts, GoReleaser config, and packaging
templates point at the single binary path.

A second rename followed: `cmd/usbip` → `cmd/usbip-go`, default
status-socket group `usbip` → `usbip-go`, and systemd unit names
`usbip.{service,socket}` → `usbip-go.{service,socket}`. The change
disambiguates against upstream `usbip` (linux/tools/usb/usbip), which
ships in `linux-tools` packages on the same host. Operators upgrading
from a build that used the old binary name see a hard `unknown
command` error rather than silent collision; activation fds carrying
the legacy `FileDescriptorName=usbip` are accepted as the singleton
fallback with a Warn log naming the expected `usbip-go` label so the
unit can be realigned.
