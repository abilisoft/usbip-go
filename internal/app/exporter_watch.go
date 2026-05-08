package app

import (
	"context"
	"iter"
	"log/slog"
	"sort"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// sessionEventBufSize bounds each WatchSessions subscriber's buffered
// channel. Slow consumers drop events (logged) rather than back-
// pressuring the session state machine; matches the KernelEvents
// fan-out semantics in spec §5.1.
const sessionEventBufSize = 16

// sessionEventSubscriber is one active WatchSessions consumer. ch is
// closed exactly once when the subscriber is removed (ctx cancel or
// Shutdown).
type sessionEventSubscriber struct {
	ch chan domain.Event
}

// Sessions returns a snapshot of the currently-accepted sessions,
// sorted by start time so callers paginating the list see a stable
// order between calls. ctx is accepted for interface symmetry with
// other list methods; it is not currently used — the snapshot is an
// in-memory copy under the Exporter's read lock.
func (e *Exporter) Sessions(_ context.Context) []domain.Session {
	e.mu.RLock()

	out := make([]domain.Session, 0, len(e.sessions))
	for _, h := range e.sessions {
		out = append(out, h.session)
	}

	e.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.Before(out[j].StartedAt)
	})

	return out
}

// WatchSessions returns an iter.Seq that yields every future
// SessionStartedEvent and SessionEndedEvent while ctx is live. The
// subscription is registered on call; canceling ctx (or yield
// returning false) removes the subscriber. Post-Shutdown the iter
// terminates immediately with no events — matches Importer.Watch's
// post-Close semantics per spec §3.4.
func (e *Exporter) WatchSessions(ctx context.Context) iter.Seq[domain.Event] {
	e.mu.Lock()

	if e.shutdown {
		e.mu.Unlock()

		return emptyEventSeq
	}

	sub := &sessionEventSubscriber{
		ch: make(chan domain.Event, sessionEventBufSize),
	}

	e.subscribers = append(e.subscribers, sub)

	e.mu.Unlock()

	remove := func() {
		e.removeSubscriber(sub)
	}

	return newEventSeq(ctx, sub.ch, remove)
}

// removeSubscriber drops sub from the subscriber list and closes its
// channel exactly once. The close-once semantics are enforced by the
// presence check: a subscriber only appears in the slice once, so the
// second remove call finds nothing to close.
func (e *Exporter) removeSubscriber(sub *sessionEventSubscriber) {
	e.mu.Lock()

	found := false

	for i, s := range e.subscribers {
		if s == sub {
			e.subscribers = append(e.subscribers[:i], e.subscribers[i+1:]...)
			found = true

			break
		}
	}

	e.mu.Unlock()

	if found {
		close(sub.ch)
	}
}

// publishSessionEvent fans ev out to every live WatchSessions
// subscriber. Slow consumers drop the event (logged) so one stuck
// watcher cannot stall the session state machine.
func (e *Exporter) publishSessionEvent(ev domain.Event) {
	e.mu.RLock()

	// Copy the slice under the read lock so publish runs without
	// holding it; a concurrent removeSubscriber can then close the
	// channel safely without racing a blocking send.
	subs := make([]*sessionEventSubscriber, len(e.subscribers))
	copy(subs, e.subscribers)

	e.mu.RUnlock()

	for _, sub := range subs {
		select {
		case sub.ch <- ev:
		default:
			e.logger.Debug("exporter session event dropped (slow consumer)",
				slog.Any("event", ev))
		}
	}
}

// closeAllSubscribers drops every remaining subscriber and closes
// their channels. Called by Shutdown so WatchSessions iters exit
// after drain. Each removeSubscriber handles its own closeOnce
// guarantee via the slice lookup.
func (e *Exporter) closeAllSubscribers() {
	e.mu.Lock()

	subs := e.subscribers

	e.subscribers = nil

	e.mu.Unlock()

	for _, sub := range subs {
		close(sub.ch)
	}
}
