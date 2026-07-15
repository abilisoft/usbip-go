// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestParseStatusFile_TranslatesKernelVDEVStatus pins the contract
// that the vhci status parser maps the raw `sta` column the kernel
// writes — values 4-7 drawn from the VDEV_ST_* half of
// usbip_device_status — onto the domain.Status enum the rest of the
// code consumes. The kernel enum intentionally collides with the
// SDEV_ST_* (server-side) range, so the parser must translate at the
// boundary; without the translation, a freshly-reset VHCI port shows
// up as domain.StatusError and findFreePort returns
// domain.ErrNoFreePort even when every port is idle.
//
// This reproduction exercises every documented vdev code the kernel
// writes during normal port lifecycle transitions.
func TestParseStatusFile_TranslatesKernelVDEVStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		kernelSta  string // raw column as the kernel writes it
		wantStatus domain.Status
	}{
		{"null / unused", "004", domain.StatusNull},
		{"not-assigned", "005", domain.StatusNotAssigned},
		{"used", "006", domain.StatusUsed},
		{"error", "007", domain.StatusError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mfs := statusFS("", nil, 16)

			a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
			require.NoError(t, err)

			body := "hub port sta spd dev      sockfd local_busid\n" +
				"hs  0000 " + tc.kernelSta + " 000 00000000 000000 0-0\n"

			rows, err := kernel.ParseStatusFileForTest(a, body, "status", 0, 16)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, tc.wantStatus, rows[0].Status,
				"kernel sta=%s must translate to %s, not %s",
				tc.kernelSta, tc.wantStatus, rows[0].Status)
		})
	}
}

// TestParseStatusFile_RejectsControllerNotReadyPlaceholder reproduces the
// exact row shape Linux status_show_not_ready emits while
// platform_get_drvdata() is nil. Although raw status 005 normally means a
// claimed vdev, the sixteen-zero sockfd token identifies this row as synthetic
// controller state. Returning it would make every CLI Port slot look attached.
func TestParseStatusFile_RejectsControllerNotReadyPlaceholder(t *testing.T) {
	t.Parallel()

	mfs := statusFS("", nil, 16)

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	body := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 005 000 00000000 0000000000000000 0-0\n"

	rows, err := kernel.ParseStatusFileForTest(a, body, "status", 0, 16)
	require.Nil(t, rows)
	require.ErrorContains(t, err, "vhci status controller not ready")
	require.ErrorContains(t, err, "source=status port=0")
}

// TestParseStatusFile_PreservesClaimedNotAssignedRow proves the fail-closed
// controller check does not hide a real vdev between socket handoff and USB
// address assignment. port_show_vhci renders that ordinary row with a
// six-digit zero sockfd, unlike status_show_not_ready's sixteen-zero marker.
func TestParseStatusFile_PreservesClaimedNotAssignedRow(t *testing.T) {
	t.Parallel()

	mfs := statusFS("", nil, 16)

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	body := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 005 000 00000000 000000 0-0\n"

	rows, err := kernel.ParseStatusFileForTest(a, body, "status", 0, 16)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, domain.StatusNotAssigned, rows[0].Status)
}

// TestListPorts_PropagatesControllerNotReady proves the public adapter boundary
// returns no partial or synthetic Ports when a controller snapshot is not
// usable. Callers may retry; they must not interpret every placeholder as an
// active attachment.
func TestListPorts_PropagatesControllerNotReady(t *testing.T) {
	t.Parallel()

	status := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 005 000 00000000 0000000000000000 0-0\n"

	a, err := kernel.NewImporterAdapter(kernel.WithFS(statusFS(status, nil, 16)))
	require.NoError(t, err)

	ports, err := a.ListPorts(context.Background())
	require.Nil(t, ports)
	require.ErrorContains(t, err, "vhci status controller not ready")
}

// TestFindFreePort_PropagatesControllerNotReady pins the allocation side of the
// same fail-closed contract. A not-ready controller must not be treated as
// either available capacity or a generic exhausted hub.
func TestFindFreePort_PropagatesControllerNotReady(t *testing.T) {
	t.Parallel()

	status := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 005 000 00000000 0000000000000000 0-0\n"

	a, err := kernel.NewImporterAdapter(kernel.WithFS(statusFS(status, nil, 16)))
	require.NoError(t, err)

	_, err = kernel.FindFreePortForTest(a, domain.SpeedHigh)
	require.ErrorContains(t, err, "vhci status controller not ready")
	require.NotErrorIs(t, err, domain.ErrNoFreePort)
}
