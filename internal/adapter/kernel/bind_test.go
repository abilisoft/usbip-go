// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"io/fs"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// writeRecord captures each write call for ordering assertions. Safe
// for concurrent use.
type writeRecord struct {
	mu    sync.Mutex
	calls []writeCall
}

type writeCall struct {
	Path string
	Data string
}

func (r *writeRecord) record() kernel.WriteFunc {
	return func(path, data string) error {
		r.mu.Lock()
		defer r.mu.Unlock()

		r.calls = append(r.calls, writeCall{Path: path, Data: data})

		return nil
	}
}

func (r *writeRecord) errAt(i int, err error) kernel.WriteFunc {
	return func(path, data string) error {
		r.mu.Lock()
		defer r.mu.Unlock()

		r.calls = append(r.calls, writeCall{Path: path, Data: data})

		if len(r.calls)-1 == i {
			return err
		}

		return nil
	}
}

// bindFS returns a MapFS wired up for a bind/unbind round trip on the
// supplied busID: modules present, device-driver symlink in place.
func bindFS(busID string) fstest.MapFS {
	iface := busID + ":1.0"

	return fstest.MapFS{
		"sys/module/usbip_core":                                 &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host":                                 &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/usbip-host":                        &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/usbip-host/match_busid":            &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/drivers/usbip-host/bind":                   &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/drivers/usbip-host/unbind":                 &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/drivers/usbip-host/rebind":                 &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/devices/" + busID:                          &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/bConfigurationValue": &fstest.MapFile{Data: []byte("1\n")},
		"sys/bus/usb/devices/" + iface:                          &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + iface + "/driver/driver_name":  &fstest.MapFile{Data: []byte("usbhid\n")},
		"sys/bus/usb/devices/" + iface + "/driver":              &fstest.MapFile{Data: []byte("usbhid\n")},
	}
}

func TestBind_WritesExactSequence(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1.2")
	iface := string(busID) + ":1.0"
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(string(busID))),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.NoError(t, err)

	require.Len(t, rec.calls, 3)

	require.Equal(t, writeCall{
		Path: "/sys/bus/usb/drivers/usbhid/unbind",
		Data: iface,
	}, rec.calls[0])

	require.Equal(t, writeCall{
		Path: "/sys/bus/usb/drivers/usbip-host/match_busid",
		Data: "add " + string(busID),
	}, rec.calls[1])

	require.Equal(t, writeCall{
		Path: "/sys/bus/usb/drivers/usbip-host/bind",
		Data: string(busID),
	}, rec.calls[2])
}

func TestUnbind_WritesReverseSequence(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1.2")
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(string(busID))),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Unbind(context.Background(), busID)
	require.NoError(t, err)

	require.Len(t, rec.calls, 4)

	// rec.calls[0]: pre-disconnect sockfd write (covered by
	// TestUnbind_DisconnectsSockfdBeforeUnbindWrite)

	require.Equal(t, writeCall{
		Path: "/sys/bus/usb/drivers/usbip-host/unbind",
		Data: string(busID),
	}, rec.calls[1])

	require.Equal(t, writeCall{
		Path: "/sys/bus/usb/drivers/usbip-host/match_busid",
		Data: "del " + string(busID),
	}, rec.calls[2])

	require.Equal(t, writeCall{
		Path: "/sys/bus/usb/drivers/usbip-host/rebind",
		Data: string(busID),
	}, rec.calls[3])
}

// TestBind_EBUSYMapsToAlreadyBound confirms v1 contract §6.4 mapping for the
// bind write.
func TestBind_EBUSYMapsToAlreadyBound(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(string(busID))),
		// Fail on index 2 (bind is the 3rd write).
		kernel.WithWriteFunc(rec.errAt(2, unix.EBUSY)),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound)
}

// TestBind_ModuleMissingShortCircuits confirms runtime module loss
// surfaces ErrKernelModuleMissing before any write is attempted.
func TestBind_ModuleMissingShortCircuits(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")
	rec := &writeRecord{}

	m := bindFS(string(busID))
	// Simulate runtime module loss.
	delete(m, "sys/module/usbip_host")

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(m),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
	require.Empty(t, rec.calls, "no sysfs writes should be attempted when module is missing")
}

// TestBind_UnboundInterfaceReturnsErrDeviceNotBound exercises the
// spec semantics for a device that exists in sysfs but has no driver
// attached yet (fresh plug-in that userland has not yet bound). The
// interface dir exists but neither driver_name nor the driver
// symlink is present. Bind's currentDriver lookup must surface
// ErrDeviceNotBound, not ErrDeviceNotFound (which means the device
// itself is missing).
func TestBind_UnboundInterfaceReturnsErrDeviceNotBound(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")
	iface := string(busID) + ":1.0"
	rec := &writeRecord{}

	m := bindFS(string(busID))
	// Remove both driver-indicator paths while keeping the interface
	// directory itself present. This is the "unbound but device
	// exists" scenario.
	delete(m, "sys/bus/usb/devices/"+iface+"/driver/driver_name")
	delete(m, "sys/bus/usb/devices/"+iface+"/driver")

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(m),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.ErrorIs(t, err, domain.ErrDeviceNotBound,
		"unbound interface must surface ErrDeviceNotBound, not ErrDeviceNotFound")
	require.Empty(t, rec.calls, "no sysfs writes should occur before the error")
}
