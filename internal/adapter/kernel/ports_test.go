//go:build linux

package kernel_test

import (
	"context"
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// statusFS builds a MapFS for findFreePort / ListPorts tests. status
// is the primary status file contents; statusN maps controller index
// (1, 2, …) to the contents of the corresponding status.N file.
func statusFS(status string, statusN map[int]string, nports int) fstest.MapFS {
	m := fstest.MapFS{
		"sys/module/usbip_core": &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/vhci_hcd":   &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host": &fstest.MapFile{Mode: fs.ModeDir},
		"sys/devices/platform/vhci_hcd.0":        &fstest.MapFile{Mode: fs.ModeDir},
		"sys/devices/platform/vhci_hcd.0/nports": &fstest.MapFile{Data: []byte(itoaBytes(nports))},
		"sys/devices/platform/vhci_hcd.0/status": &fstest.MapFile{Data: []byte(status)},
	}

	for i, body := range statusN {
		m[fmt.Sprintf("sys/devices/platform/vhci_hcd.0/status.%d", i)] = &fstest.MapFile{Data: []byte(body)}
	}

	return m
}

// itoaBytes avoids importing strconv in a function used inside a map
// literal.
func itoaBytes(n int) string {
	return fmt.Sprintf("%d\n", n)
}

// TestFindFreePort_HappyHS verifies the hs-row selection at 3 rows (0
// free, 1 used, 2 free). SpeedHigh picks port 0.
func TestFindFreePort_HappyHS(t *testing.T) {
	t.Parallel()

	status := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n" +
		"hs  0001 003 003 01020304 000005 1-1\n" +
		"hs  0002 000 000 00000000 000000 0-0\n" +
		"ss  0003 003 005 01020304 000005 1-1\n" +
		"ss  0004 000 000 00000000 000000 0-0\n"

	a, err := kernel.NewImporterAdapter(kernel.WithFS(statusFS(status, nil, 16)))
	require.NoError(t, err)

	got, err := kernel.FindFreePortForTest(a, domain.SpeedHigh)
	require.NoError(t, err)
	require.EqualValues(t, 0, got)
}

// TestFindFreePort_FlatSSNumbering exercises flat port numbering
// across status and status.1.
func TestFindFreePort_FlatSSNumbering(t *testing.T) {
	t.Parallel()

	// status.0: 2 hs + 2 ss rows (all ss busy).
	primary := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n" +
		"hs  0001 003 003 01020304 000005 1-1\n" +
		"ss  0002 003 005 01020304 000005 1-1\n" +
		"ss  0003 003 005 01020304 000005 1-1\n"

	// status.1: one more ss row, free. Its port number on the wire
	// is 0000, and the parser offsets by vhciPortsPerController (16).
	secondary := "hs  0000 003 003 01020304 000005 1-1\n" +
		"ss  0001 000 000 00000000 000000 0-0\n"

	mfs := statusFS(primary, map[int]string{1: secondary}, 17)

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := kernel.FindFreePortForTest(a, domain.SpeedSuper)
	require.NoError(t, err)
	require.EqualValues(t, 17, got, "flat numbering: status.1 row offset by 16")
}

// TestFindFreePort_AllSSBusyReturnsNoFreePort covers the all-full
// speed-class path.
func TestFindFreePort_AllSSBusyReturnsNoFreePort(t *testing.T) {
	t.Parallel()

	status := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n" +
		"ss  0001 003 005 01020304 000005 1-1\n" +
		"ss  0002 003 005 01020304 000005 1-1\n"

	a, err := kernel.NewImporterAdapter(kernel.WithFS(statusFS(status, nil, 16)))
	require.NoError(t, err)

	_, err = kernel.FindFreePortForTest(a, domain.SpeedSuperPlus)
	require.ErrorIs(t, err, domain.ErrNoFreePort)
	require.ErrorContains(t, err, "SuperSpeed+")
}

// TestListPorts_ReturnsAllRowsIncludingFree confirms ListPorts surfaces
// free rows as well as used ones.
func TestListPorts_ReturnsAllRowsIncludingFree(t *testing.T) {
	t.Parallel()

	status := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n" +
		"hs  0001 003 003 01020304 000005 1-1\n" +
		"ss  0002 003 005 01020304 000005 1-1\n"

	a, err := kernel.NewImporterAdapter(kernel.WithFS(statusFS(status, nil, 16)))
	require.NoError(t, err)

	ports, err := a.ListPorts(context.Background())
	require.NoError(t, err)
	require.Len(t, ports, 3)

	require.Equal(t, domain.StatusNull, ports[0].Status)
	require.Equal(t, domain.StatusUsed, ports[1].Status)
	require.Equal(t, domain.StatusUsed, ports[2].Status)
}

// TestListPorts_HeaderlessFileIsEmpty matches the spec: if the header
// is absent, treat as empty (returns no rows, no error).
func TestListPorts_HeaderlessFileIsEmpty(t *testing.T) {
	t.Parallel()

	// Note: our implementation tolerates both header-present and
	// header-absent inputs. An empty body trivially returns no rows.
	status := ""

	a, err := kernel.NewImporterAdapter(kernel.WithFS(statusFS(status, nil, 16)))
	require.NoError(t, err)

	ports, err := a.ListPorts(context.Background())
	require.NoError(t, err)
	require.Empty(t, ports)
}

// TestListPorts_MalformedRowSkipped confirms a malformed leading token
// is logged-and-skipped rather than failing the whole call.
func TestListPorts_MalformedRowSkipped(t *testing.T) {
	t.Parallel()

	status := "hub port sta spd dev      sockfd local_busid\n" +
		"xx  0000 000 000 00000000 000000 0-0\n" + // malformed
		"hs  0001 003 003 01020304 000005 1-1\n"

	a, err := kernel.NewImporterAdapter(kernel.WithFS(statusFS(status, nil, 16)))
	require.NoError(t, err)

	ports, err := a.ListPorts(context.Background())
	require.NoError(t, err)
	require.Len(t, ports, 1, "malformed row skipped, good row retained")
	require.Equal(t, domain.StatusUsed, ports[0].Status)
}

// TestListPorts_ModuleMissingReturnsBoth confirms §3.4 contract: on
// module loss, return (nil, ErrKernelModuleMissing).
func TestListPorts_ModuleMissingReturnsBoth(t *testing.T) {
	t.Parallel()

	status := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 003 003 01020304 000005 1-1\n"

	mfs := statusFS(status, nil, 16)
	delete(mfs, "sys/module/vhci_hcd")

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	ports, err := a.ListPorts(context.Background())
	require.Empty(t, ports)
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
}
