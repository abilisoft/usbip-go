## ADDED Requirements

### Requirement: ListenAndServe reserves lifecycle before transport bind

The public `Exporter.ListenAndServe` path SHALL acquire the authoritative
internal Serve reservation before invoking `Transport.Listen`, and the listener
setup SHALL receive a context that Exporter Shutdown can cancel.

#### Scenario: Exporter is already shut down
- **WHEN** ListenAndServe is invoked after Shutdown
- **THEN** it returns the terminal Exporter sentinel
- **AND** `Transport.Listen` is not called

#### Scenario: Another Serve call is active
- **WHEN** ListenAndServe is invoked while another Serve call owns the reservation
- **THEN** it returns the overlapping-Serve sentinel
- **AND** `Transport.Listen` is not called

#### Scenario: Shutdown begins during transport bind
- **WHEN** `Transport.Listen` is waiting on the supplied context and Shutdown begins
- **THEN** that context is cancelled
- **AND** no listener remains installed after the Serve call exits

#### Scenario: Transport bind succeeds
- **WHEN** the lifecycle reservation succeeds and `Transport.Listen` returns a listener
- **THEN** the listener is installed for Shutdown and accept-loop ownership
- **AND** it is closed before ListenAndServe returns
