// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

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
//
// The map is also seeded with the minimal usb* children discoverTopology
// requires (one HS + one SS sibling per controller), so 's
// topology-driven readStatusRows path finds a valid BusMap. The
// controller count is inferred from len(statusN)+1.
func statusFS(status string, statusN map[int]string, nports int) fstest.MapFS {
	m := fstest.MapFS{
		"sys/module/usbip_core":                  &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/vhci_hcd":                    &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host":                  &fstest.MapFile{Mode: fs.ModeDir},
		"sys/devices/platform/vhci_hcd.0":        &fstest.MapFile{Mode: fs.ModeDir},
		"sys/devices/platform/vhci_hcd.0/nports": &fstest.MapFile{Data: []byte(itoaBytes(nports))},
		"sys/devices/platform/vhci_hcd.0/status": &fstest.MapFile{Data: []byte(status)},
	}

	for i, body := range statusN {
		m[fmt.Sprintf("sys/devices/platform/vhci_hcd.0/status.%d", i)] = &fstest.MapFile{Data: []byte(body)}
	}

	// Seed usb* children: one HS + one SS per controller. Bus numbers
	// are allocated 2*idx+1 (HS) and 2*idx+2 (SS); controller count is
	// the number of status files present (primary + len(statusN)).
	seedUSBChildren(m, len(statusN)+1)

	return m
}

// seedUSBChildren populates m with the minimum vhci_hcd.<N>/usb*/busnum
// files discoverTopology expects: exactly two usb children per
// controller (rank 0 → HS, rank 1 → SS), with bus numbers allocated
// deterministically so the Busmap is predictable across tests.
func seedUSBChildren(m fstest.MapFS, ctrlCount int) {
	for idx := range ctrlCount {
		seedUSBChildPair(m, idx)
	}
}

// seedUSBChildPair writes the HS + SS sibling busnum files for a single
// vhci_hcd.<idx> controller. Split out to keep seedUSBChildren below the
// wsl_v5 whitespace lint's tolerance for contiguous map assigns.
func seedUSBChildPair(m fstest.MapFS, idx int) {
	hsBus := 2*idx + 1
	hsPath := fmt.Sprintf("sys/devices/platform/vhci_hcd.%d/usb%d/busnum", idx, hsBus)

	m[hsPath] = &fstest.MapFile{Data: []byte(itoaBytes(hsBus))}

	ssBus := 2*idx + 2
	ssPath := fmt.Sprintf("sys/devices/platform/vhci_hcd.%d/usb%d/busnum", idx, ssBus)

	m[ssPath] = &fstest.MapFile{Data: []byte(itoaBytes(ssBus))}
}

// itoaBytes avoids importing strconv in a function used inside a map
// literal.
func itoaBytes(n int) string {
	return fmt.Sprintf("%d\n", n)
}

// TestFindFreePort_HappyHS verifies the hs-row selection at 3 rows (0
// free, 1 used, 2 free). SpeedHigh picks port 0. Kernel default
// VHCI_HC_PORTS=8, so HS ports are flat 0..7 and SS ports are flat
// 8..15 on a single controller (nports=16).
func TestFindFreePort_HappyHS(t *testing.T) {
	t.Parallel()

	status := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n" +
		"hs  0001 003 003 01020304 000005 1-1\n" +
		"hs  0002 000 000 00000000 000000 0-0\n" +
		"ss  0008 003 005 01020304 000005 1-1\n" +
		"ss  0009 000 000 00000000 000000 0-0\n"

	a, err := kernel.NewImporterAdapter(kernel.WithFS(statusFS(status, nil, 16)))
	require.NoError(t, err)

	got, err := kernel.FindFreePortForTest(a, domain.SpeedHigh)
	require.NoError(t, err)
	require.EqualValues(t, 0, got)
}

// TestFindFreePort_FlatSSNumbering exercises flat port numbering
// across status and status.1. The kernel already writes flat port
// identifiers in every row of every status file (see status_show_vhci
// in vhci_sysfs.c: flat = pdev_nr*VHCI_PORTS + hubOffset + rhport), so
// the parser must trust the value verbatim — no per-controller offset
// is applied on top of what the kernel emitted.
func TestFindFreePort_FlatSSNumbering(t *testing.T) {
	t.Parallel()

	// Default VHCI_HC_PORTS=8 layout. Controller 0 owns flat ports
	// 0..15 (HS 0..7, SS 8..15); controller 1 owns flat ports 16..31
	// (HS 16..23, SS 24..31). primary is status (controller 0) and
	// secondary is status.1 (controller 1); nports=32 = 2*VHCI_PORTS.
	primary := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n" +
		"hs  0001 003 003 01020304 000005 1-1\n" +
		"ss  0008 003 005 01020304 000005 1-1\n" +
		"ss  0009 003 005 01020304 000005 1-1\n"

	// status.1: one free SS row at flat port 25 (pdev_nr=1, hub
	// offset 8, rhport 1). Under the trust-flat contract the port ID
	// comes through untouched — no controllerIdx*16 offset is added
	// on top.
	secondary := "hs  0016 003 003 01020304 000005 1-1\n" +
		"ss  0025 000 000 00000000 000000 0-0\n"

	mfs := statusFS(primary, map[int]string{1: secondary}, 32)

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := kernel.FindFreePortForTest(a, domain.SpeedSuper)
	require.NoError(t, err)
	require.EqualValues(t, 25, got, "kernel-emitted flat port is trusted verbatim")
}

// TestFindFreePort_AllSSBusyReturnsNoFreePort covers the all-full
// speed-class path. SS rows sit in the flat range 8..15 on a
// single-controller default-build kernel.
func TestFindFreePort_AllSSBusyReturnsNoFreePort(t *testing.T) {
	t.Parallel()

	status := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n" +
		"ss  0008 003 005 01020304 000005 1-1\n" +
		"ss  0009 003 005 01020304 000005 1-1\n"

	a, err := kernel.NewImporterAdapter(kernel.WithFS(statusFS(status, nil, 16)))
	require.NoError(t, err)

	_, err = kernel.FindFreePortForTest(a, domain.SpeedSuperPlus)
	require.ErrorIs(t, err, domain.ErrNoFreePort)
	require.ErrorContains(t, err, "SuperSpeed+")
}

// TestListPorts_ReturnsAllRowsIncludingFree confirms ListPorts surfaces
// free rows as well as used ones. SS row lives in the flat 8..15 range
// of a default single-controller build.
func TestListPorts_ReturnsAllRowsIncludingFree(t *testing.T) {
	t.Parallel()

	status := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n" +
		"hs  0001 003 003 01020304 000005 1-1\n" +
		"ss  0008 003 005 01020304 000005 1-1\n"

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

// TestListPorts_TrustsFlatPortSingleController pins the trust-flat
// port contract for the single-controller case. The parser emits
// each row's PortID verbatim from the "port" column — the kernel
// already computed the flat index via status_show_vhci and a
// controllerIdx-based re-offset would double-count. With nports=16
// (one controller, default VHCI_HC_PORTS=8), the HS row at flat port
// 0 and the SS row at flat port 8 surface as domain.PortID(0) and
// domain.PortID(8) respectively.
func TestListPorts_TrustsFlatPortSingleController(t *testing.T) {
	t.Parallel()

	status := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n" +
		"ss  0008 000 000 00000000 000000 0-0\n"

	a, err := kernel.NewImporterAdapter(kernel.WithFS(statusFS(status, nil, 16)))
	require.NoError(t, err)

	ports, err := a.ListPorts(context.Background())
	require.NoError(t, err)
	require.Len(t, ports, 2)
	require.EqualValues(t, 0, ports[0].ID, "HS row's flat port is trusted verbatim")
	require.EqualValues(t, 8, ports[1].ID, "SS row's flat port is trusted verbatim")
}

// TestListPorts_TrustsFlatPortMultiController pins the trust-flat
// port contract across two controllers. Every row's PortID comes out
// exactly as the kernel wrote it: any controllerIdx-based offset
// would push status.1's flat-16 row to 32 and its flat-24 row to 40
// (nports=32, default VHCI_HC_PORTS=8, 2 controllers).
func TestListPorts_TrustsFlatPortMultiController(t *testing.T) {
	t.Parallel()

	primary := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n" +
		"ss  0008 000 000 00000000 000000 0-0\n"

	secondary := "hs  0016 000 000 00000000 000000 0-0\n" +
		"ss  0024 000 000 00000000 000000 0-0\n"

	mfs := statusFS(primary, map[int]string{1: secondary}, 32)

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	ports, err := a.ListPorts(context.Background())
	require.NoError(t, err)
	require.Len(t, ports, 4)
	require.EqualValues(t, 0, ports[0].ID)
	require.EqualValues(t, 8, ports[1].ID)
	require.EqualValues(t, 16, ports[2].ID, "no +controllerIdx*16 double-add on status.1 HS row")
	require.EqualValues(t, 24, ports[3].ID, "no +controllerIdx*16 double-add on status.1 SS row")
}

// TestListPorts_NonDefaultHCPorts pins the trust-flat port contract
// for a kernel built with VHCI_HC_PORTS=4 (nports=16, 2 controllers).
// A hardcoded vhciPortsPerController=16 would over-count by 4× for
// every status.1 row; only a topology-sourced VHCIPorts (HCPorts*2 =
// 8 in this fixture) correctly renders the kernel's already-flat
// values. status.0 rows are flat 0..7 and status.1 rows are flat
// 8..15; the parser emits every row exactly as written.
func TestListPorts_NonDefaultHCPorts(t *testing.T) {
	t.Parallel()

	// status.0: HS flat 0, SS flat 4 (HCPorts=4 → SS offset is 4).
	primary := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n" +
		"ss  0004 000 000 00000000 000000 0-0\n"

	// status.1: HS flat 8 (pdev_nr=1 * VHCI_PORTS=8), SS flat 12.
	secondary := "hs  0008 000 000 00000000 000000 0-0\n" +
		"ss  0012 000 000 00000000 000000 0-0\n"

	mfs := statusFS(primary, map[int]string{1: secondary}, 16)

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	ports, err := a.ListPorts(context.Background())
	require.NoError(t, err)
	require.Len(t, ports, 4)
	require.EqualValues(t, 0, ports[0].ID)
	require.EqualValues(t, 4, ports[1].ID)
	require.EqualValues(t, 8, ports[2].ID, "non-default VHCI_HC_PORTS=4: status.1 HS at flat 8")
	require.EqualValues(t, 12, ports[3].ID, "non-default VHCI_HC_PORTS=4: status.1 SS at flat 12")
}

// TestListPorts_ToleratesIncompleteBusMap pins the status-reading /
// BusMap independence contract: the status-reading path (ListPorts /
// findFreePort / readStatusRows) needs only the controller count and
// the per-controller VHCI_PORTS stride — it does NOT consume the
// BusMap. An incomplete usb* child layout (e.g. a controller still
// mid-probe, or one hub vanishing around a hot-unplug race) must not
// hard-fail ListPorts because the BusMap is irrelevant to row
// parsing. Only BusMap-dependent paths (uevent mapping) surface
// errTopologyIncomplete.
//
// Fixture: nports=16 on a single controller, a valid status file with
// two well-formed rows, but ONLY usb1 present (the SS sibling is
// missing). ListPorts must succeed with the parsed rows; the BusMap
// check stays on the full-topology path used by uevent consumers.
func TestListPorts_ToleratesIncompleteBusMap(t *testing.T) {
	t.Parallel()

	mfs := fstest.MapFS{
		"sys/module/usbip_core":                       {Mode: fs.ModeDir},
		"sys/module/vhci_hcd":                         {Mode: fs.ModeDir},
		"sys/devices/platform/vhci_hcd.0":             {Mode: fs.ModeDir},
		"sys/devices/platform/vhci_hcd.0/nports":      {Data: []byte("16\n")},
		"sys/devices/platform/vhci_hcd.0/usb1/busnum": {Data: []byte("1\n")},
		// Note: no usb2 entry — the SS sibling is missing. The full
		// Topology would fail len(BusMap)==2; the StatusTopology must
		// not care.
		"sys/devices/platform/vhci_hcd.0/status": {Data: []byte(
			"hub port sta spd dev      sockfd local_busid\n" +
				"hs  0000 000 000 00000000 000000 0-0\n" +
				"ss  0008 000 000 00000000 000000 0-0\n",
		)},
	}

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	ports, err := a.ListPorts(context.Background())
	require.NoError(t, err,
		"ListPorts must succeed on an incomplete BusMap — status-reading does not consume BusMap")
	require.Len(t, ports, 2, "both rows must surface despite the missing usb2 sibling")
	require.EqualValues(t, 0, ports[0].ID)
	require.EqualValues(t, 8, ports[1].ID)
}

// TestParseStatusFile_GuardsZeroVHCIPorts pins the
// zero-VHCIPorts defense-in-depth guard: parseStatusFile must never
// execute `port / vhciPorts` when vhciPorts is zero. Topology-layer
// enforcement is the first line, but parseStatusFile is the eventual
// user of the value and must refuse the call with a clear error
// rather than panic with integer division by zero. The body is a
// single well-formed row so the test pins the guard itself, not a
// tokenisation side effect.
func TestParseStatusFile_GuardsZeroVHCIPorts(t *testing.T) {
	t.Parallel()

	mfs := statusFS("", nil, 16)

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	body := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n"

	require.NotPanics(t, func() {
		_, perr := kernel.ParseStatusFileForTest(a, body, "status", 0, 0)
		require.Error(t, perr, "vhciPorts=0 must surface an error, not a division-by-zero panic")
	}, "parseStatusFile must guard a zero vhciPorts input before dividing by it")
}

// TestListPorts_RowInWrongFileErrors pins the per-file block
// consistency contract: a row whose declared flat port does not
// belong to its controller's block (port / VHCIPorts
// != controllerIdx) is a kernel-state inconsistency. The adapter must
// refuse the whole call instead of silently surfacing an out-of-range
// port that downstream attach/detach logic would trust. Here status.1
// claims flat port 5, which belongs to controller 0 (0 <= 5 < 16) and
// violates the per-file invariant.
func TestListPorts_RowInWrongFileErrors(t *testing.T) {
	t.Parallel()

	primary := "hub port sta spd dev      sockfd local_busid\n" +
		"hs  0000 000 000 00000000 000000 0-0\n"

	// Row claims flat port 5, but status.1 owns flat 16..31 on
	// default VHCI_HC_PORTS=8.
	secondary := "hs  0005 000 000 00000000 000000 0-0\n"

	mfs := statusFS(primary, map[int]string{1: secondary}, 32)

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	_, err = a.ListPorts(context.Background())
	require.Error(t, err, "a row whose flat port falls outside its controller's block must error")
}
