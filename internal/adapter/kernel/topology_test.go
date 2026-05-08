//go:build linux

package kernel_test

import (
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
