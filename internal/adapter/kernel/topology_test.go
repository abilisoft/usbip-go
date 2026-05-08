//go:build linux

package kernel_test

import (
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// topoFS builds a MapFS that models the vhci_hcd platform-device
// subtree. files maps absolute paths to file contents. Directory
// existence is inferred by fstest.MapFS from the file entries (a file
// at "a/b/c" implies "a" and "a/b"); tests that need empty directories
// can insert a zero-data entry at that path.
func topoFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}

	for p, d := range files {
		rel := p
		if len(rel) > 0 && rel[0] == '/' {
			rel = rel[1:]
		}

		m[rel] = &fstest.MapFile{Data: []byte(d)}
	}

	return m
}

// errFS wraps an fs.FS and returns errOn for every Open whose
// fs-relative path matches block. Used to synthesise a non-ENOENT
// failure on a specific sysfs attribute without touching the real
// filesystem.
type errFS struct {
	inner fs.FS
	block string
	errOn error
}

// Open implements fs.FS. When the requested name equals the blocked
// path, the pre-canned error is returned; all other opens are
// delegated to the inner fs.FS. The inner error is wrapped in a
// PathError so the caller sees the same shape io/fs users expect from
// real filesystems.
func (e errFS) Open(name string) (fs.File, error) {
	if name == e.block {
		return nil, &fs.PathError{Op: "open", Path: name, Err: e.errOn}
	}

	f, err := e.inner.Open(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}

	return f, nil
}

// TestDiscoverTopology_SingleControllerDefault covers the default
// kernel configuration: one vhci_hcd.* platform device, VHCI_HC_PORTS=8,
// nports=16 (hs + ss), usb1 (hs) + usb2 (ss).
func TestDiscoverTopology_SingleControllerDefault(t *testing.T) {
	t.Parallel()

	mfs := topoFS(map[string]string{
		"/sys/devices/platform/vhci_hcd.0/nports":      "16\n",
		"/sys/devices/platform/vhci_hcd.0/status":      "",
		"/sys/devices/platform/vhci_hcd.0/usb1/busnum": "1\n",
		"/sys/devices/platform/vhci_hcd.0/usb1/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/speed":  "5000\n",
	})

	topo, err := kernel.DiscoverTopologyForTest(mfs)
	require.NoError(t, err)
	require.EqualValues(t, 1, topo.NControllers)
	require.EqualValues(t, 8, topo.HCPorts)
	require.EqualValues(t, 16, topo.VHCIPorts)
	require.Len(t, topo.BusMap, 2)
	require.Equal(t, kernel.VHCILocation{ControllerIdx: 0, Hub: kernel.HubTypeHS}, topo.BusMap[1])
	require.Equal(t, kernel.VHCILocation{ControllerIdx: 0, Hub: kernel.HubTypeSS}, topo.BusMap[2])
}

// TestDiscoverTopology_MultiController covers two vhci_hcd.* controllers,
// each with its own hs+ss pair. Controller probing stops at the first
// missing status.<N> file, so status + status.1 must both exist, and
// status.2 must be absent.
func TestDiscoverTopology_MultiController(t *testing.T) {
	t.Parallel()

	mfs := topoFS(map[string]string{
		"/sys/devices/platform/vhci_hcd.0/nports":      "32\n",
		"/sys/devices/platform/vhci_hcd.0/status":      "",
		"/sys/devices/platform/vhci_hcd.0/status.1":    "",
		"/sys/devices/platform/vhci_hcd.0/usb1/busnum": "1\n",
		"/sys/devices/platform/vhci_hcd.0/usb1/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/speed":  "5000\n",
		"/sys/devices/platform/vhci_hcd.1/usb3/busnum": "3\n",
		"/sys/devices/platform/vhci_hcd.1/usb3/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.1/usb4/busnum": "4\n",
		"/sys/devices/platform/vhci_hcd.1/usb4/speed":  "5000\n",
	})

	topo, err := kernel.DiscoverTopologyForTest(mfs)
	require.NoError(t, err)
	require.EqualValues(t, 2, topo.NControllers)
	require.EqualValues(t, 8, topo.HCPorts)
	require.EqualValues(t, 16, topo.VHCIPorts)
	require.Len(t, topo.BusMap, 4)
	require.Equal(t, kernel.VHCILocation{ControllerIdx: 0, Hub: kernel.HubTypeHS}, topo.BusMap[1])
	require.Equal(t, kernel.VHCILocation{ControllerIdx: 0, Hub: kernel.HubTypeSS}, topo.BusMap[2])
	require.Equal(t, kernel.VHCILocation{ControllerIdx: 1, Hub: kernel.HubTypeHS}, topo.BusMap[3])
	require.Equal(t, kernel.VHCILocation{ControllerIdx: 1, Hub: kernel.HubTypeSS}, topo.BusMap[4])
}

// TestDiscoverTopology_NonDefaultHCPorts covers a kernel built with
// VHCI_HC_PORTS=4. nports=16 with 2 controllers means HCPorts=4,
// VHCIPorts=8.
func TestDiscoverTopology_NonDefaultHCPorts(t *testing.T) {
	t.Parallel()

	mfs := topoFS(map[string]string{
		"/sys/devices/platform/vhci_hcd.0/nports":      "16\n",
		"/sys/devices/platform/vhci_hcd.0/status":      "",
		"/sys/devices/platform/vhci_hcd.0/status.1":    "",
		"/sys/devices/platform/vhci_hcd.0/usb1/busnum": "1\n",
		"/sys/devices/platform/vhci_hcd.0/usb1/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/speed":  "5000\n",
		"/sys/devices/platform/vhci_hcd.1/usb3/busnum": "3\n",
		"/sys/devices/platform/vhci_hcd.1/usb3/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.1/usb4/busnum": "4\n",
		"/sys/devices/platform/vhci_hcd.1/usb4/speed":  "5000\n",
	})

	topo, err := kernel.DiscoverTopologyForTest(mfs)
	require.NoError(t, err)
	require.EqualValues(t, 2, topo.NControllers)
	require.EqualValues(t, 4, topo.HCPorts)
	require.EqualValues(t, 8, topo.VHCIPorts)
}

// TestDiscoverTopology_VHCINotFirstHCD covers the live-host scenario
// where another HCD (e.g. xhci) already owns bus 1. vhci_hcd.0 owns
// usb2 (hs) + usb3 (ss); bus 1 is absent from the topology.
func TestDiscoverTopology_VHCINotFirstHCD(t *testing.T) {
	t.Parallel()

	mfs := topoFS(map[string]string{
		"/sys/devices/platform/vhci_hcd.0/nports":      "16\n",
		"/sys/devices/platform/vhci_hcd.0/status":      "",
		"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.0/usb3/busnum": "3\n",
		"/sys/devices/platform/vhci_hcd.0/usb3/speed":  "5000\n",
	})

	topo, err := kernel.DiscoverTopologyForTest(mfs)
	require.NoError(t, err)
	require.EqualValues(t, 1, topo.NControllers)
	require.Len(t, topo.BusMap, 2)
	require.Equal(t, kernel.VHCILocation{ControllerIdx: 0, Hub: kernel.HubTypeHS}, topo.BusMap[2])
	require.Equal(t, kernel.VHCILocation{ControllerIdx: 0, Hub: kernel.HubTypeSS}, topo.BusMap[3])

	_, ok := topo.BusMap[1]
	require.False(t, ok, "bus 1 is owned by a non-vhci HCD and must not appear")
}

// TestDiscoverTopology_InconsistentNPorts covers the sysfs-consistency
// guard: nports must be divisible by (nControllers * 2). nports=17 is
// indivisible with any controller count.
func TestDiscoverTopology_InconsistentNPorts(t *testing.T) {
	t.Parallel()

	mfs := topoFS(map[string]string{
		"/sys/devices/platform/vhci_hcd.0/nports": "17\n",
		"/sys/devices/platform/vhci_hcd.0/status": "",
	})

	_, err := kernel.DiscoverTopologyForTest(mfs)
	require.Error(t, err)
}

// TestDiscoverTopology_MissingNPorts covers the failure mode where the
// vhci_hcd.0/nports attribute is absent. Must surface an error — a
// missing file means the module is not loaded or the sysfs mount is
// stale.
func TestDiscoverTopology_MissingNPorts(t *testing.T) {
	t.Parallel()

	mfs := topoFS(nil)

	_, err := kernel.DiscoverTopologyForTest(mfs)
	require.Error(t, err)
}

// TestTopology_FlatPort exercises the FlatPort algebra for both hub
// kinds and both controllers. HS hub offset is 0, SS hub offset is
// HCPorts; controllerIdx shifts by VHCIPorts.
func TestTopology_FlatPort(t *testing.T) {
	t.Parallel()

	mfs := topoFS(map[string]string{
		"/sys/devices/platform/vhci_hcd.0/nports":      "32\n",
		"/sys/devices/platform/vhci_hcd.0/status":      "",
		"/sys/devices/platform/vhci_hcd.0/status.1":    "",
		"/sys/devices/platform/vhci_hcd.0/usb1/busnum": "1\n",
		"/sys/devices/platform/vhci_hcd.0/usb1/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/speed":  "5000\n",
		"/sys/devices/platform/vhci_hcd.1/usb3/busnum": "3\n",
		"/sys/devices/platform/vhci_hcd.1/usb3/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.1/usb4/busnum": "4\n",
		"/sys/devices/platform/vhci_hcd.1/usb4/speed":  "5000\n",
	})

	topo, err := kernel.DiscoverTopologyForTest(mfs)
	require.NoError(t, err)

	require.Equal(t, domain.PortID(0),
		topo.FlatPort(kernel.VHCILocation{ControllerIdx: 0, Hub: kernel.HubTypeHS}, 0))
	require.Equal(t, domain.PortID(8),
		topo.FlatPort(kernel.VHCILocation{ControllerIdx: 0, Hub: kernel.HubTypeSS}, 0))
	require.Equal(t, domain.PortID(16),
		topo.FlatPort(kernel.VHCILocation{ControllerIdx: 1, Hub: kernel.HubTypeHS}, 0))
	require.Equal(t, domain.PortID(25),
		topo.FlatPort(kernel.VHCILocation{ControllerIdx: 1, Hub: kernel.HubTypeSS}, 1))
}

// TestDiscoverTopology_StatusFilePermissionErrorSurfaces pins BUG 2:
// the controller probe must propagate any non-ENOENT failure opening a
// status.N file instead of silently treating the file as present and
// continuing. A permission-denied read would otherwise either terminate
// enumeration with a wrong controller count or fold the probe into a
// downstream "sysfs is healthy" success that masks the real failure.
//
// Fixture: the usual base layout plus a status.1 entry that the fake
// fs.FS refuses to open with fs.ErrPermission. discoverTopology must
// return a non-nil error that wraps fs.ErrPermission.
func TestDiscoverTopology_StatusFilePermissionErrorSurfaces(t *testing.T) {
	t.Parallel()

	inner := topoFS(map[string]string{
		"/sys/devices/platform/vhci_hcd.0/nports":      "32\n",
		"/sys/devices/platform/vhci_hcd.0/status":      "",
		"/sys/devices/platform/vhci_hcd.0/status.1":    "",
		"/sys/devices/platform/vhci_hcd.0/usb1/busnum": "1\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		"/sys/devices/platform/vhci_hcd.1/usb3/busnum": "3\n",
		"/sys/devices/platform/vhci_hcd.1/usb4/busnum": "4\n",
	})

	fake := errFS{
		inner: inner,
		block: "sys/devices/platform/vhci_hcd.0/status.1",
		errOn: fs.ErrPermission,
	}

	_, err := kernel.DiscoverTopologyForTest(fake)
	require.Error(t, err, "permission-denied on status.N must surface as an error")
	require.ErrorIs(t, err, fs.ErrPermission,
		"the wrapped error must chain back to fs.ErrPermission")
}

// TestDiscoverTopology_SupportsManyControllers pins BUG 4: the probe
// loop must not cap at a hardcoded controller count. The natural stop
// signal is the first ENOENT on status.<i>; any arbitrary cap silently
// truncates large deployments. The fixture here installs 20
// controllers (nports = 20 * 2 * 8 = 320) with the expected two usb
// children per controller and asserts full discovery.
func TestDiscoverTopology_SupportsManyControllers(t *testing.T) {
	t.Parallel()

	const (
		controllers  = 20
		portsPerHub  = 8
		hubsPerCtrl  = 2
		totalBusmaps = controllers * hubsPerCtrl
		nports       = controllers * hubsPerCtrl * portsPerHub
	)

	files := map[string]string{
		"/sys/devices/platform/vhci_hcd.0/nports": fmt.Sprintf("%d\n", nports),
		"/sys/devices/platform/vhci_hcd.0/status": "",
	}

	for i := 1; i < controllers; i++ {
		files[fmt.Sprintf("/sys/devices/platform/vhci_hcd.0/status.%d", i)] = ""
	}

	for i := range controllers {
		hsBus := 2*i + 1
		ssBus := 2*i + 2

		files[fmt.Sprintf("/sys/devices/platform/vhci_hcd.%d/usb%d/busnum", i, hsBus)] =
			fmt.Sprintf("%d\n", hsBus)
		files[fmt.Sprintf("/sys/devices/platform/vhci_hcd.%d/usb%d/busnum", i, ssBus)] =
			fmt.Sprintf("%d\n", ssBus)
	}

	mfs := topoFS(files)

	topo, err := kernel.DiscoverTopologyForTest(mfs)
	require.NoError(t, err)
	require.EqualValues(t, controllers, topo.NControllers)
	require.Len(t, topo.BusMap, totalBusmaps)
}

// TestDiscoverTopology_IncompleteControllerErrors pins BUG 3: the
// post-condition len(BusMap) == NControllers * hubsPerController must
// hold — a controller that exposes only one usb child (the other is
// mid-probe, the sysfs is partially populated, or the kernel is in an
// unsupported state) must surface as an error rather than a silent
// partial topology that downstream flat-port arithmetic trusts as
// authoritative.
//
// Two fixtures exercise the two shapes: (a) a single controller with
// only one usb child; (b) two controllers where the second controller
// has no usb children at all.
func TestDiscoverTopology_IncompleteControllerErrors(t *testing.T) {
	t.Parallel()

	t.Run("single controller missing one hub", func(t *testing.T) {
		t.Parallel()

		mfs := topoFS(map[string]string{
			"/sys/devices/platform/vhci_hcd.0/nports":      "16\n",
			"/sys/devices/platform/vhci_hcd.0/status":      "",
			"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		})

		_, err := kernel.DiscoverTopologyForTest(mfs)
		require.Error(t, err,
			"a controller with fewer than hubsPerController usb children is incomplete")
	})

	t.Run("second controller has zero hubs", func(t *testing.T) {
		t.Parallel()

		mfs := topoFS(map[string]string{
			"/sys/devices/platform/vhci_hcd.0/nports":      "32\n",
			"/sys/devices/platform/vhci_hcd.0/status":      "",
			"/sys/devices/platform/vhci_hcd.0/status.1":    "",
			"/sys/devices/platform/vhci_hcd.0/usb1/busnum": "1\n",
			"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		})

		_, err := kernel.DiscoverTopologyForTest(mfs)
		require.Error(t, err,
			"nControllers * hubsPerController must equal len(BusMap)")
	})
}

// TestDiscoverTopology_ClassifyHubBySiblingOrder pins BUG 1: when the
// speed attribute is absent or empty, classification must fall back to
// sibling order (lower busnum = HS, higher busnum = SS) because
// vhci_hcd_probe registers the HS root hub before the SS root hub for
// every controller. The pre-fix implementation returned HubTypeHS
// unconditionally in the missing-speed path, which silently collapsed
// both hubs to HS and would corrupt every downstream flat-port
// calculation.
func TestDiscoverTopology_ClassifyHubBySiblingOrder(t *testing.T) {
	t.Parallel()

	mfs := topoFS(map[string]string{
		"/sys/devices/platform/vhci_hcd.0/nports":      "16\n",
		"/sys/devices/platform/vhci_hcd.0/status":      "",
		"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/speed":  "",
		"/sys/devices/platform/vhci_hcd.0/usb3/busnum": "3\n",
		"/sys/devices/platform/vhci_hcd.0/usb3/speed":  "",
	})

	topo, err := kernel.DiscoverTopologyForTest(mfs)
	require.NoError(t, err)
	require.Len(t, topo.BusMap, 2)
	require.Equal(t, kernel.VHCILocation{ControllerIdx: 0, Hub: kernel.HubTypeHS}, topo.BusMap[2],
		"lower busnum within a controller must classify as HS regardless of missing speed")
	require.Equal(t, kernel.VHCILocation{ControllerIdx: 0, Hub: kernel.HubTypeSS}, topo.BusMap[3],
		"higher busnum within a controller must classify as SS regardless of missing speed")
}

// countingFS wraps an fs.FS and increments a counter every time Open
// is called on a name matching the requested path. Used to pin BUG 5:
// loadTopology must run discoverTopology at most once per adapter
// instance.
type countingFS struct {
	inner   fs.FS
	watch   string
	counter *int
}

// Open implements fs.FS. Open calls against the watched name bump the
// counter atomically by using a single-goroutine test driver so no
// sync primitive is needed.
func (c countingFS) Open(name string) (fs.File, error) {
	if name == c.watch {
		*c.counter++
	}

	f, err := c.inner.Open(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}

	return f, nil
}

// TestCommonAdapter_TopologyCached pins BUG 5: calling loadTopology N
// times on the same adapter must read the sysfs nports attribute once,
// not N times. Re-reading on every call races a live kernel's topology
// and, more importantly, makes Task 2+ consumers (attach/detach,
// status renumbering) pay a full sysfs walk for every port operation.
func TestCommonAdapter_TopologyCached(t *testing.T) {
	t.Parallel()

	inner := topoFS(map[string]string{
		"/sys/devices/platform/vhci_hcd.0/nports":      "16\n",
		"/sys/devices/platform/vhci_hcd.0/status":      "",
		"/sys/devices/platform/vhci_hcd.0/usb1/busnum": "1\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
	})

	var count int

	fake := countingFS{
		inner:   inner,
		watch:   "sys/devices/platform/vhci_hcd.0/nports",
		counter: &count,
	}

	a, err := kernel.NewImporterAdapter(kernel.WithFS(fake))
	require.NoError(t, err)

	const calls = 5

	var first kernel.Topology

	for i := range calls {
		topo, terr := kernel.LoadTopologyForTest(a)
		require.NoError(t, terr)

		if i == 0 {
			first = topo
			continue
		}

		require.True(t, topologiesEqual(first, topo),
			"loadTopology must return the same cached value on call %d", i+1)
	}

	require.Equal(t, 1, count,
		"loadTopology must open nports exactly once across %d calls", calls)
}

// topologiesEqual compares two Topology values for deep equality. Used
// by the cache test to confirm repeated calls yield the identical
// snapshot without reflecting through the whole reflect package.
func topologiesEqual(a, b kernel.Topology) bool {
	if a.NControllers != b.NControllers || a.HCPorts != b.HCPorts || a.VHCIPorts != b.VHCIPorts {
		return false
	}

	if len(a.BusMap) != len(b.BusMap) {
		return false
	}

	for k, v := range a.BusMap {
		other, ok := b.BusMap[k]
		if !ok || other != v {
			return false
		}
	}

	return true
}

// TestImporterAdapter_LoadTopology confirms the importer adapter
// exposes a cached topology post-construction. Task 2 and later consume
// this cached value rather than re-reading sysfs on every call.
func TestImporterAdapter_LoadTopology(t *testing.T) {
	t.Parallel()

	mfs := topoFS(map[string]string{
		"/sys/devices/platform/vhci_hcd.0/nports":      "16\n",
		"/sys/devices/platform/vhci_hcd.0/status":      "",
		"/sys/devices/platform/vhci_hcd.0/usb1/busnum": "1\n",
		"/sys/devices/platform/vhci_hcd.0/usb1/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/speed":  "5000\n",
	})

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	topo, err := kernel.LoadTopologyForTest(a)
	require.NoError(t, err)
	require.EqualValues(t, 1, topo.NControllers)
	require.EqualValues(t, 8, topo.HCPorts)
	require.EqualValues(t, 16, topo.VHCIPorts)
	require.Equal(t, kernel.VHCILocation{ControllerIdx: 0, Hub: kernel.HubTypeHS}, topo.BusMap[1])
	require.Equal(t, kernel.VHCILocation{ControllerIdx: 0, Hub: kernel.HubTypeSS}, topo.BusMap[2])
}
