## ADDED Requirements

### Requirement: Module probe cancellation preserves a complete observation shape

Operational kernel-module probing SHALL return the canonical `usbip_core`,
`vhci_hcd`, and `usbip_host` keys even when cancellation prevents some or all
observations. Unobserved entries SHALL be `Unknown` rather than absent.

#### Scenario: Status probe is cancelled before work
- **WHEN** module probing starts with a cancelled context
- **THEN** the result contains all three canonical keys as `Unknown`
- **AND** the returned error preserves the cancellation cause

#### Scenario: Status probe is cancelled partway through Linux observations
- **WHEN** cancellation occurs after one Linux module state is observed
- **THEN** that observed state is retained
- **AND** the two unobserved keys remain present as `Unknown`

### Requirement: Queued terminal lifecycle observations are not discarded

Terminal Importer Close and Exporter Shutdown SHALL preserve lifecycle events
already accepted by an active subscriber before the subscriber's closure
barrier.

#### Scenario: A terminal event and closure are both ready
- **WHEN** an active event iterator observes terminal closure with an event already queued before the closure barrier
- **THEN** it yields the queued event before returning
