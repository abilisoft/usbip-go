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

// boundFS returns a MapFS for testing Unbind: modules present, bare
// device's driver is usbip-host (post-Bind state). Tests that
// exercise the Unbind() path use this instead of bindFS so the
// Unbind precheck (currentDriver == usbip-host) passes.
func boundFS(busID string) fstest.MapFS {
	mfs := bindFS(busID)

	mfs["sys/bus/usb/devices/"+busID+"/driver/driver_name"] = &fstest.MapFile{Data: []byte("usbip-host\n")}
	mfs["sys/bus/usb/devices/"+busID+"/driver"] = &fstest.MapFile{Data: []byte("usbip-host\n")}

	return mfs
}

// bindFS returns a MapFS wired up for a bind/unbind round trip on the
// supplied busID: modules present, device-driver symlink in place. The
// bare device carries the generic testUeventSubsystemUSB device-level driver (the kernel
// default for all enumerated USB devices) so the bind sequence exercises
// unbindCurrentDeviceDriver in addition to the interface unbind.
func bindFS(busID string) fstest.MapFS {
	iface := busID + ":1.0"

	return fstest.MapFS{
		testFSModuleUSBIPCorePath:                               &fstest.MapFile{Mode: fs.ModeDir},
		testFSModuleUSBIPHostPath:                               &fstest.MapFile{Mode: fs.ModeDir},
		testFSUSBIPHostDir:                                      &fstest.MapFile{Mode: fs.ModeDir},
		testFSUSBIPHostMatchBusIDPath:                           &fstest.MapFile{Data: []byte("")},
		testFSUSBIPHostBindPath:                                 &fstest.MapFile{Data: []byte("")},
		testFSUSBIPHostUnbindPath:                               &fstest.MapFile{Data: []byte("")},
		testFSUSBIPHostRebindPath:                               &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/devices/" + busID:                          &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/bConfigurationValue": &fstest.MapFile{Data: []byte("1\n")},
		"sys/bus/usb/devices/" + busID + "/driver/driver_name":  &fstest.MapFile{Data: []byte("usb\n")},
		"sys/bus/usb/devices/" + busID + "/driver":              &fstest.MapFile{Data: []byte("usb\n")},
		"sys/bus/usb/devices/" + iface:                          &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + iface + "/driver/driver_name":  &fstest.MapFile{Data: []byte("usbhid\n")},
		"sys/bus/usb/devices/" + iface + "/driver":              &fstest.MapFile{Data: []byte("usbhid\n")},
	}
}

func TestBind_WritesExactSequence(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1.2")
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(string(busID))),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.NoError(t, err)

	// Three writes: match_busid add, bare-device unbind, usbip-host bind.
	// Mirrors upstream usbip_bind.c::bind_device(): modify_match_busid()
	// runs FIRST so the kernel auto-probe that fires after unbind_other()
	// finds usbip-host's stub_probe winning the match table; otherwise
	// the original interface drivers (cdc_ether, cdc_ncm, usbhid, …)
	// reclaim the device in the auto-probe race window.
	require.Len(t, rec.calls, 3)

	require.Equal(t, writeCall{
		Path: testUSBIPHostMatchBusIDPath,
		Data: "add " + string(busID),
	}, rec.calls[0])

	require.Equal(t, writeCall{
		Path: "/sys/bus/usb/drivers/usb/unbind",
		Data: string(busID),
	}, rec.calls[1])

	require.Equal(t, writeCall{
		Path: testUSBIPHostBindPath,
		Data: string(busID),
	}, rec.calls[2])
}

func TestUnbind_WritesReverseSequence(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1.2")
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(boundFS(string(busID))),
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
		Path: testUSBIPHostMatchBusIDPath,
		Data: "del " + string(busID),
	}, rec.calls[2])

	require.Equal(t, writeCall{
		Path: "/sys/bus/usb/drivers/usbip-host/rebind",
		Data: string(busID),
	}, rec.calls[3])
}

// TestBind_EBUSYMapsToAlreadyBound confirms kernel-adapter and domain-model OpenSpec documents mapping for the
// bind write — when the EBUSY persists across the full retry budget.
// Bind retries the usbip-host bind step on EBUSY (transient drain),
// so the test injects a writeFunc that returns EBUSY for every write
// to that exact path and lets the others succeed. After
// usbipHostBindRetryAttempts retries the surfaced error must still
// classify to ErrDeviceAlreadyBound.
func TestBind_EBUSYMapsToAlreadyBound(t *testing.T) {
	t.Parallel()

	busID := domain.BusID(testRootBusID)
	rec := &writeRecord{}

	persistentEBUSYOnBind := func(p, data string) error {
		rec.mu.Lock()
		rec.calls = append(rec.calls, writeCall{Path: p, Data: data})
		rec.mu.Unlock()

		if p == testUSBIPHostBindPath {
			return unix.EBUSY
		}

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(string(busID))),
		kernel.WithWriteFunc(persistentEBUSYOnBind),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound)
}

// TestBind_ModuleMissingShortCircuits confirms runtime module loss
// surfaces ErrKernelModuleMissing before any write is attempted.
func TestBind_ModuleMissingShortCircuits(t *testing.T) {
	t.Parallel()

	busID := domain.BusID(testRootBusID)
	rec := &writeRecord{}

	m := bindFS(string(busID))
	// Simulate runtime module loss.
	delete(m, testFSModuleUSBIPHostPath)

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(m),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
	require.Empty(t, rec.calls, "no sysfs writes should be attempted when module is missing")
}

// TestBind_NoDeviceDriver_ProceedsToMatchAndBind pins the half-state
// recovery contract: a device sysfs entry that exists but has no
// bare-device driver attached (e.g. left over from a previous failed
// bind, or a fresh hot-plug pre-probe) must not block bind. There is
// nothing to unbind so Bind proceeds straight to match_busid +
// usbip-host bind — only TWO writes total.
//
// Older revisions returned ErrDeviceNotBound here, forcing operators
// to manually trigger /sys/bus/usb/drivers_probe to re-attach a
// driver they then immediately want unbound. That surface is handled
// inside Bind.
func TestBind_NoDeviceDriver_ProceedsToMatchAndBind(t *testing.T) {
	t.Parallel()

	busID := domain.BusID(testRootBusID)
	rec := &writeRecord{}

	m := bindFS(string(busID))
	// Remove the bare-device driver-indicator paths. Device dir stays
	// present so checkAlreadyExported sees "no driver" and continues.
	delete(m, "sys/bus/usb/devices/"+string(busID)+"/driver/driver_name")
	delete(m, "sys/bus/usb/devices/"+string(busID)+"/driver")

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(m),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.NoError(t, err,
		"Bind must succeed when the bare device has no driver — nothing to unbind")
	require.Len(t, rec.calls, 2,
		"no driver means no unbind; match_busid add + usbip-host bind only")

	require.Equal(t, testUSBIPHostMatchBusIDPath, rec.calls[0].Path)
	require.Equal(t, testUSBIPHostBindPath, rec.calls[1].Path)
}
