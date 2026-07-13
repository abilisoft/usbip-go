## ADDED Requirements

### Requirement: Coverage evidence fails closed

The repository coverage gate SHALL reject an LCOV report whose aggregate contains no executable lines after configured exclusions are applied. An aggregate zero denominator MUST be treated as missing evidence, not as 100 percent coverage. A zero-line package record in an otherwise measurable report MUST be reported as not coverable and excluded from per-package threshold evaluation rather than assigned an invented percentage.

#### Scenario: Coverage report is empty

- **WHEN** the coverage checker receives an empty report or a report with no included `LF` records
- **THEN** it exits non-zero with an actionable missing-coverage error
- **AND** it does not print `0/0` as 100 percent coverage

#### Scenario: Zero-line and measured packages are mixed

- **WHEN** an LCOV report contains an included `LF:0` package and another included package whose measured lines satisfy the configured thresholds
- **THEN** the gate succeeds using the nonzero measured aggregate and package evidence
- **AND** it reports the zero-line package as not coverable without evaluating a package threshold or printing a `0/0` percentage

### Requirement: Human terminal output neutralizes untrusted controls

Human-oriented CLI rendering SHALL escape control code points from Device- or peer-controlled text before terminal styling is applied. Printable Unicode SHALL remain readable, and machine-readable JSON SHALL continue using JSON escaping without display-only mutation.

#### Scenario: Device descriptor contains terminal controls

- **WHEN** a Device manufacturer or product contains C0, DEL, C1, OSC, or CSI control bytes
- **THEN** table output contains a visible escaped representation
- **AND** no untrusted executable terminal control reaches the output writer

#### Scenario: Device descriptor contains printable Unicode

- **WHEN** a Device manufacturer or product contains printable Unicode text
- **THEN** human output preserves that text
