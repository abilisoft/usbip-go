# PortReconnectExhaustedEvent carries last-known Port snapshot

When the importer's reconnect watcher exhausts `MaxAttempts`, it emits
`PortReconnectExhaustedEvent` through `Importer.Watch`. The event embeds
the full last-known `domain.Port` value, alongside `Attempts int` and
`LastError string`.

```go
type PortReconnectExhaustedEvent struct {
    At        time.Time
    Port      Port
    Attempts  int
    LastError string
}
```

The disaggregated alternative — `PortID + Remote + BusID` — was rejected.
Although the watcher technically only knows `(portID, busID, remote)` at
exhaustion time (the kernel slot is gone, sysfs cannot be read), embedding
the full `Port` snapshot keeps the event consistent with the existing
`PortDetachedEvent{Port Port, Reason string}` shape. Consumers should not
need to learn a different access pattern for two events that both signal
"port is no longer viable".

Implementation: the importer's internal `portHandle` gains a
`lastKnownPort domain.Port` field, set in `finishAttach` from the same
`Port` value that is returned to the caller, and updated in
`finishReconnectSuccess` when a reattach lands. At exhaustion the watcher
reads the snapshot under the importer's read lock and emits the event.

`LastError string` is the stringified form of the final attempt's error.
The domain layer does not import or carry `error` values: `error` is an
infrastructure interface (typically wrapping syscalls), and domain events
must be value types that can serialize to JSON without losing
information. The application layer stringifies at the boundary.

`Attempts int` carries the actual attempts made (loop post-exit counter
minus one), not `MaxAttempts`. Distinguishing "exhausted after 1 attempt"
from "exhausted after 50" enables consumer policy decisions (alert
escalation, auto-rebind triggers) without requiring a sidecar metric
lookup.
