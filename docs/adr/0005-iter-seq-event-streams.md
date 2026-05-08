# iter.Seq[Event] for Watch and WatchSessions

`Watch` and `WatchSessions` return `iter.Seq[domain.Event]` (Go 1.23
push iterators) rather than `chan domain.Event`.

Channel-based streaming was the original design. It was replaced
because channels force callers to own teardown (drain the channel,
track context cancellation separately, avoid goroutine leaks from slow
consumers). `iter.Seq` composes naturally with `for range` and
terminates automatically when the caller breaks or the context is
cancelled — no extra goroutine ownership on the consumer side.

The trade-off is a hard Go 1.23 minimum and a less familiar API for
engineers who have not yet encountered push iterators. The minimum was
already set by other 1.23 features in the project; the iterator
pattern was considered worth the learning curve given it eliminates an
entire class of goroutine-leak bugs in calling code.

Slow consumers drop events (logged at warn level) rather than
back-pressuring the fan-out. This is deliberate: a stalled consumer
must not affect the daemon's accept loop or other subscribers.
