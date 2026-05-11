## Purpose

Define the repository architecture boundaries that keep usbip-go embeddable, testable, pure-Go, and compatible with Linux USB/IP userspace.

## Requirements

### Requirement: Dependency direction is top-down only
The repository SHALL preserve the layering `cmd/examples -> pkg -> internal/app -> internal/adapter`, with no reverse dependency from lower layers into higher layers.

#### Scenario: Command code invokes behavior
- **WHEN** a CLI subcommand needs USB/IP behavior
- **THEN** it calls the public `pkg/usbip` facade
- **AND** it does not import `internal/app` or adapter packages directly

#### Scenario: Adapter code is compiled
- **WHEN** adapter packages implement sysfs, netlink, wire, or transport details
- **THEN** they do not import `internal/app`

### Requirement: pkg/domain is pure stdlib domain data
`pkg/domain` SHALL contain value objects, entity shapes, event types, enums, and public sentinel errors without I/O, goroutines, third-party imports, or internal-package imports.

#### Scenario: Value-object package gains a dependency
- **WHEN** a contributor adds an import to `pkg/domain`
- **THEN** architecture CI accepts only Go standard-library or same-module imports that preserve the no-internal boundary

### Requirement: pkg/usbip is the sole public service facade
`pkg/usbip` SHALL be the only public package allowed to compose internal services and adapters.

#### Scenario: External caller constructs services
- **WHEN** an external caller needs an Importer or Exporter
- **THEN** they call `usbip.NewImporter` or `usbip.NewExporter`
- **AND** adapter injection remains hidden from the public API

### Requirement: internal/app owns use-case orchestration and interfaces
`internal/app` SHALL implement Importer and Exporter use cases and declare the consumer-owned interfaces for kernel, event, transport, codec, and clock dependencies.

#### Scenario: App layer needs kernel behavior
- **WHEN** importer or exporter use cases need sysfs or netlink operations
- **THEN** they call interfaces declared in `internal/app`
- **AND** concrete kernel adapters are injected by the facade

#### Scenario: App layer needs wire value types
- **WHEN** app interfaces mention codec opcodes or wire types
- **THEN** `internal/app` may import `internal/adapter/wire`
- **AND** this exception does not allow importing kernel or transport adapters

### Requirement: Adapter packages own external-system details
Adapter packages SHALL isolate Linux sysfs/netlink, TCP transport, and USB/IP binary encoding details from use-case and public packages.

#### Scenario: Kernel sysfs changes
- **WHEN** sysfs path formats or errno mapping change
- **THEN** the change is localized to `internal/adapter/kernel` and its tests

#### Scenario: TCP tuning changes
- **WHEN** socket option behavior changes
- **THEN** the change is localized to `internal/adapter/transport` and `internal/netopts`

### Requirement: Non-Linux builds compile with explicit unsupported behavior
The public facade SHALL remain buildable on non-Linux systems while real importer/exporter operations return kernel-module-missing behavior.

#### Scenario: Non-Linux caller constructs defaults
- **WHEN** a non-Linux build calls `usbip.NewImporter` or `usbip.NewExporter`
- **THEN** construction returns `ErrKernelModuleMissing` instead of failing compilation

### Requirement: No cgo is allowed
The repository SHALL remain pure Go with no cgo files and no `import "C"`.

#### Scenario: cgo is introduced
- **WHEN** CI scans package metadata and source files
- **THEN** any cgo usage fails the pure-Go architecture gate

### Requirement: OpenSpec is the source of truth for current behavior
Durable architectural and behavioral decisions SHALL be captured in the relevant OpenSpec capability under `openspec/specs/` when they describe current behavior.

#### Scenario: CLI shape changes
- **WHEN** the project changes command grouping or binary topology
- **THEN** `openspec/specs/cli-interface/spec.md` is updated in the same change

### Requirement: Main specs describe current behavior
OpenSpec files under `openspec/specs/` SHALL describe behavior currently present in the repository, while future changes are captured under `openspec/changes/`.

#### Scenario: New feature is proposed
- **WHEN** a behavior is not implemented yet
- **THEN** it is represented as a change proposal/delta spec instead of being added directly to main specs

