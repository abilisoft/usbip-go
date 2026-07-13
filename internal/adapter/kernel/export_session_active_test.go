// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

const (
	testUSBIPStatusAvailableRaw = "1\n"
	testUSBIPStatusUsedRaw      = "2\n"
	testUSBIPStatusMalformedRaw = "used\n"
)

func TestExportSessionActiveParsesKernelStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusRaw  string
		wantActive bool
		wantErr    bool
	}{
		{
			name:       "used is active",
			statusRaw:  testUSBIPStatusUsedRaw,
			wantActive: true,
		},
		{
			name:      "available is inactive",
			statusRaw: testUSBIPStatusAvailableRaw,
		},
		{
			name:      "malformed status returns parse error",
			statusRaw: testUSBIPStatusMalformedRaw,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			statusPath := "sys/bus/usb/devices/" + testRootBusID + "/" + kernel.SysfsUsbipStatus
			mfs := fstest.MapFS{
				testFSModuleUSBIPCorePath: &fstest.MapFile{Mode: fs.ModeDir},
				testFSModuleUSBIPHostPath: &fstest.MapFile{Mode: fs.ModeDir},
				statusPath:                &fstest.MapFile{Data: []byte(tt.statusRaw)},
			}

			adapter, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
			require.NoError(t, err)

			active, err := adapter.ExportSessionActive(context.Background(), domain.BusID(testRootBusID))
			if tt.wantErr {
				require.Error(t, err)
				require.False(t, active)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantActive, active)
		})
	}
}

func TestExportSessionActiveReturnsStatusReadError(t *testing.T) {
	t.Parallel()

	mfs := fstest.MapFS{
		testFSModuleUSBIPCorePath: &fstest.MapFile{Mode: fs.ModeDir},
		testFSModuleUSBIPHostPath: &fstest.MapFile{Mode: fs.ModeDir},
	}

	adapter, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	active, err := adapter.ExportSessionActive(context.Background(), domain.BusID(testRootBusID))
	require.ErrorIs(t, err, domain.ErrDeviceNotFound)
	require.False(t, active)
}

func TestExportSessionActiveReturnsModuleError(t *testing.T) {
	t.Parallel()

	adapter, err := kernel.NewExporterAdapter(kernel.WithFS(fstest.MapFS{}))
	require.NoError(t, err)

	active, err := adapter.ExportSessionActive(context.Background(), domain.BusID(testRootBusID))
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
	require.False(t, active)
}
