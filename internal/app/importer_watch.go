// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"log/slog"
	"sync"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// importerEventBufSize bounds each Watch subscriber's buffered channel
// for app-synthesized events. Slow consumers drop events (logged at
// debug) rather than back-pressuring the reconnect watcher; the upstream
// kernel-events buffer is sized by the kernel adapter independently.
const importerEventBufSize = 16

// importerEventSubscriber is one active Watch consumer's buffer for
// app-synthesized events. done is closed exactly once by
// removeImporterSubscriber or closeAllImporterSubscribers. ch is never
// closed from unsubscribe paths to avoid racing publishImporterEvent
// sends.
type importerEventSubscriber struct {
	ch   chan domain.Event
	done chan struct{}

	mu     sync.Mutex
	closed bool
}

func (s *importerEventSubscriber) closeDone() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closeDoneLocked()
}

// closeDoneLocked publishes the terminal barrier. The caller must hold s.mu.
func (s *importerEventSubscriber) closeDoneLocked() {
	if s.closed {
		return
	}

	// Closing done while holding the same mutex used by tryPublish is a
	// publication barrier: once a receiver observes done, no sender can add a
	// later event to ch. The iterator can therefore drain the bounded buffer
	// deterministically before it returns.
	s.closed = true
	close(s.done)
}

func (s *importerEventSubscriber) tryPublish(ev domain.Event) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.tryPublishLocked(ev)
}

// tryPublishLocked publishes one event before the terminal barrier. The caller
// must hold s.mu.
func (s *importerEventSubscriber) tryPublishLocked(ev domain.Event) (bool, bool) {
	if s.closed {
		return false, false
	}

	select {
	case s.ch <- ev:
		return true, true
	default:
		return true, false
	}
}

// publishImporterEvent fans ev out to every live Watch subscriber. Slow
// consumers drop the event (logged at debug) so a stuck consumer cannot
// stall the reconnect watcher. tryPublish serializes publication with the
// subscriber's close barrier so a concurrent removal cannot enqueue after the
// iterator observes done.
func (i *Importer) publishImporterEvent(ev domain.Event) {
	i.mu.RLock()

	subs := make([]*importerEventSubscriber, len(i.subscribers))
	copy(subs, i.subscribers)

	i.mu.RUnlock()

	for _, sub := range subs {
		active, sent := sub.tryPublish(ev)
		if active && !sent {
			i.logger.Debug("importer event dropped (slow consumer)",
				slog.Any("event", ev))
		}
	}
}

// removeImporterSubscriber drops sub from the list and signals the
// iterator via sub.done. The event channel is NOT closed here: a
// concurrent publish-side send would otherwise panic on a closed
// channel. The shared subscriber mutex makes publication and unsubscribe
// race-safe.
func (i *Importer) removeImporterSubscriber(sub *importerEventSubscriber) {
	i.mu.Lock()

	for idx, s := range i.subscribers {
		if s == sub {
			i.subscribers = append(i.subscribers[:idx], i.subscribers[idx+1:]...)

			break
		}
	}

	i.mu.Unlock()

	sub.closeDone()
}

// closeAllImporterSubscribers drops every remaining subscriber and
// signals them via done. Called by Close so Watch iters exit when the
// Importer shuts down.
func (i *Importer) closeAllImporterSubscribers() {
	i.mu.Lock()

	subs := i.subscribers

	i.subscribers = nil

	i.mu.Unlock()

	for _, sub := range subs {
		sub.closeDone()
	}
}

type mergedSelectResult uint8

const (
	mergedSelectStop mergedSelectResult = iota
	mergedSelectContinue
	mergedSelectSourceClosed
)

// runImporterMergedSeq yields events to the supplied yield function from
// either the upstream KernelEvents channel or the per-call subscriber
// channel. A kernel-channel close is classified after the select so caller
// cancellation or Importer.Close racing that close remains a clean stop.
//
// On exit the kernel-side cancel is called and the subscriber is
// removed from the Importer fanout list.
//
// Ordering and cancellation semantics:
//
// Ordering between the two source channels is non-deterministic by
// design — Go's select picks pseudo-randomly among
// ready cases. The same rule means cancellation is cooperative, not
// instantaneous: if ctx.Done() and an event channel are simultaneously
// ready, the iterator may yield ONE final event before terminating.
// Consumers that require strict "no events after cancel" semantics
// MUST gate downstream effects on their own ctx.Err() check rather
// than rely on the iterator stopping at the first opportunity.
func (i *Importer) runImporterMergedSeq(
	ctx context.Context,
	kernelCh <-chan domain.Event,
	kernelCancel func(),
	sub *importerEventSubscriber,
	yield func(domain.Event, error) bool,
) {
	defer kernelCancel()
	defer i.removeImporterSubscriber(sub)

	for {
		switch mergedSelectOnce(ctx, kernelCh, sub, yield) {
		case mergedSelectStop:
		case mergedSelectContinue:
			continue
		case mergedSelectSourceClosed:
			if !i.watchStopped(ctx, sub) {
				_ = yield(nil, ErrEventStreamClosed)
			}
		}

		return
	}
}

// watchStopped reports whether a terminal source condition coincided with a
// caller- or Importer-owned shutdown. Checking sub.done as well as closed
// makes the close classification deterministic even while Close is between
// signalling subscribers and publishing its final state.
func (i *Importer) watchStopped(ctx context.Context, sub *importerEventSubscriber) bool {
	if ctx.Err() != nil {
		return true
	}

	select {
	case <-sub.done:
		return true
	default:
	}

	i.mu.RLock()

	closed := i.closed
	i.mu.RUnlock()

	return closed
}

// mergedSelectOnce performs one select over both termination signals and
// event sources, returning whether the caller should continue, stop cleanly,
// or classify an upstream close.
func mergedSelectOnce(
	ctx context.Context,
	kernelCh <-chan domain.Event,
	sub *importerEventSubscriber,
	yield func(domain.Event, error) bool,
) mergedSelectResult {
	select {
	case <-ctx.Done():
		return mergedSelectStop
	case <-sub.done:
		drainImporterSubscriber(sub, yield)

		return mergedSelectStop
	case ev, ok := <-kernelCh:
		if !ok {
			return mergedSelectSourceClosed
		}

		return mergedEventResult(yield(ev, nil))
	case ev := <-sub.ch:
		return mergedEventResult(yield(ev, nil))
	}
}

// drainImporterSubscriber yields every app-synthesized event that was queued
// before closeDone's publication barrier. Caller cancellation deliberately
// does not use this path: the existing cooperative-cancellation contract still
// permits cancellation to stop without draining buffered events.
func drainImporterSubscriber(
	sub *importerEventSubscriber,
	yield func(domain.Event, error) bool,
) {
	for {
		select {
		case ev := <-sub.ch:
			if !yield(ev, nil) {
				return
			}
		default:
			return
		}
	}
}

func mergedEventResult(keepGoing bool) mergedSelectResult {
	if keepGoing {
		return mergedSelectContinue
	}

	return mergedSelectStop
}
