## MODIFIED Requirements

### Requirement: detach and port operate on numeric port IDs

`usbip-go detach <port>` SHALL detach a live Port by ID even when the Attachment was created by a preceding one-shot process, and `usbip-go port --id N` SHALL filter port output to one active Port.

#### Scenario: Port filter misses

- **WHEN** `usbip-go port --id N` is supplied for a non-attached Port
- **THEN** the command returns a not-found classified error

#### Scenario: One-shot attach is detached later

- **WHEN** `usbip-go attach HOST BUSID` succeeds and exits, then a later `usbip-go detach N` process targets its acknowledged PortID
- **THEN** the later process detaches the live kernel Port

#### Scenario: Fresh process inspects a Port

- **WHEN** `usbip-go port --id N` runs after the attach process exited
- **THEN** it reports the live Port status and importer-local busid
- **AND** unavailable remote endpoint and busid fields remain empty
