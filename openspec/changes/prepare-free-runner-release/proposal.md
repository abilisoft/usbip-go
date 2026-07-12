## Why

The stable release workflow depends on a kernel-integration job that requires a
special self-hosted runner, but the project has access only to standard free
GitHub-hosted runners. Those runners do not expose the USB/IP exporter, gadget,
dummy-host-controller, writable configfs, or root kernel surface required by the
suite, leaving releases permanently queued.

## What Changes

- Remove kernel integration from GitHub Actions and from the stable-release
  dependency graph.
- Keep `make test-integration` as an explicit manual maintainer check for a
  capable Linux host; do not convert missing kernel capabilities into a passing
  CI skip.
- Preserve the normal hosted security, unit, conformance, architecture,
  coverage, mutation, and release gates.
- Correct Linux driver-core usbip-host bind/unbind event classification exposed
  during manual integration validation.
- Prepare installation, support, and schema documentation for the next
  supported release without embedding a concrete release number.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `release-packaging`: stable releases use only gates that standard hosted
  runners can execute; privileged kernel integration is a separate manual check.
- `security-release-quality`: distinguishes automated unprivileged validation
  from manual privileged kernel integration.
- `kernel-adapter`: consumes the real Linux `SUBSYSTEM=usb` driver-core
  bind/unbind event shape for usbip-host lifecycle events.

## Impact

- Removes `.github/workflows/vm-integration.yml` and its release dependency.
- Updates contributor, operator, security, OpenSpec, and traceability docs.
- Adds no dependency and changes no public Go or CLI API.
- Corrects an internal Linux event-classification defect while preserving v1
  source and behavioral compatibility.
