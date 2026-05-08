// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// socketPair is a minimal Unix socketpair helper for ExportOnConn
// tests. Reuses the same pattern as attach_test.go but stays local so
// the tests do not depend on unexported helpers from a sibling file.
func socketPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()

	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, pair[0], 0)
	require.GreaterOrEqual(t, pair[1], 0)

	ld, err := strconv.ParseUint(strconv.Itoa(pair[0]), 10, 64)
	require.NoError(t, err)

	rd, err := strconv.ParseUint(strconv.Itoa(pair[1]), 10, 64)
	require.NoError(t, err)

	left := os.NewFile(uintptr(ld), "pair-left")
	right := os.NewFile(uintptr(rd), "pair-right")

	lc, err := net.FileConn(left)
	require.NoError(t, err)

	rc, err := net.FileConn(right)
	require.NoError(t, err)

	_ = left.Close()
	_ = right.Close()

	return lc, rc
}

// fdOfConn extracts the OS fd from a syscall.Conn for comparison
// against the ASCII payload ExportOnConn writes.
func fdOfConn(t *testing.T, conn net.Conn) uintptr {
	t.Helper()

	sc, ok := conn.(syscall.Conn)
	require.True(t, ok)

	raw, err := sc.SyscallConn()
	require.NoError(t, err)

	var fd uintptr

	cerr := raw.Control(func(f uintptr) { fd = f })
	require.NoError(t, cerr)

	return fd
}

// exportFS builds the MapFS the exporter-side tests need: modules
// present plus the target device's per-device sysfs dir with a
// writable usbip_sockfd attribute.
func exportFS(busID string) fstest.MapFS {
	return fstest.MapFS{
		"sys/module/usbip_core":                                     &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host":                                     &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID:                              &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/usbip_sockfd":            &fstest.MapFile{Data: []byte("")},
	}
}

type countingCloseConn struct {
	net.Conn

	closes atomic.Int32
}

func (c *countingCloseConn) Close() error {
	c.closes.Add(1)

	err := c.Conn.Close()
	if err != nil {
		return fmt.Errorf("close: %w", err)
	}

	return nil
}

func (c *countingCloseConn) SyscallConn() (syscall.RawConn, error) {
	inner, ok := c.Conn.(syscall.Conn)
	if !ok {
		return nil, errNoEmbeddedSyscall
	}

	raw, err := inner.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("embedded SyscallConn: %w", err)
	}

	return raw, nil
}

func TestExportOnConn_WritesFDToUsbipSockfd(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1.2")
	left, right := socketPair(t)

	defer func() { _ = right.Close() }()
	defer func() { _ = left.Close() }() // caller owns conn; adapter MUST NOT close it.

	fd := fdOfConn(t, left)

	var gotWrites []writeCall

	writer := func(path, data string) error {
		gotWrites = append(gotWrites, writeCall{Path: path, Data: data})

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(exportFS(string(busID))),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	err = a.ExportOnConn(context.Background(), left, busID)
	require.NoError(t, err)

	require.Len(t, gotWrites, 1)
	require.Equal(t, "/sys/bus/usb/devices/"+string(busID)+"/usbip_sockfd", gotWrites[0].Path)
	require.Equal(t, strconv.FormatUint(uint64(fd), 10), gotWrites[0].Data)
}

// TestExportOnConn_DoesNotCloseConn asserts caller-owned conn
// semantics: ExportOnConn never closes the caller's conn.
func TestExportOnConn_DoesNotCloseConn(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")
	left, right := socketPair(t)

	defer func() { _ = right.Close() }()

	counter := &countingCloseConn{Conn: left}
	writer := func(_, _ string) error { return nil }

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(exportFS(string(busID))),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	err = a.ExportOnConn(context.Background(), counter, busID)
	require.NoError(t, err)

	require.EqualValues(t, 0, counter.closes.Load())

	_ = left.Close()
}

func TestDisconnect_WritesMinusOne(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")

	var gotWrites []writeCall

	writer := func(path, data string) error {
		gotWrites = append(gotWrites, writeCall{Path: path, Data: data})

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(exportFS(string(busID))),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	err = a.Disconnect(context.Background(), busID)
	require.NoError(t, err)

	require.Len(t, gotWrites, 1)
	require.Equal(t, "/sys/bus/usb/devices/"+string(busID)+"/usbip_sockfd", gotWrites[0].Path)
	require.Equal(t, "-1", gotWrites[0].Data)
}

func TestDetachPort_WritesDecimalPortID(t *testing.T) {
	t.Parallel()

	var gotWrites []writeCall

	writer := func(path, data string) error {
		gotWrites = append(gotWrites, writeCall{Path: path, Data: data})

		return nil
	}

	// added a StatusTopology-backed bounds check to DetachPort,
	// so the fixture must expose nports + status (the BusMap-free
	// projection loadStatusTopology consumes) in addition to the
	// module-presence entries ModulesAvailable probes. nports=8 comfortably
	// includes port 5 (flat, kernel-post-Task-2 semantics).
	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(fstest.MapFS{
			"sys/module/usbip_core":                  &fstest.MapFile{Mode: fs.ModeDir},
			"sys/module/vhci_hcd":                    &fstest.MapFile{Mode: fs.ModeDir},
			"sys/devices/platform/vhci_hcd.0":        &fstest.MapFile{Mode: fs.ModeDir},
			"sys/devices/platform/vhci_hcd.0/nports": &fstest.MapFile{Data: []byte("8\n")},
			"sys/devices/platform/vhci_hcd.0/status": &fstest.MapFile{Data: []byte(
				"hub port sta spd dev      sockfd local_busid\n" +
					"hs  0000 000 000 00000000 000000 0-0\n",
			)},
		}),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	err = a.DetachPort(context.Background(), domain.PortID(5))
	require.NoError(t, err)

	require.Len(t, gotWrites, 1)
	require.Equal(t, "/sys/devices/platform/vhci_hcd.0/detach", gotWrites[0].Path)
	require.Equal(t, "5", gotWrites[0].Data)
}
