## MODIFIED Requirements

### Requirement: watch streams domain events until interrupted

`usbip-go watch` SHALL subscribe to importer-visible events and render them continuously until SIGINT/SIGTERM cancels the command. Failure to establish the subscription or unexpected loss of the established event source SHALL terminate the command with an error instead of a successful empty stream.

#### Scenario: JSON watch output is selected

- **WHEN** `usbip-go watch --output=json` receives events
- **THEN** it emits one schema-versioned JSON object per line

#### Scenario: Watch subscription fails

- **WHEN** the KernelEvents subscription cannot be established
- **THEN** `usbip-go watch` returns a non-zero runtime error

#### Scenario: Watch event source closes unexpectedly

- **WHEN** the established KernelEvents source closes before caller cancellation
- **THEN** `usbip-go watch` returns a non-zero runtime error

#### Scenario: Watch is interrupted

- **WHEN** SIGINT or SIGTERM cancels the watch context
- **THEN** the command exits cleanly without classifying cancellation as a source failure

## ADDED Requirements

### Requirement: Human tables are safe for terminals

Human table output SHALL escape control code points in dynamic cell content before applying project-owned styling. JSON output SHALL remain schema-compatible and use standard JSON string escaping.

#### Scenario: Dynamic table cell contains an escape sequence

- **WHEN** a Device or peer supplies terminal-control bytes in a displayed string
- **THEN** the table renders visible escaped text
- **AND** the bytes cannot alter terminal state
