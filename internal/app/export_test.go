// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"net"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// ResolveAcceptRateForTest applies exporter options and returns the effective
// accept rate after omitted-default resolution.
func ResolveAcceptRateForTest(opts ...ExporterOption) float64 {
	cfg := exporterConfig{}

	for _, opt := range opts {
		opt(&cfg)
	}

	return resolveAcceptRate(&cfg)
}

// DefaultAcceptRateLimitForTest exposes the canonical omitted-option default
// to black-box tests without duplicating the production value.
func DefaultAcceptRateLimitForTest() float64 {
	return defaultAcceptRateLimit
}

// WithSessionIDGeneratorForTest injects deterministic SessionID generation
// failures into black-box exporter handshake tests.
func WithSessionIDGeneratorForTest(
	generate func() (domain.SessionID, error),
) ExporterOption {
	return func(c *exporterConfig) { c.newSessionID = generate }
}

// PublishSessionEventForTest exposes the internal session-event fan-out
// so race-detector tests can drive the publish path directly and
// reproduce the publish-vs-unsubscribe window.
func PublishSessionEventForTest(e *Exporter, ev domain.Event) {
	e.publishSessionEvent(ev)
}

// SubscriberBarrierResultForTest captures both sides of a deterministic
// publish-vs-close contention plus the events observed by the production
// iterator after the terminal barrier.
type SubscriberBarrierResultForTest struct {
	PublishActive   bool
	PublishSent     bool
	PostCloseActive bool
	PostCloseSent   bool
	Delivered       []domain.Event
}

// SubscriberOverflowResultForTest captures the bounded-buffer behavior of one
// importer subscriber without exposing the production subscriber type.
type SubscriberOverflowResultForTest struct {
	FirstActive    bool
	FirstSent      bool
	OverflowActive bool
	OverflowSent   bool
	Buffered       []domain.Event
}

// ExerciseImporterSubscriberOverflowForTest fills a capacity-one importer
// subscriber and attempts one additional publication. The second event must be
// dropped without closing or deactivating the subscriber.
func ExerciseImporterSubscriberOverflowForTest(
	first domain.Event,
	overflow domain.Event,
) SubscriberOverflowResultForTest {
	sub := &importerEventSubscriber{
		ch:   make(chan domain.Event, 1),
		done: make(chan struct{}),
	}

	firstActive, firstSent := sub.tryPublish(first)
	overflowActive, overflowSent := sub.tryPublish(overflow)

	buffered := make([]domain.Event, 0, 1)

	select {
	case event := <-sub.ch:
		buffered = append(buffered, event)
	default:
	}

	return SubscriberOverflowResultForTest{
		FirstActive:    firstActive,
		FirstSent:      firstSent,
		OverflowActive: overflowActive,
		OverflowSent:   overflowSent,
		Buffered:       buffered,
	}
}

// DrainImporterSubscriberForTest invokes the production terminal drain on a
// closed subscriber preloaded with events and returns the number left buffered
// after yield stops.
func DrainImporterSubscriberForTest(
	events []domain.Event,
	yield func(domain.Event, error) bool,
) int {
	sub := &importerEventSubscriber{
		ch:   make(chan domain.Event, len(events)),
		done: make(chan struct{}),
	}

	for _, event := range events {
		sub.ch <- event
	}

	sub.closeDone()
	drainImporterSubscriber(sub, yield)

	return len(sub.ch)
}

// DrainSessionSubscriberForTest is the WatchSessions counterpart of
// DrainImporterSubscriberForTest.
func DrainSessionSubscriberForTest(
	events []domain.Event,
	yield func(domain.Event) bool,
) int {
	sub := &sessionEventSubscriber{
		ch:   make(chan domain.Event, len(events)),
		done: make(chan struct{}),
	}

	for _, event := range events {
		sub.ch <- event
	}

	sub.closeDone()
	drainSessionEvents(sub, yield)

	return len(sub.ch)
}

// ExerciseImporterSubscriberBarrierForTest holds the subscriber mutex while a
// close goroutine begins contending for it, accepts one event in the production
// publication critical section, then releases closure and drains through the
// production iterator. A final tryPublish proves post-close sends are rejected.
func ExerciseImporterSubscriberBarrierForTest(
	accepted domain.Event,
	postClose domain.Event,
) SubscriberBarrierResultForTest {
	sub := &importerEventSubscriber{
		ch:   make(chan domain.Event, 1),
		done: make(chan struct{}),
	}

	sub.mu.Lock()
	closeStarted := make(chan struct{})
	closeFinished := make(chan struct{})

	go func() {
		close(closeStarted)
		sub.mu.Lock()
		sub.closeDoneLocked()
		sub.mu.Unlock()
		close(closeFinished)
	}()

	<-closeStarted

	publishActive, publishSent := sub.tryPublishLocked(accepted)
	sub.mu.Unlock()
	<-closeFinished

	postCloseActive, postCloseSent := sub.tryPublish(postClose)

	delivered := make([]domain.Event, 0, 1)
	imp := &Importer{}
	imp.runImporterMergedSeq(
		context.Background(),
		make(chan domain.Event),
		func() {},
		sub,
		func(event domain.Event, watchErr error) bool {
			if watchErr == nil {
				delivered = append(delivered, event)
			}

			return true
		},
	)

	return SubscriberBarrierResultForTest{
		PublishActive:   publishActive,
		PublishSent:     publishSent,
		PostCloseActive: postCloseActive,
		PostCloseSent:   postCloseSent,
		Delivered:       delivered,
	}
}

// ExerciseSessionSubscriberBarrierForTest is the WatchSessions counterpart of
// ExerciseImporterSubscriberBarrierForTest.
func ExerciseSessionSubscriberBarrierForTest(
	accepted domain.Event,
	postClose domain.Event,
) SubscriberBarrierResultForTest {
	sub := &sessionEventSubscriber{
		ch:   make(chan domain.Event, 1),
		done: make(chan struct{}),
	}

	sub.mu.Lock()
	closeStarted := make(chan struct{})
	closeFinished := make(chan struct{})

	go func() {
		close(closeStarted)
		sub.mu.Lock()
		sub.closeDoneLocked()
		sub.mu.Unlock()
		close(closeFinished)
	}()

	<-closeStarted

	publishActive, publishSent := sub.tryPublishLocked(accepted)
	sub.mu.Unlock()
	<-closeFinished

	postCloseActive, postCloseSent := sub.tryPublish(postClose)

	delivered := make([]domain.Event, 0, 1)

	runSessionEventSeq(context.Background(), sub, func() {}, func(event domain.Event) bool {
		delivered = append(delivered, event)

		return true
	})

	return SubscriberBarrierResultForTest{
		PublishActive:   publishActive,
		PublishSent:     publishSent,
		PostCloseActive: postCloseActive,
		PostCloseSent:   postCloseSent,
		Delivered:       delivered,
	}
}

// ArmHandshakeTimeoutForTest exposes the handshake-timeout arming helper
// so black-box tests can assert the "register timer before return"
// invariant directly. The returned stop func disarms the watcher and
// blocks until the spawned goroutine exits — callers MUST invoke it
// exactly once to avoid a test-scoped goroutine leak.
func ArmHandshakeTimeoutForTest(e *Exporter, conn net.Conn) func() {
	return e.armHandshakeTimeout(&connCloser{conn: conn})
}

// SessionSubscribersLenForTest reports the live count of WatchSessions
// fanout subscribers under the exporter's read lock. Black-box tests
// use it to pin the lazy-registration contract: a Watch caller that
// constructs the iter and discards it must not occupy a fanout slot.
func SessionSubscribersLenForTest(e *Exporter) int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return len(e.subscribers)
}

// ImporterSubscribersLenForTest is the Importer counterpart of
// SessionSubscribersLenForTest. Pins the same lazy-registration
// contract on Importer.Watch.
func ImporterSubscribersLenForTest(i *Importer) int {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return len(i.subscribers)
}
