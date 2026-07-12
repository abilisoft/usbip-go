## Why

The release pipeline currently requires a specially labelled self-hosted kernel
runner, but the project only has access to free GitHub-hosted runners. This
leaves every stable release permanently queued. GitHub's reduced Azure kernel
omits the exporter, VUDC, gadget, and dummy-host-controller modules, so the
free runner must host a full Linux kernel in an isolated guest VM.

## What Changes

- Run the dedicated USB/IP kernel integration workflow on the pinned free
  `ubuntu-24.04` GitHub-hosted image.
- Boot a SHA-512-pinned Debian generic cloud image under QEMU, load its full
  USB/IP/gadget module set, mount configfs, and fail closed if the image,
  guest, module, or writable gadget surface is unavailable.
- Keep live kernel integration as a mandatory release prerequisite; do not
  replace it with skips, mocks, containers, or a weaker userspace-only check.
- Document the narrow networked VM-image and host-tool provisioning exception
  to the otherwise hermetic Bazel-backed test pipeline.
- Prepare installation, support, and schema documentation for the next
  supported release without embedding a concrete release number in prose or
  commands.
- Preserve the existing plaintext USB/IP security model and all public v1 API
  behavior; correct Linux driver-core bind/unbind event classification without
  changing protocol, CLI, or public API semantics.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `release-packaging`: Stable releases use an available GitHub-hosted runner
  for the mandatory live-kernel gate and publish only after it passes.
- `security-release-quality`: Kernel integration provisions and verifies its
  required host capabilities without silently skipping coverage.
- `kernel-adapter`: Exporter lifecycle mapping consumes the real Linux
  `SUBSYSTEM=usb` driver-core bind/unbind event shape.

## Impact

- Affects `.github/workflows/vm-integration.yml`, release/operator
  documentation, OpenSpec traceability, and workflow regression coverage.
- Adds no Go dependency and changes no public Go or CLI API.
- Corrects an existing Linux event-classification defect exposed by the live
  hosted kernel gate.
- Uses Ubuntu's package repositories for QEMU/cloud-image tooling and a
  checksum-pinned Debian guest image because the hosted Azure kernel lacks the
  required modules; the guest test remains Bazel-backed.
