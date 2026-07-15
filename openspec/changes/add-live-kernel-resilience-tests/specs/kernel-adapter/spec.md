## MODIFIED Requirements

### Requirement: VHCI status parsing is defensive

The adapter SHALL parse `status` and `status.N` files using one freshly
discovered validated status topology per operation, skip malformed rows with a
warning, and fail on controller-window inconsistencies. It SHALL preserve an
ordinary `VDEV_ST_NOTASSIGNED` row whose socket field has the normal six-digit
width as a claimed Port. It SHALL reject Linux's exact
`status_show_not_ready` placeholder, identified by its sixteen-zero socket
field and otherwise zero not-assigned shape, so listing and allocation fail
closed without returning partial or synthetic Ports.

#### Scenario: Status row is malformed

- **WHEN** a status row has the wrong shape or invalid values
- **THEN** the row is skipped and a warning is logged

#### Scenario: Status row belongs to the wrong controller file

- **WHEN** a parsed flat port falls outside the controller file's valid window
- **THEN** the status read fails because kernel state is inconsistent

#### Scenario: Controller status is not ready

- **WHEN** a status snapshot contains Linux's exact sixteen-zero `status_show_not_ready` placeholder
- **THEN** the status read fails without returning partial or synthetic Ports
- **AND** `ListPorts` and free-Port allocation propagate the error instead of reporting claimed capacity or `ErrNoFreePort`
- **AND** an ordinary six-digit-socket `NotAssigned` row remains a claimed Port

### Requirement: Kernel errors map to public sentinels

Kernel adapter errors SHALL classify common sysfs and errno failures into
domain sentinels such as permission, not found, already bound, not bound, no
free port, unsupported device, and missing module. After Port range validation,
an `EINVAL` returned by the detach sysfs write SHALL classify as not bound while
preserving the underlying errno. This classification SHALL NOT apply to other
`EINVAL` paths or to `EIO`.

#### Scenario: Permission errno occurs

- **WHEN** a sysfs operation returns EACCES or EPERM
- **THEN** the returned error matches `ErrPermission`

#### Scenario: Module disappears at runtime

- **WHEN** a required `/sys/module` entry is absent before an operation
- **THEN** the returned error matches `ErrKernelModuleMissing`

#### Scenario: Detach write reports an already-free Port

- **WHEN** an in-range detach sysfs write returns `EINVAL`
- **THEN** the returned error matches `ErrDeviceNotBound`
- **AND** the returned error continues to wrap `EINVAL`

#### Scenario: Unrelated kernel operation returns EINVAL

- **WHEN** an operation other than the validated detach sysfs write returns `EINVAL`
- **THEN** the adapter does not classify that error as `ErrDeviceNotBound`
