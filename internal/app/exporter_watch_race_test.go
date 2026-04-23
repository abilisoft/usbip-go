package app_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestExporterWatchSessions_PublishVsUnsubscribeRace hammers
// subscribe/unsubscribe concurrently with the session-event publish
// path. Subscribers use a done signal so publishSessionEvent can bail
// out without closing the event channel; otherwise removeSubscriber
// closing sub.ch while publishSessionEvent is mid-send would produce a
// send-on-closed-channel panic and a race-detector hit.
func TestExporterWatchSessions_PublishVsUnsubscribeRace(t *testing.T) {
	t.Parallel()

	exp := newExporterForTest(t)

	t.Cleanup(func() {
		require.NoError(t, exp.Shutdown(context.Background()))
	})

	stop := make(chan struct{})

	var wg sync.WaitGroup

	// Publisher: continuously fans a SessionStartedEvent out while
	// subscribers churn in and out.
	wg.Go(func() {
		ev := domain.SessionStartedEvent{At: time.Now()}

		for {
			select {
			case <-stop:
				return
			default:
			}

			app.PublishSessionEventForTest(exp, ev)
		}
	})

	const iterations = 500

	for range iterations {
		ctx, cancel := context.WithCancel(context.Background())

		wg.Go(func() {
			defer cancel()

			// Subscribe and consume one event (or none) before unsubscribing.
			for range exp.WatchSessions(ctx) {
				break
			}
		})

		// Cancel quickly to force overlap with the publisher.
		cancel()
	}

	close(stop)
	wg.Wait()
}
