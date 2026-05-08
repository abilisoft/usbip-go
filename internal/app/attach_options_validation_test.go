package app_test

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestImporterAttachRejectsNegativeMaxAttempts pins the invariant
// that Attach rejects a MaxAttempts option with a negative value
// rather than silently short-circuiting the reconnect loop. The
// `MaxAttempts == 0 || attempt <= MaxAttempts` gate in the reconnect
// loop means a negative value causes the loop body to never run;
// the watcher then emits "reconnect giving up" with attempt=0,
// misleading operators into thinking every retry exhausted when in
// fact no retry was ever attempted.
func TestImporterAttachRejectsNegativeMaxAttempts(t *testing.T) {
	t.Parallel()

	imp, _, _, kernel := newReconnectFixture(t,
		func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return domain.PortID(1), nil
		})
	t.Cleanup(func() { _ = imp.Close() })

	_ = kernel

	opts := app.AttachOptions{
		AutoReconnect: true,
		MaxAttempts:   -1,
	}

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.Error(t, err, "negative MaxAttempts must be rejected by Attach")
}
