## Context

Stable tag publication is gated on the reusable kernel-integration workflow.
That workflow currently targets a `usbip-kernel` self-hosted runner, while the
repository has no registered self-hosted runner and the owner can use only the
free GitHub-hosted pool. GitHub-hosted Ubuntu runners are disposable Azure VMs,
but their official kernel packages contain only the USB/IP core and VHCI
drivers. Ubuntu explicitly disables `CONFIG_USB_DUMMY_HCD` and omits the host,
VUDC, libcomposite, and gadget-function modules required by the full suite.

The live integration suite must exercise real configfs gadgets, USB/IP host and
VHCI drivers, VUDC handoff, and kernel-owned URB traffic. Substituting mocks,
containers that share an incapable host kernel, or skip-based success would
weaken the release gate and is not acceptable.

Release-facing documentation also describes the temporary state in which all
published versions are retracted. It must describe the durable policy instead
of embedding the next release number or becoming false immediately after a
tag is published.

## Goals / Non-Goals

**Goals:**

- Make the mandatory live-kernel workflow schedulable on the free
  GitHub-hosted runner pool.
- Boot a checksum-pinned full Debian kernel under QEMU and fail closed when the
  image, guest, or required module surfaces are unavailable.
- Preserve Bazel as the test entry point inside the guest and verify all
  downloadable VM inputs cryptographically.
- Keep README, operator, support, and schema guidance accurate before and after
  a supported release without a concrete version literal.
- Surface the dynamic pkg.go.dev documentation entry point.

**Non-Goals:**

- Changing USB/IP protocol, public API, CLI, or runtime behavior.
- Replacing live kernel tests with userspace simulation.
- Building a custom Linux kernel or VM image inside the release job.
- Supporting prerelease tag publication or automating SemVer selection.

## Decisions

### Use the pinned GitHub-hosted Ubuntu image

The integration job will use `ubuntu-24.04`, not `ubuntu-latest`, so an image
label migration cannot silently change the operating-system family. Standard
hosted runners are free for this public repository and provide passwordless
`sudo`.

**Alternative considered:** retain the self-hosted labels and make the gate
optional. This leaves releases unschedulable and violates the mandatory kernel
coverage contract.

### Run a checksum-pinned full kernel in QEMU

The workflow installs QEMU and cloud-image seed tooling, then boots a Debian
generic cloud image pinned by immutable release path and SHA-512. Unlike the
Azure and cloud-reduced kernel flavors, Debian's full amd64 kernel package
ships `dummy_hcd`, USB/IP host/VUDC/VHCI, libcomposite, and the required gadget
functions. The repository checkout is streamed into the disposable guest,
which loads modules, mounts configfs, verifies both dummy and VUDC UDCs, and
runs `make test-integration` as root.

KVM is used when the standard runner exposes it; QEMU TCG is the deterministic
fallback because nested virtualization is not guaranteed. The pinned guest
image is cached by content identity, and every cache hit is rehashed before
use. TCG runs advertise their execution mode to the integration harness so
kernel convergence waits remain fail-closed but allow for software-emulation
latency. Guest preflight loads both gadget/export modules and the host-side
CDC/storage drivers exercised by the scenarios.

Host `apt`, the guest image fetch, and the guest's verified bootstrap downloads
are declared non-hermetic exceptions. They are necessary because kernel state
cannot be supplied by Bazel. The integration command itself remains the stable
Bazel-backed Make entry point.

**Alternatives considered:**

- Privileged Docker still shares the hosted runner kernel and cannot provide
  absent modules.
- Installing `linux-modules-extra-$(uname -r)` on the Azure host was tested
  against Ubuntu's official package manifests and rejected because the required
  exporter, VUDC, gadget, and dummy HCD modules are not shipped.
- Building a kernel for every run would increase runtime and compiler
  supply-chain surface compared with a signed distribution image whose digest
  is pinned in the repository.

### Keep the release gate fail-closed

Image verification, VM boot, cloud-init, repository transfer, every `modprobe`,
configfs mounting, module presence, UDC presence, and configfs writeability are
hard failures. The workflow does not translate infrastructure failures into
skips. Existing test-level skip guards remain defensive for developer machines,
while CI preflight guarantees their prerequisites before the suite starts.

### Classify Linux driver-core events

The Linux driver core emits usbip-host attachment as
`SUBSYSTEM=usb ACTION=bind DRIVER=usbip-host`, not as a synthetic
`SUBSYSTEM=usbip_host ACTION=add` event. It clears the driver pointer before
the matching `ACTION=unbind`, so the unbind payload omits `DRIVER`. The event
mapper records bus IDs observed binding to usbip-host and maps only their
matching unbind notifications, preventing unrelated USB driver changes from
becoming exporter lifecycle events.

### Describe support symbolically

Documentation will refer to the latest non-retracted stable release and use
`X.Y.Z` placeholders in commands. It will link to GitHub Releases and
`SECURITY.md` instead of embedding the intended tag. The pkg.go.dev badge and
links remain versionless so they advance automatically after publication.

## Risks / Trade-offs

- **[Nested virtualization is unavailable or unstable]** → The harness falls
  back to QEMU TCG; the job timeout still fails closed if the guest cannot
  complete.
- **[Pinned guest image disappears]** → The checksum-pinned download fails;
  updating the image requires a reviewed source, checksum, tests, and OpenSpec
  evidence rather than silently following a mutable URL.
- **[Ubuntu/Debian repository network outage]** → Host tooling, cloud-init, or
  the guest bootstrap fails and release publication remains blocked.
- **[Guest kernel state leaks across tests]** → Every job creates a fresh qcow2
  overlay, the harness uses unique gadget names, and teardown remains mandatory.
- **[Documentation merged before a release exists]** → Conditional wording
  remains accurate when the set of supported releases is empty.

## Migration Plan

1. Merge the workflow, OpenSpec, regression-test, and documentation changes.
2. Manually dispatch kernel integration on `main` and require a complete pass.
3. Run the full local release validation matrix.
4. Push the signed stable tag only after both paths pass.
5. Confirm artifact, provenance, support-page, and pkg.go.dev results.

Rollback is a normal pull request reverting the hosted provisioning design.
The mandatory release dependency must remain in place during any rollback.

## Open Questions

None. The actual runner image is validated by dispatching this change before
release publication.
