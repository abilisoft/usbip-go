## MODIFIED Requirements

### Requirement: drain talks to the status socket

`usbip-go drain` SHALL request graceful daemon shutdown over the configured status UDS and poll until sessions drain, the daemon exits after its bounded shutdown attempt, or the operator-side drain timeout expires. A status response SHALL prove completion with a successful schema-v1 document and explicitly present drain fields.

#### Scenario: Repeated drain requests occur

- **WHEN** drain is called multiple times against the same daemon
- **THEN** the daemon-side drain action is idempotent

#### Scenario: Drain polls for completion

- **WHEN** `POST /drain` succeeds
- **THEN** the client immediately polls `GET /`
- **AND** subsequent polls occur at `--poll-interval`, defaulting to 200ms
- **AND** success requires an explicit empty `sessions` array and explicit false `listening.accepting`, or the daemon's status socket to disappear after the initial POST and bounded shutdown attempt

#### Scenario: Drain status response cannot prove completion

- **WHEN** `GET /` is non-2xx, uses a schema other than `v1`, or omits or nulls `sessions`, `listening`, or `listening.accepting`
- **THEN** the drain command fails closed instead of interpreting missing data as Go zero values
- **AND** unknown additive schema-v1 fields remain accepted

#### Scenario: Drain timeout expires

- **WHEN** the polling loop exceeds `--drain-timeout`
- **THEN** the command returns the timeout exit code
- **AND** the daemon continues following its own server-side `--shutdown-timeout`

### Requirement: Attach host completion uses private XDG state history

The CLI SHALL record successful attach remotes in a most-recent-first history file under XDG state storage and use that history for first-argument attach completion. Updates SHALL be private, atomic for readers, and serialized across CLI processes.

#### Scenario: Attach completes successfully

- **WHEN** `usbip-go attach HOST BUSID` succeeds
- **THEN** HOST is recorded in `$XDG_STATE_HOME/usbip-go/history` or `$HOME/.local/state/usbip-go/history`
- **AND** the state directory is created with mode `0700`
- **AND** the history file and sidecar lock use mode `0600`
- **AND** an existing history or lock file with broader permissions is corrected to mode `0600`

#### Scenario: History exceeds capacity

- **WHEN** more than 20 distinct hosts have been recorded
- **THEN** only the 20 most recent hosts are retained
- **AND** recording an existing host moves it to the most-recent position rather than duplicating it

#### Scenario: Concurrent history updates occur

- **WHEN** independent CLI processes record hosts concurrently
- **THEN** the read-modify-write transactions are serialized without losing any retained update
- **AND** readers observe an old or new complete history file rather than a partially written generation

#### Scenario: History or lock path is replaced by a symlink

- **WHEN** a history update encounters a symbolic link at the history or sidecar lock pathname
- **THEN** the update rejects the link instead of changing or replacing its target
- **AND** the successful attach result remains authoritative while the history failure is logged

#### Scenario: Completion reads unavailable history

- **WHEN** the history file is missing, unreadable, or malformed
- **THEN** completion silently returns no history suggestions
