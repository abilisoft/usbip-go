// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"iter"
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
	ch       chan domain.Event
	done     chan struct{}
	doneOnce sync.Once
}

func (s *importerEventSubscriber) closeDone() {
	s.doneOnce.Do(func() { close(s.done) })
}

// publishImporterEvent fans ev out to every live Watch subscriber. Slow
// consumers drop the event (logged at debug) so a stuck consumer cannot
// stall the reconnect watcher. Sends are guarded by a select on done so
// a concurrent removeImporterSubscriber is observed as "subscriber
// gone" instead of racing the channel close.
func (i *Importer) publishImporterEvent(ev domain.Event) {
	i.mu.RLock()

	subs := make([]*importerEventSubscriber, len(i.subscribers))
	copy(subs, i.subscribers)

	i.mu.RUnlock()

	for _, sub := range subs {
		select {
		case <-sub.done:
		case sub.ch <- ev:
		default:
			i.logger.Debug("importer event dropped (slow consumer)",
				slog.Any("event", ev))
		}
	}
}

// removeImporterSubscriber drops sub from the list and signals the
// iterator via sub.done. The event channel is NOT closed here: a
// concurrent publish-side send would otherwise panic on a closed
// channel. The publish path's select on done makes the unsubscribe race
// safe.
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

// newImporterMergedSeq returns an iter.Seq that yields events from
// either the upstream KernelEvents channel or the per-call subscriber
// channel, whichever delivers next. Iteration ends when:
//   - ctx is cancelled, or
//   - the kernel channel is closed (terminal), or
//   - sub.done is signalled (Importer.Close), or
//   - yield returns false.
//
// On exit the kernel-side cancel is called and the subscriber is
// removed from the Importer fanout list. Ordering between the two
// sources is non-deterministic by design (see ADR-0008).
func (i *Importer) newImporterMergedSeq(
	ctx context.Context,
	kernelCh <-chan domain.Event,
	kernelCancel func(),
	sub *importerEventSubscriber,
) iter.Seq[domain.Event] {
	return func(yield func(domain.Event) bool) {
		defer kernelCancel()
		defer i.removeImporterSubscriber(sub)

		for mergedSelectOnce(ctx, kernelCh, sub, yield) {
			// loop until mergedSelectOnce reports termination
		}
	}
}

// mergedSelectOnce performs a single select over the four termination
// signals and the two event sources. Returns true to continue the
// outer loop, false to terminate. Extracted so newImporterMergedSeq
// stays under the gocognit threshold.
func mergedSelectOnce(
	ctx context.Context,
	kernelCh <-chan domain.Event,
	sub *importerEventSubscriber,
	yield func(domain.Event) bool,
) bool {
	select {
	case <-ctx.Done():
		return false
	case <-sub.done:
		return false
	case ev, ok := <-kernelCh:
		if !ok {
			return false
		}

		return yield(ev)
	case ev := <-sub.ch:
		return yield(ev)
	}
}
