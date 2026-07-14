// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"net"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestImporterAdapterAttachAndDetachSharePortMutationBoundary(t *testing.T) {
	t.Parallel()

	reservationEntered := make(chan struct{})
	releaseReservation := make(chan struct{})
	attachWriteEntered := make(chan struct{})
	releaseAttachWrite := make(chan struct{})
	detachWriteEntered := make(chan struct{})
	releaseDetachWrite := make(chan struct{})
	status := newSinglePortStatus(8)

	writer := func(path, _ string) error {
		switch path {
		case testVHCIAttachPath:
			close(attachWriteEntered)
			<-releaseAttachWrite
			status.markUsed(0)
		case kernel.SysfsVHCIHCD + "/" + kernel.SysfsVHCIDetach:
			close(detachWriteEntered)
			<-releaseDetachWrite
		}

		return nil
	}

	adapter, err := kernel.NewImporterAdapter(
		kernel.WithFS(&mutableStatusFS{inner: attachFS(), state: status}),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	left, right := socketpairConns(t)
	defer func() { require.NoError(t, right.Close()) }()

	attachResult := make(chan error, 1)

	go func(conn net.Conn) {
		_, attachErr := adapter.AttachRemote(
			context.Background(), conn,
			app.RemoteDeviceSpec{
				DevID: 1,
				Speed: domain.SpeedHigh,
				ReserveLocalPort: func(domain.PortID) error {
					close(reservationEntered)
					<-releaseReservation

					return nil
				},
			},
		)
		attachResult <- attachErr
	}(left)

	<-reservationEntered
	require.True(t, kernel.PortMutationLockHeldForTest(adapter),
		"AttachRemote must hold the shared mutation mutex after discovery and before publication")

	var (
		detachResult      = make(chan error, 1)
		detachCallStarted = make(chan struct{})
	)

	go func() {
		close(detachCallStarted)

		detachResult <- adapter.DetachPort(context.Background(), domain.PortID(0))
	}()

	<-detachCallStarted

	select {
	case <-detachWriteEntered:
		t.Fatal("DetachPort overlapped AttachRemote between discovery and mutation")
	default:
	}

	close(releaseReservation)
	<-attachWriteEntered

	select {
	case <-detachWriteEntered:
		t.Fatal("DetachPort overlapped AttachRemote during its sysfs mutation")
	default:
	}

	close(releaseAttachWrite)
	require.NoError(t, <-attachResult)

	<-detachWriteEntered
	require.True(t, kernel.PortMutationLockHeldForTest(adapter),
		"DetachPort must hold the same mutation mutex during its sysfs write")
	close(releaseDetachWrite)
	require.NoError(t, <-detachResult)
}
