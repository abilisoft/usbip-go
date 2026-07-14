## ADDED Requirements

### Requirement: Importer terminal event publication is drained

Importer subscriber closure SHALL establish a publication barrier and an active
watch iterator SHALL drain every application event accepted into its bounded
subscriber buffer before terminal Importer closure.

#### Scenario: Reconnect exhaustion is queued before Importer closure
- **WHEN** a reconnect-exhausted event has entered a subscriber buffer before `Importer.Close` closes that subscriber
- **THEN** the active iterator yields the reconnect-exhausted event before returning

#### Scenario: Caller cancels event watching
- **WHEN** the Watch context is cancelled independently of Importer closure
- **THEN** the iterator stops without a terminal-buffer drain requirement

### Requirement: Backoff factory state follows one logical Attachment

An auto-reconnecting Attach SHALL construct importer-level backoff-factory state
at most once after terminal-state, argument-validation, and duplicate-Attachment
checks pass but before kernel or network work begins. Successful reconnect
replacement generations SHALL retain that same strategy.

#### Scenario: Attach is rejected before reservation
- **WHEN** Attach is rejected because the Importer is closed or its arguments are invalid
- **THEN** its configured backoff factory is not invoked

#### Scenario: Reconnect creates a replacement generation
- **WHEN** an Attachment successfully reconnects and starts a replacement watcher
- **THEN** the replacement watcher uses the original logical Attachment's strategy
- **AND** the factory invocation count remains one

### Requirement: Closed Importer state precedes Attach validation

After Close, Importer Attach SHALL return `ErrImporterClosed` before validating
the endpoint, BusID, or AttachOptions, while the locked attachment reservation
SHALL recheck closure against a racing Close.

#### Scenario: Closed Attach receives malformed inputs
- **WHEN** Attach is called after Close with a malformed endpoint, BusID, or negative MaxAttempts
- **THEN** it returns `ErrImporterClosed`
- **AND** no backoff factory, kernel, or network operation runs
