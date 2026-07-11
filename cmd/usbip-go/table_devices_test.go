// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// errWriteDenied is the static sentinel returned by failWriter.
var errWriteDenied = errors.New("disk full")

// failWriter returns an error on every Write call.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errWriteDenied }

// TestTableRendererDevices smoke-tests Devices: the method must write a
// non-empty table that includes the busid and column headers.
func TestTableRendererDevices(t *testing.T) {
	t.Parallel()

	devs := []usbip.Device{
		{
			BusID:     domain.BusID(testRootBusID),
			Speed:     domain.SpeedHigh,
			VendorID:  0x0951,
			ProductID: 0x1666,
			Class:     domain.USBClass(0),
		},
	}

	var out bytes.Buffer

	require.NoError(t, tableRenderer{}.Devices(&out, devs))

	got := out.String()
	require.NotEmpty(t, got)

	for _, want := range []string{testRootBusID, testBusIDHeader, "SPEED"} {
		require.Contains(t, got, want)
	}
}

// TestTableRendererDevices_WriteError pins the error path when the
// underlying writer rejects bytes. Devices must surface the failure
// wrapped in "render device table: ...".
func TestTableRendererDevices_WriteError(t *testing.T) {
	t.Parallel()

	err := tableRenderer{}.Devices(failWriter{}, []usbip.Device{{BusID: testRootBusID}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "render device table")
}

// TestTableRendererPorts_WriteError pins the Ports write-error path.
func TestTableRendererPorts_WriteError(t *testing.T) {
	t.Parallel()

	err := tableRenderer{}.Ports(failWriter{}, []usbip.Port{{ID: 1, BusID: testRootBusID}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "render port table")
}

// TestTableRendererSessions_WriteError pins the Sessions write-error path.
func TestTableRendererSessions_WriteError(t *testing.T) {
	t.Parallel()

	sid, err := domain.NewSessionID()
	require.NoError(t, err)

	err = tableRenderer{}.Sessions(failWriter{}, []usbip.Session{{ID: sid, BusID: testRootBusID}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "render session table")
}

// TestTableRendererEvent_WriteError_KnownType pins the write-error path inside
// tableRenderer.Event when the event is recognised by classifyEvent (the
// fmt.Fprintf at the end of the function fails). The error must be wrapped in
// "render event: ...".
func TestTableRendererEvent_WriteError_KnownType(t *testing.T) {
	t.Parallel()

	ev := domain.DeviceBoundEvent{Device: domain.Device{BusID: domain.BusID(testRootBusID)}}

	err := tableRenderer{}.Event(failWriter{}, ev)
	require.Error(t, err)
	require.Contains(t, err.Error(), "render event")
}

// TestTableRendererEvent_WriteError_UnknownType pins the write-error path inside
// the !ok branch of tableRenderer.Event: when classifyEvent returns nil the
// fallback fmt.Fprintf must propagate the writer error wrapped in "render event: ...".
func TestTableRendererEvent_WriteError_UnknownType(t *testing.T) {
	t.Parallel()

	err := tableRenderer{}.Event(failWriter{}, unknownEvent{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "render event")
}

// TestDeviceLabel_AllBranches pins every branch of deviceLabel:
// both empty (placeholder), only manufacturer, only product, both
// populated. Together with the populated-both case in goldenDevices
// these cover all four arms.
func TestDeviceLabel_AllBranches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		manufacturer string
		product      string
		want         string
	}{
		{"both empty -> placeholder", "", "", "—"},
		{"manufacturer empty -> product only", "", testProduct, testProduct},
		{"product empty -> manufacturer only", testManufacturer, "", testManufacturer},
		{"both populated -> joined with space", testManufacturer, testProduct, "Kingston DataTraveler"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := deviceLabel(usbip.Device{
				Manufacturer: tc.manufacturer,
				Product:      tc.product,
			})
			require.Equal(t, tc.want, got)
		})
	}
}
