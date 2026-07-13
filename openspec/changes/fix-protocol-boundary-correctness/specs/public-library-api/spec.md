## MODIFIED Requirements

### Requirement: Backoff strategies are pluggable

The public API SHALL expose `BackoffStrategy`, `FixedBackoff`, `ExponentialBackoff`, and an explicitly panic-named `MustNewExponentialBackoff` constructor for reconnect timing. Exponential Min and Max SHALL remain non-negative for v1 compatibility, and every jittered exponential delay SHALL remain no greater than Max without duration overflow. The v1 `NewExponentialBackoff` name remains as a deprecated compatibility alias.

#### Scenario: Exponential backoff is constructed

- **WHEN** `MustNewExponentialBackoff` receives negative Min or Max, Max below Min, or invalid Jitter values
- **THEN** construction panics because invalid backoff configuration is a programmer error

#### Scenario: Exponential backoff validation is fallible

- **WHEN** a caller invokes `ExponentialBackoffConfig.Validate` before construction
- **THEN** invalid configuration returns `ErrExponentialBackoffConfigInvalid` without panicking

#### Scenario: Jitter samples above the capped base

- **WHEN** an exponential delay has reached Max and positive jitter would produce a larger or unrepresentable duration
- **THEN** `Next` returns Max

#### Scenario: Zero bounds retain v1 behavior

- **WHEN** Min is zero with a non-negative Max, including an all-zero bound pair
- **THEN** validation and construction succeed
- **AND** `Next` retains the historical zero-delay schedule

#### Scenario: Custom backoff is supplied

- **WHEN** a caller passes a custom BackoffStrategy implementation
- **THEN** the facade adapts it to the internal reconnect machinery without exposing internal types

#### Scenario: Fixed zero-delay backoff is supplied

- **WHEN** a caller explicitly supplies `FixedBackoff{Delay: 0}`
- **THEN** the fixed strategy retains its deterministic immediate-retry behavior
