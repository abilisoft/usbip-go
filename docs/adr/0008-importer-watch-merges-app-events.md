# Importer.Watch merges kernel and app-synthesized port events

`Importer.Watch` returns one `iter.Seq[domain.Event]` that yields BOTH
kernel-sourced port events (`PortAttached`, `PortDetached`, `PortErrored`)
AND app-synthesized port events (`PortReconnectExhausted`). The split
between `Watch` and `WatchSessions` is by **domain concern**, not by event
source.

The alternative was a separate `WatchImporterEvents` method mirroring the
`WatchSessions` pattern: kernel events on `Watch`, app events on a new
endpoint, consumer subscribes to both. It was rejected because port
lifecycle is one bounded-context concern. `PortDetachedEvent` and
`PortReconnectExhaustedEvent` both answer "what happened to this port?";
splitting them by producer technology leaks an implementation detail
(uevent vs reconnect goroutine) into the public API and forces every
consumer to fan in two iterators to track a single domain concept.

`WatchSessions` stays separate because Sessions are a different
exporter-side accounting concern (UUIDv7-keyed, byte counters), not the
same lifecycle as device-bind state.

Implementation: `Importer` gains an internal subscriber list and a
non-blocking `publishImporterEvent` fanout, mirroring the Exporter's
session-event fanout. Each `Watch` call subscribes a buffered channel and
runs a `select` loop that reads from both the `KernelEvents` subscription
and the per-call importer-event channel. Slow consumers drop events
(logged at debug) — the reconnect watcher and accept loop must not block
on a stuck subscriber.

Ordering between the two sources is deliberately unspecified: the iterator
yields events in the order they arrive at the merge point, which is
non-deterministic between concurrent kernel uevent and app emission. Tests
assert eventual delivery, not interleaving.
