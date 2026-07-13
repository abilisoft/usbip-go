## Why

Several public-facade edge cases could lose terminal events, perform network
side effects before lifecycle rejection, share mutable reconnect state, or
return ambiguous configuration and cancellation results. These defects sit at
API boundaries where deterministic precedence and complete observable state
are part of the compatibility contract.

## What Changes

- Drain subscriber buffers after a synchronized terminal publication barrier so
  already-published importer and exporter lifecycle events remain observable.
- Reserve exporter serving state before `ListenAndServe` creates a listener,
  and cancel in-flight listener setup during shutdown.
- Distinguish an omitted accept-rate option from an explicit finite zero and
  reject non-finite rates through an additive public sentinel.
- Make a closed Importer reject `Attach` before argument validation while
  retaining race-safe reservation checks.
- Return the canonical three-module probe shape on every platform and on every
  cancellation path, including cancellation between Linux module probes.
- Add a `BackoffFactory` importer option that creates independent strategy state
  per logical attachment while preserving and serializing the legacy shared
  custom-strategy path.
- Preserve the public `AttachOptions` struct shape and all existing finite,
  successful behavior. No protocol, encryption, authentication, or kernel data
  path changes are introduced.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `public-library-api`: Define additive backoff-factory and accept-rate error
  contracts, closed-Importer precedence, and shape-stable module probes.
- `importer-lifecycle`: Define terminal event draining and reconnect-strategy
  ownership across logical attachment generations.
- `exporter-daemon`: Define terminal session-event draining, explicit accept-rate
  semantics, and reservation-first serving lifecycle.
- `operations-observability`: Define complete module-probe results during
  cancellation and preservation of queued terminal lifecycle events.
- `transport-networking`: Require lifecycle reservation before transport bind
  and cancellation of an in-flight bind during shutdown.

## Impact

The change affects `pkg/usbip` facade options, errors, module probing, and
listener setup plus the corresponding `internal/app` lifecycle seams. It adds
only `BackoffFactory`, `WithImporterBackoffFactory`, and
`ErrAcceptRateLimitInvalid` to the public API; existing v1 symbols and the
public `AttachOptions` field layout remain intact. Focused deterministic and
race tests, API-diff evidence, documentation, and OpenSpec trace evidence are
required.
