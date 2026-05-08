package app_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestExporter_ArmHandshakeTimeoutRegistersTimerBeforeReturn asserts
// armHandshakeTimeout registers its FakeClock.After deadline
// SYNCHRONOUSLY on the caller goroutine, not from the spawned watcher.
// The invariant matters because test code routinely calls
// clk.Advance(handshakeTimeout + …) immediately after the handshake
// starts; if the timer were registered from the watcher goroutine, the
// Advance may run before the goroutine reaches After(), leaving the
// pending list empty → Advance fires nothing → the watcher deadline is
// later registered against an already-advanced Now and never fires.
//
// RED on the original code (After inside the goroutine): Pending() is
// racily 0 or 1 depending on scheduler interleaving — fails
// deterministically under -count=50 -race.
// GREEN after moving clock.After(...) to the caller goroutine:
// Pending() is always 1 the instant ArmHandshakeTimeoutForTest returns.
func TestExporter_ArmHandshakeTimeoutRegistersTimerBeforeReturn(t *testing.T) {
	t.Parallel()

	const handshakeTimeout = 100 * time.Millisecond

	clk := testutil.NewFakeClockAt(exporterTestEpoch())

	exp := newExporterForTest(t,
		app.WithExporterClock(clk),
		app.WithExporterHandshakeTimeout(handshakeTimeout),
	)
	t.Cleanup(func() {
		require.NoError(t, exp.Shutdown(context.Background()))
	})

	// net.Pipe gives us a paired *net.Conn we can hand the arm helper.
	// We never read or write on it — armHandshakeTimeout only holds it
	// via connCloser to force-close on the timeout fire path.
	server, client := net.Pipe()

	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})

	// Invariant check BEFORE arming: the FakeClock must have no pending
	// deadlines so a post-arm count of 1 is unambiguous.
	require.Equal(t, 0, clk.Pending(),
		"precondition: FakeClock must start with no pending deadlines")

	stop := app.ArmHandshakeTimeoutForTest(exp, server)

	// Register the disarm BEFORE the assertion. FailNow on the invariant
	// check unwinds via t.Cleanup; if stop() ran only after the require
	// line, a failed RED iteration would leave the watcher parked on
	// the After channel and Shutdown would hang until the test timeout
	// expired.
	t.Cleanup(stop)

	pending := clk.Pending()

	// THE CORE ASSERTION: the instant Arm returns, the timer MUST be
	// registered. If we poll or sleep here we are cheating the
	// invariant; the whole point of the fix is that the caller
	// goroutine registers the deadline BEFORE returning, so no barrier
	// is needed between Arm and Pending.
	require.Equal(t, 1, pending,
		"armHandshakeTimeout must register its After deadline before "+
			"returning; otherwise clk.Advance from the test goroutine "+
			"races the watcher's delayed registration")
}
