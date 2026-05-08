// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestImporterAttachRejectsRemoteMaliciousBusID pins the wire-side
// trust boundary: a peer that sends an OP_REP_IMPORT body whose busid
// would escape sysfs-basename rules (e.g. contains '/') MUST NOT
// reach kernel.AttachRemote. ValidateWireBusID gates the remote-
// supplied value before it is concatenated into a sysfs path.
func TestImporterAttachRejectsRemoteMaliciousBusID(t *testing.T) {
	t.Parallel()

	conn := newFakeConn()

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}

	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) {
			dev := attachDevice()

			dev.BusID = domain.BusID("../../etc/passwd")

			return dev, nil
		},
	}

	var attachCalled bool

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			attachCalled = true

			return domain.PortID(1), nil
		},
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrBusIDInvalid)
	require.False(t, attachCalled,
		"kernel.AttachRemote must not be invoked when remote busid is malformed")
}

// TestImporterAttachRejectsInvalidBusID pins the importer-side
// boundary check: a BusID built by raw string conversion (bypassing
// ParseBusID) MUST be rejected before any wire codec encode or
// kernel-attach sysfs writes. Mirrors Exporter.Bind/Unbind on the
// other side of the protocol.
func TestImporterAttachRejectsInvalidBusID(t *testing.T) {
	t.Parallel()

	var kernelCalled bool

	imp, _, _, kernel := newReconnectFixture(t,
		func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			kernelCalled = true

			return domain.PortID(1), nil
		})

	_ = kernel

	t.Cleanup(func() { _ = imp.Close() })

	_, err := imp.Attach(context.Background(), testRemote(),
		domain.BusID("../etc/passwd"), app.AttachOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrBusIDInvalid)
	require.False(t, kernelCalled,
		"kernel adapter must not be invoked when busid is malformed")
}

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
