// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"iter"
	"log/slog"
	"sort"
	"sync"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// sessionEventBufSize bounds each WatchSessions subscriber's buffered
// channel. Slow consumers drop events (logged) rather than back-
// pressuring the session state machine; matches the KernelEvents
// fan-out semantics in spec §5.1.
const sessionEventBufSize = 16

// sessionEventSubscriber is one active WatchSessions consumer. done is
// closed exactly once by removeSubscriber or closeAllSubscribers to
// signal the iterator that no more events will arrive. ch is
// deliberately NOT closed from unsubscribe paths — the publish-side
// select on ch vs done would otherwise race a removeSubscriber close
// and panic with send-on-closed-channel.
type sessionEventSubscriber struct {
	ch       chan domain.Event
	done     chan struct{}
	doneOnce sync.Once
}

// closeDone signals the subscriber as removed exactly once. Safe to
// call from any goroutine.
func (s *sessionEventSubscriber) closeDone() {
	s.doneOnce.Do(func() { close(s.done) })
}

// Sessions returns a snapshot of the currently-accepted sessions,
// sorted by start time so callers paginating the list see a stable
// order between calls. ctx is accepted for interface symmetry with
// other list methods; it is not currently used — the snapshot is an
// in-memory copy under the Exporter's read lock. Equal StartedAt
// values tiebreak by SessionID string form: UUIDv7 is lexical-time-
// ordered so the secondary key stays meaningful without additional
// state.
func (e *Exporter) Sessions(_ context.Context) []domain.Session {
	e.mu.RLock()

	out := make([]domain.Session, 0, len(e.sessions))
	for _, h := range e.sessions {
		out = append(out, h.session)
	}

	e.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID.String() < out[j].ID.String()
		}

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
		ch:   make(chan domain.Event, sessionEventBufSize),
		done: make(chan struct{}),
	}

	e.subscribers = append(e.subscribers, sub)

	e.mu.Unlock()

	remove := func() {
		e.removeSubscriber(sub)
	}

	return newSessionEventSeq(ctx, sub, remove)
}

// removeSubscriber drops sub from the subscriber list and signals the
// iterator via sub.done. The event channel is NOT closed here: closing
// it would race a concurrent publishSessionEvent send. Instead the
// publish path selects on sub.done and skips subscribers that have
// unsubscribed.
func (e *Exporter) removeSubscriber(sub *sessionEventSubscriber) {
	e.mu.Lock()

	for i, s := range e.subscribers {
		if s == sub {
			e.subscribers = append(e.subscribers[:i], e.subscribers[i+1:]...)

			break
		}
	}

	e.mu.Unlock()

	sub.closeDone()
}

// publishSessionEvent fans ev out to every live WatchSessions
// subscriber. Slow consumers drop the event (logged) so one stuck
// watcher cannot stall the session state machine. Sends are guarded
// by a select on sub.done so a concurrent removeSubscriber is observed
// as "subscriber gone" instead of racing the channel close.
func (e *Exporter) publishSessionEvent(ev domain.Event) {
	e.mu.RLock()

	// Copy the slice under the read lock so publish runs without
	// holding it; concurrent subscribe/unsubscribe churn then cannot
	// block the session state machine.
	subs := make([]*sessionEventSubscriber, len(e.subscribers))
	copy(subs, e.subscribers)

	e.mu.RUnlock()

	for _, sub := range subs {
		select {
		case <-sub.done:
			// Subscriber already torn down; skip without logging.
		case sub.ch <- ev:
		default:
			e.logger.Debug("exporter session event dropped (slow consumer)",
				slog.Any("event", ev))
		}
	}
}

// closeAllSubscribers drops every remaining subscriber and signals
// them via done. Called by Shutdown so WatchSessions iters exit after
// drain. The event channels stay open for the lifetime of the
// Exporter — the iterator loop terminates on sub.done independently.
func (e *Exporter) closeAllSubscribers() {
	e.mu.Lock()

	subs := e.subscribers

	e.subscribers = nil

	e.mu.Unlock()

	for _, sub := range subs {
		sub.closeDone()
	}
}

// newSessionEventSeq returns an iter.Seq that yields events from sub.ch
// until ctx is cancelled, sub.done fires, or yield returns false. The
// remove callback is invoked on exit so the subscriber is dropped from
// the Exporter's list. Unlike newEventSeq (used by the Importer), this
// variant terminates on sub.done rather than on channel close — the
// publish path never closes sub.ch to avoid the send-on-closed race.
func newSessionEventSeq(
	ctx context.Context,
	sub *sessionEventSubscriber,
	remove func(),
) iter.Seq[domain.Event] {
	return func(yield func(domain.Event) bool) {
		defer remove()

		for {
			select {
			case <-ctx.Done():
				return
			case <-sub.done:
				return
			case ev := <-sub.ch:
				if !yield(ev) {
					return
				}
			}
		}
	}
}
