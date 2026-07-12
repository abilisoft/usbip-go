## Context

The project can use only standard GitHub-hosted runners. Their Azure kernel does
not supply the exporter, VUDC, gadget-function, dummy HCD, writable configfs, and
root capabilities required by the live USB/IP integration suite. The existing
self-hosted workflow therefore cannot run, while a QEMU full-kernel workaround
proved slow and operationally fragile under software emulation.

Release-facing documentation also describes the temporary state in which every
published version is retracted. It must instead express the durable support
policy without hardcoding the intended next tag.

## Goals / Non-Goals

**Goals:**

- Make stable release automation runnable entirely on standard free hosted
  runners.
- Retain all normal unprivileged correctness, security, API, coverage, packaging,
  and provenance gates.
- Keep privileged kernel integration available and documented as a manual
  maintainer check on suitable Linux hardware.
- Keep release documentation accurate before and after publication without a
  concrete version literal.
- Correct the Linux usbip-host lifecycle event mapping found during integration
  validation.

**Non-Goals:**

- Simulating or silently skipping kernel integration in CI.
- Changing the USB/IP protocol, public API, CLI, or runtime security model.
- Automating SemVer selection or publishing prerelease tags.

## Decisions

### Limit GitHub Actions to supported hosted capabilities

The kernel-integration workflow and its release dependency are removed. Pull
request, nightly, and release automation continue to run the existing hosted
unit, conformance, architecture/API, security, coverage, mutation, local-CI, and
packaging targets. This avoids both a permanently queued self-hosted job and an
expensive VM layer that does not make privileged kernel behavior more reliable.

**Alternatives considered:**

- Retaining the self-hosted labels leaves every release blocked because no such
  runner is registered.
- Privileged Docker cannot add modules absent from the host kernel.
- Booting a full QEMU guest on every run adds network/tooling dependencies and
  software-emulation instability disproportionate to this project.

### Preserve kernel integration as a manual check

`make test-integration` remains the canonical Bazel-backed entry point. Its
requirements are stated explicitly: root, writable configfs, and the USB/IP,
gadget, VUDC, VHCI, and dummy HCD modules. Maintainers run it separately when
changing kernel-facing behavior and before a release when a capable environment
is available. GitHub Actions does not report it as passed, failed, or skipped.

### Classify Linux driver-core events

Linux emits usbip-host attachment as
`SUBSYSTEM=usb ACTION=bind DRIVER=usbip-host`, not as a synthetic
`SUBSYSTEM=usbip_host ACTION=add` event. The driver pointer is cleared before
the matching `ACTION=unbind`, so that payload omits `DRIVER`. The mapper records
bus IDs observed binding to usbip-host and maps only their matching unbind
notifications, preventing unrelated USB driver changes from becoming exporter
lifecycle events.

### Describe support symbolically

Documentation refers to the latest non-retracted stable release and uses
`X.Y.Z` placeholders. GitHub Releases, `SECURITY.md`, and the versionless
pkg.go.dev URL remain authoritative and do not require a documentation edit for
each patch release.

## Risks / Trade-offs

- **[Kernel regressions are not detected automatically]** → Keep the manual
  target documented, preserve focused unit tests for kernel-adapter logic, and
  record manual execution honestly rather than claiming CI coverage.
- **[Documentation merges before a release exists]** → Conditional wording
  remains accurate when the supported-release set is empty.
- **[A future hosted runner gains the required surface]** → Reintroducing an
  automated kernel job requires a reviewed OpenSpec change and live evidence.

## Migration Plan

1. Merge the workflow, implementation, OpenSpec, and documentation changes.
2. Confirm hosted PR and release gates complete on the free runner pool.
3. Run the full local release validation matrix and retain manual kernel-test
   evidence separately.
4. Push the signed stable tag only after the branch is merged and green.
5. Confirm artifact, provenance, support-page, and pkg.go.dev results.

Rollback is a normal pull request restoring the prior workflow contract only
after a suitable runner exists.

## Open Questions

None.
