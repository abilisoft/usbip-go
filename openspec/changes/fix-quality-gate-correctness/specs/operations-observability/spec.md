## MODIFIED Requirements

### Requirement: Build provenance is visible at startup and in status

The binary SHALL expose version and commit metadata through `usbip-go version`, daemon startup logs, and status output where applicable. Release-configured Bazel builds SHALL populate version, commit, and build date from declared workspace-status inputs; ordinary unstamped development builds SHALL retain explicit compiled fallback values.

#### Scenario: Daemon starts

- **WHEN** `usbip-go serve` starts
- **THEN** startup logs include version, commit, build date value, and Go version
- **AND** unstamped fields retain their compiled default values

#### Scenario: Bazel distribution binary reports provenance

- **WHEN** the production binary is built through the Bazel release configuration
- **THEN** version, commit, and build date are populated from repository status metadata

## ADDED Requirements

### Requirement: Event stream failures are observable

An importer event stream SHALL distinguish normal caller cancellation from failure to subscribe and unexpected upstream source loss. Error-aware consumers MUST receive the underlying classified error or a stable event-stream-closed error.

#### Scenario: Kernel event subscription fails

- **WHEN** the importer cannot establish its KernelEvents subscription
- **THEN** the error-aware event iterator yields the subscription error

#### Scenario: Kernel event source closes unexpectedly

- **WHEN** an established KernelEvents source closes while the Importer and caller context remain live
- **THEN** the error-aware event iterator yields a stable unexpected-closure error

#### Scenario: Caller cancels event watching

- **WHEN** the caller context is cancelled
- **THEN** iteration ends without reporting source failure
