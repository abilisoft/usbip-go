// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// moduleAndDriverFS returns a minimal sysfs skeleton with modules and
// the usbip-host driver dir so the preflight passes before ifaceSuffix runs.
func moduleAndDriverFS(busID string) fstest.MapFS {
	return fstest.MapFS{
		"sys/module/usbip_core":                      &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host":                      &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/usbip-host":             &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/usbip-host/match_busid": &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/drivers/usbip-host/bind":        &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/devices/" + busID:               &fstest.MapFile{Mode: fs.ModeDir},
	}
}

// TestBind_ifaceSuffix_ZeroConfigValue pins that ifaceSuffix rejects
// a device reporting bConfigurationValue=0 (no active configuration).
// The kernel sets this during enumeration before SET_CONFIGURATION
// completes; binding such a device would target "<busid>:0.0" which
// sysfs does not populate.
func TestBind_ifaceSuffix_ZeroConfigValue(t *testing.T) {
	t.Parallel()

	const busID = "2-1"

	mfs := moduleAndDriverFS(busID)

	mfs["sys/bus/usb/devices/"+busID+"/bConfigurationValue"] = &fstest.MapFile{Data: []byte("0\n")}

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrDeviceNotBound,
		"a device reporting bConfigurationValue=0 has no active configuration and must surface ErrDeviceNotBound")
}

// TestBind_ifaceSuffix_ConfigValueExceedsByte pins that ifaceSuffix
// rejects a device whose bConfigurationValue exceeds 255. The field is
// declared uint8 in USB descriptors; values above 0xFF are never
// emitted by real hardware but can appear in fuzz/corrupt sysfs reads.
func TestBind_ifaceSuffix_ConfigValueExceedsByte(t *testing.T) {
	t.Parallel()

	const busID = "2-2"

	mfs := moduleAndDriverFS(busID)

	// 256 is > byteMax (0xFF); the kernel never emits this, but
	// out-of-range sysfs reads must be rejected rather than truncated.
	mfs["sys/bus/usb/devices/"+busID+"/bConfigurationValue"] = &fstest.MapFile{Data: []byte("256\n")}

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.Error(t, err,
		"bConfigurationValue > 255 exceeds uint8 and must error; truncation would target the wrong interface directory")
}
