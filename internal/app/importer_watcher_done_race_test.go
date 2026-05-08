package app_test

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestImporterAttachDetachWatcherDoneRaceFree hammers concurrent
// Attach + Detach pairs with AutoReconnect=true under the race
// detector. The historical ordering wrote h.watcherDone AFTER
// registerHandle had already published h into the handle map; a
// Detach racing the narrow window between that publication and the
// subsequent assignment triggered a -race report because the
// watcher-channel field was read under mu while being written
// without any happens-before relationship. Each iteration launches
// Attach and Detach on separate goroutines targeting the same
// pre-known port id so the Detach can fire BEFORE Attach has
// written the channel; 500 iterations with GOMAXPROCS>1 is enough
// for the detector to fire in practice pre-fix. Post-fix
// watcherDone is assigned under mu inside registerHandle, removing
// the race mechanically.
func TestImporterAttachDetachWatcherDoneRaceFree(t *testing.T) {
	t.Parallel()

	var nextID atomic.Uint32

	imp, _, _, kernel := newReconnectFixture(t,
		func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return domain.PortID(nextID.Add(1)), nil
		})
	t.Cleanup(func() { _ = imp.Close() })

	kernel.DetachPortFunc = func(_ context.Context, _ domain.PortID) error {
		return nil
	}

	const iterations = 500

	for range iterations {
		// Pre-compute the port id this iteration's AttachRemote will
		// return so the Detach goroutine can race the publication.
		targetID := domain.PortID(nextID.Load() + 1)

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			_, _ = imp.Attach(context.Background(), testRemote(), attachBusID(),
				attachOptionsWithBackoff())
		}()

		go func(id domain.PortID) {
			defer wg.Done()

			_ = imp.Detach(context.Background(), id)
		}(targetID)

		wg.Wait()
	}
}
