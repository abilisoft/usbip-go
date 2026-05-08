// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestBind_AlreadyBound_NoConfigValue_ReturnsCleanly pins the
// short-circuit ordering: when the bare device is already bound to
// usbip-host AND its bConfigurationValue is 0 (the kernel state after
// the generic-usb driver has detached and before stub_probe re-reads
// the descriptor), Bind MUST surface ErrDeviceAlreadyBound — NOT a
// downstream ifaceSuffix error about "no active configuration".
//
// Without the early short-circuit, ifaceSuffix runs first and returns
// ErrDeviceNotBound + "bConfigurationValue=0" which masks the real
// state and forces operators to chase a non-existent unconfigured-
// device error when their issue is that the device is already
// exported.
func TestBind_AlreadyBound_NoConfigValue_ReturnsCleanly(t *testing.T) {
	t.Parallel()

	const busID = "1-1"

	mfs := bindFS(busID)
	// State after a successful prior bind: driver = usbip-host,
	// bConfigurationValue = 0 because USB core unconfigured the
	// device when the original driver was detached.
	mfs["sys/bus/usb/devices/"+busID+"/driver/driver_name"].Data = []byte("usbip-host\n")
	mfs["sys/bus/usb/devices/"+busID+"/driver"].Data = []byte("usbip-host\n")
	mfs["sys/bus/usb/devices/"+busID+"/bConfigurationValue"] = &fstest.MapFile{Data: []byte("0\n")}

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound,
		"already-bound state must short-circuit BEFORE ifaceSuffix; "+
			"otherwise a configValue=0 device returns a misleading not-bound error")

	require.Empty(t, rec.calls,
		"already-bound short-circuit must fire BEFORE any sysfs write")
}
