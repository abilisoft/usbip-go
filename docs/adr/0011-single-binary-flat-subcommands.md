# Single binary `usbip` uses flat verbs, not grouped subcommands

The `usbip` binary exposes its full command surface as flat top-level
verbs (`usbip attach`, `usbip serve`, `usbip drain`, ...), not grouped
under categories (`usbip device attach`, `usbip server start`, ...).

| Command | Role | Notes |
|---|---|---|
| `usbip list` | Importer | `-l` for local, `-r <host>` for remote |
| `usbip attach` | Importer | `-r <host> -b <busid>` |
| `usbip detach` | Importer | `-p <port>` |
| `usbip bind` | Exporter | `-b <busid>` |
| `usbip unbind` | Exporter | `-b <busid>` |
| `usbip serve` | Exporter | run the daemon (replaces upstream `usbipd`) |
| `usbip drain` | Operator | tell a running daemon to refuse new sessions and exit |
| `usbip version` | — | build provenance |

## Rejected alternatives

**Grouped subcommands** (`usbip device attach`, `usbip server start`):
the ergonomic case for `kubectl` / `gh` style depends on a large object
space that requires taxonomy. With seven verbs whose role is already
clear from the verb itself (you bind to export and attach to import),
grouping adds typing without reducing ambiguity. Operators who already
know upstream `usbip attach -r host -b busid` would face a breaking
rename for no gain.

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
3. Upstream `usbip` has never grouped this way, and operators with
   muscle memory must not be forced to relearn the surface.

## Cobra layout

The single-binary `cmd/usbip/main.go` registers each verb as a top-
level `*cobra.Command` under the root. There is no intermediate group
command. Persistent flags (`--log-level`, `--log-format`, `--verbose`,
`--config`) live on the root; per-verb flags (`-r`, `-b`, `-p`,
`--listen`, `--health-addr`) live on each subcommand. Hidden helpers
like `completion`, `__complete` follow Cobra's defaults.

## Migration

The existing two-binary tree (`cmd/usbip-go/` for the importer CLI and
`cmd/usbipd-go/` for the daemon) collapses into `cmd/usbip/`. The
daemon's existing logic (`run.go`, `drain.go`, `health.go`,
`status.go`, etc.) becomes the implementation of `usbip serve` and
`usbip drain`. Build artefacts, GoReleaser config, and packaging
templates point at the single new binary path.
