## ADDED Requirements

### Requirement: Exporter terminal session events are drained

Exporter subscriber closure SHALL establish a publication barrier and an active
WatchSessions iterator SHALL drain every lifecycle event accepted into its
bounded subscriber buffer before terminal Exporter shutdown.

#### Scenario: Session end is queued before shutdown closes subscribers
- **WHEN** a session-ended event has entered a subscriber buffer before shutdown closes that subscriber
- **THEN** the active iterator yields the session-ended event before returning

#### Scenario: WatchSessions caller cancels independently
- **WHEN** the WatchSessions context is cancelled independently of Exporter shutdown
- **THEN** the iterator stops without a terminal-buffer drain requirement

### Requirement: Serving lifecycle is reserved before listener setup

The Exporter SHALL atomically reserve one Serve lifecycle before invoking a
listener factory, SHALL reject terminal or overlapping calls before factory
side effects, and SHALL let Shutdown cancel an in-flight context-aware factory.

#### Scenario: Listener setup overlaps Shutdown
- **WHEN** Shutdown begins while a listener factory is waiting on its supplied context
- **THEN** the factory context is cancelled
- **AND** Shutdown waits for the reserved Serve call to leave setup

#### Scenario: Concurrent Serve already owns the reservation
- **WHEN** a second Serve operation begins before the first Serve operation exits
- **THEN** the second operation returns `ErrServeAlreadyRunning`
- **AND** its listener factory is not invoked

### Requirement: Accept-rate option has explicit disable semantics

Exporter construction SHALL apply the default accept rate only when the option
is omitted, SHALL treat an explicit finite rate less than or equal to zero as
disabled, and SHALL reject a non-finite rate.

#### Scenario: Explicit zero differs from omission
- **WHEN** one Exporter omits the rate option and another explicitly supplies zero
- **THEN** the omitted option receives the documented default limiter
- **AND** the explicit zero Exporter has no accept-rate limiter

#### Scenario: Non-finite rate is configured
- **WHEN** NaN or either infinity is supplied as the accept rate
- **THEN** Exporter construction fails with the accept-rate-invalid sentinel
