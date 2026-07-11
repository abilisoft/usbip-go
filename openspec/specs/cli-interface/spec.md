## Purpose

Specify the `usbip-go` command-line interface, global flags, output contracts, shell completion behavior, and exit-code semantics.

## Requirements

### Requirement: CLI is a single binary with flat subcommands

The project SHALL ship one binary named `usbip-go` with flat top-level subcommands for importer, exporter, daemon, operator, version, and completion workflows.

#### Scenario: Operator inspects help

- **WHEN** `usbip-go --help` is run
- **THEN** commands are exposed as flat verbs such as `list`, `attach`, `detach`, `port`, `bind`, `unbind`, `watch`, `serve`, `drain`, `version`, and `completion`
- **AND** role nouns such as `device` or `server` are not required command groups

### Requirement: Global flags configure output, logging, verbosity, color, and network completion

The root command SHALL support `--output/-o`, `--log-level`, `--log-format`, `--verbose/-v`, `--no-color`, and `--complete-network`.

#### Scenario: Output flag is invalid

- **WHEN** an operator passes an output format other than `table` or `json`
- **THEN** the command returns a usage error

#### Scenario: No-color is enabled

- **WHEN** `--no-color` is supplied
- **THEN** ANSI colors are disabled for pretty logging and table rendering

### Requirement: CLI omits deferred configuration and misplaced drain flags

The root command SHALL NOT expose a persistent `--config` flag until YAML configuration exists, and SHALL NOT expose `--drain-timeout` outside the `drain` subcommand.

#### Scenario: Deferred config flag is requested

- **WHEN** an operator passes `--config PATH`
- **THEN** cobra treats it as an unknown flag rather than accepting an unread configuration path

#### Scenario: Daemon drain timeout is requested on serve

- **WHEN** an operator passes `usbip-go serve --drain-timeout=10s`
- **THEN** cobra treats it as an unknown flag
- **AND** daemon-side shutdown behavior remains controlled by `--shutdown-timeout`

### Requirement: list command defaults local and accepts an optional remote

`usbip-go list` SHALL list locally exportable devices by default. `usbip-go list HOST` SHALL parse HOST as a RemoteEndpoint and list that peer's devices. Listing source flags such as `--remote/-r`, `--local/-l`, and `--ports/-p` SHALL NOT be exposed; attached ports are listed with `usbip-go port`.

#### Scenario: Local listing is selected by default

- **WHEN** `usbip-go list` runs
- **THEN** the CLI renders locally exportable devices

#### Scenario: Remote listing is selected

- **WHEN** `usbip-go list HOST` runs
- **THEN** the CLI parses HOST as a RemoteEndpoint and renders the peer's devices

#### Scenario: Removed selector flag is supplied

- **WHEN** `--remote`, `--local`, or `--ports` is supplied to `usbip-go list`
- **THEN** cobra rejects the command as an unknown flag before running the use case

### Requirement: attach uses positional remote and BusID arguments

`usbip-go attach` SHALL accept `<remote> <busid>` positional arguments and optional reconnect flags.

#### Scenario: Attach with reconnect flags

- **WHEN** `usbip-go attach HOST BUSID --auto-reconnect --max-attempts N --backoff SPEC` runs
- **THEN** the CLI translates the flags into AttachOptions

#### Scenario: Backoff spec is invalid

- **WHEN** `--backoff` is not a valid fixed or exponential spec
- **THEN** the command returns a usage-class error

### Requirement: detach and port operate on numeric port IDs

`usbip-go detach <port>` SHALL detach a Port by ID, and `usbip-go port --id N` SHALL filter port output to one active Port.

#### Scenario: Port filter misses

- **WHEN** `usbip-go port --id N` is supplied for a non-attached Port
- **THEN** the command returns a not-found classified error

### Requirement: bind and unbind operate on BusID

`usbip-go bind <busid>` SHALL make a local Device exportable, and `usbip-go unbind <busid>` SHALL return it to its previous driver when possible.

#### Scenario: Bind succeeds

- **WHEN** a valid local BusID is bound
- **THEN** table mode prints a human-oriented acknowledgement
- **AND** JSON mode prints an ack envelope with schema `v1`, op `bind`, ok `true`, and the BusID

### Requirement: watch streams domain events until interrupted

`usbip-go watch` SHALL subscribe to importer-visible events and render them continuously until iteration ends or SIGINT/SIGTERM cancels the command.

#### Scenario: JSON watch output is selected

- **WHEN** `usbip-go watch --output=json` receives events
- **THEN** it emits one schema-versioned JSON object per line

### Requirement: serve runs the exporter daemon

`usbip-go serve` SHALL run the daemon with flags for listen address, status socket, status socket group, health address, ACLs, session caps, rate limit, handshake caps/timeouts, and shutdown timeout.

#### Scenario: systemd socket activation is present

- **WHEN** systemd passes a named USB/IP listener
- **THEN** `--listen` is ignored and the daemon serves on the activation listener

#### Scenario: health address is omitted

- **WHEN** `--health-addr` is empty
- **THEN** no separate health HTTP listener is started

### Requirement: drain talks to the status socket

`usbip-go drain` SHALL request graceful daemon shutdown over the configured status UDS and poll until sessions drain, the daemon exits, or the operator-side drain timeout expires.

#### Scenario: Repeated drain requests occur

- **WHEN** drain is called multiple times against the same daemon
- **THEN** the daemon-side drain action is idempotent

#### Scenario: Drain polls for completion

- **WHEN** `POST /drain` succeeds
- **THEN** the client immediately polls `GET /`
- **AND** subsequent polls occur at `--poll-interval`, defaulting to 200ms
- **AND** success requires `sessions` to be empty and `listening.accepting` to be false, or the daemon's status socket to disappear after the initial POST

#### Scenario: Drain timeout expires

- **WHEN** the polling loop exceeds `--drain-timeout`
- **THEN** the command returns the timeout exit code
- **AND** the daemon continues following its own server-side `--shutdown-timeout`

### Requirement: JSON output uses schema v1 envelopes

Machine-readable CLI output SHALL include `"schema": "v1"` and use stable field names for device, port, session, acknowledgement, status, and event objects.

#### Scenario: Command succeeds in JSON mode

- **WHEN** a list or mutating command succeeds with `--output=json`
- **THEN** stdout contains the documented v1 envelope
- **AND** failure is represented by non-zero exit code plus stderr, not by `{"ok": false}`

### Requirement: Exit codes are operator-stable

The binary SHALL map classified errors to stable numeric exit codes: success `0`, generic `1`, usage `2`, permission `3`, kernel module `4`, device not found `5`, device busy `6`, protocol mismatch `7`, network `8`, timeout `9`, no free port `10`, protocol error `11`, already running `12`, and interrupted `130`.

#### Scenario: Permission error occurs

- **WHEN** a sysfs operation fails due to insufficient privilege
- **THEN** the process exits with the permission exit code

#### Scenario: Context is interrupted

- **WHEN** SIGINT cancels a running command
- **THEN** the process exits with Unix-conventional interrupted code 130

#### Scenario: Stderr text contains newlines

- **WHEN** a classified error detail contains embedded newlines
- **THEN** formatted stderr collapses them into a single line

#### Scenario: Status socket is disabled for drain

- **WHEN** `usbip-go drain --status-socket ''` runs
- **THEN** the command returns the usage exit code with a targeted status-socket-disabled message

### Requirement: Shell completion avoids surprise network dials

Completion SHALL provide static suggestions by default and SHALL only dial remotes for attach BusID completion when `--complete-network` or `USBIP_COMPLETE_NETWORK=1` is set.

#### Scenario: Network completion is disabled

- **WHEN** completing the second positional argument of `attach`
- **THEN** no network dial is attempted unless the operator explicitly opted in

#### Scenario: Network completion is enabled

- **WHEN** completing the second positional argument of `attach` with network completion enabled
- **THEN** the CLI caps the remote list operation at 800ms
- **AND** completion failures are silent

### Requirement: Attach host completion uses private XDG state history

The CLI SHALL record successful attach remotes in a most-recent-first history file under XDG state storage and use that history for first-argument attach completion.

#### Scenario: Attach completes successfully

- **WHEN** `usbip-go attach HOST BUSID` succeeds
- **THEN** HOST is recorded in `$XDG_STATE_HOME/usbip-go/history` or `$HOME/.local/state/usbip-go/history`
- **AND** the state directory is created with mode `0700`
- **AND** the history file is written with mode `0600`

#### Scenario: History exceeds capacity

- **WHEN** more than 20 distinct hosts have been recorded
- **THEN** only the 20 most recent hosts are retained
- **AND** recording an existing host moves it to the most-recent position rather than duplicating it

#### Scenario: Completion reads unavailable history

- **WHEN** the history file is missing, unreadable, or malformed
- **THEN** completion silently returns no history suggestions

### Requirement: Completion install writes shell-specific scripts

`usbip-go completion install` SHALL generate cobra completion scripts for supported shells and write them to XDG data paths, with dry-run and uninstall support.

#### Scenario: Shell is inferred

- **WHEN** `--shell` is omitted
- **THEN** the command infers the target shell from the basename of `$SHELL`

#### Scenario: Completion path is resolved

- **WHEN** the target shell is `bash`, `zsh`, `fish`, `pwsh`, or `powershell`
- **THEN** the install path is respectively `$XDG_DATA_HOME/bash-completion/completions/usbip-go`, `$XDG_DATA_HOME/zsh/site-functions/_usbip-go`, `$XDG_DATA_HOME/fish/vendor_completions.d/usbip-go.fish`, or `$XDG_DATA_HOME/powershell/Modules/usbip-go.ps1`
- **AND** `$HOME/.local/share` is used when `$XDG_DATA_HOME` is unset

#### Scenario: Completion script is installed

- **WHEN** installation writes a script
- **THEN** parent directories are created with mode `0755`
- **AND** the script is written with mode `0644`

#### Scenario: Dry-run is selected

- **WHEN** `--dry-run` is supplied
- **THEN** the command prints the target path without writing a script

#### Scenario: Uninstall is selected

- **WHEN** `--uninstall` is supplied
- **THEN** the command removes the target script
- **AND** a missing target is treated as success
