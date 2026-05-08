//go:build linux

package kernel

import (
	"io/fs"
	"log/slog"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// DiscoverTopologyForTest exposes the unexported discoverTopology so
// topology_test.go can drive a MapFS-backed discovery directly without
// constructing an adapter. The production code keeps discoverTopology
// internal so tasks 2+ route every topology read through the cached
// LoadTopologyForTest path.
func DiscoverTopologyForTest(fsys fs.FS) (Topology, error) {
	return discoverTopology(fsys)
}

// LoadTopologyForTest exposes the ImporterAdapter's loadTopology method
// so an integration-shaped test can confirm a freshly-constructed
// adapter surfaces the sysfs topology via its injected fs.FS without
// any Task 2+ consumers wired yet.
func LoadTopologyForTest(a *ImporterAdapter) (Topology, error) {
	return a.loadTopology()
}

// CommonExport mirrors commonAdapter for white-box tests. Keeping a
// separate exported shape (rather than exposing commonAdapter directly)
// lets the tests assert field identity without widening the production
// API surface.
type CommonExport struct {
	FS          fs.FS
	Write       WriteFunc
	NetlinkDial NetlinkDialer
	Logger      *slog.Logger
	Clock       app.Clock
}

// ExportCommonFromImporter reveals the injected common state inside an
// ImporterAdapter for test assertions.
func ExportCommonFromImporter(a *ImporterAdapter) CommonExport {
	return commonSnapshot(a.commonAdapter)
}

// ExportCommonFromExporter reveals the injected common state inside an
// ExporterAdapter for test assertions.
func ExportCommonFromExporter(a *ExporterAdapter) CommonExport {
	return commonSnapshot(a.commonAdapter)
}

// ExportCommonFromEvents reveals the injected common state inside an
// EventsAdapter for test assertions.
func ExportCommonFromEvents(a *EventsAdapter) CommonExport {
	return commonSnapshot(a.commonAdapter)
}

func commonSnapshot(c commonAdapter) CommonExport {
	return CommonExport{
		FS:          c.fs,
		Write:       c.write,
		NetlinkDial: c.nlDial,
		Logger:      c.logger,
		Clock:       c.clock,
	}
}

// FindFreePortForTest exposes the internal findFreePort for unit
// tests. Role adapters share the algorithm via commonAdapter so the
// ImporterAdapter parameter is arbitrary — either role would produce
// identical results.
func FindFreePortForTest(a *ImporterAdapter, speed domain.Speed) (domain.PortID, error) {
	return a.findFreePort(speed)
}

// ExtractPortFromBusIDForTest exposes the unexported extractPortFromBusID
// helper so uevent tests can lock in the parsing table (Pass-4 RANK 1).
// The function has no dependencies on adapter state — passing it by
// name through a thin white-box trampoline keeps the production API
// surface closed.
func ExtractPortFromBusIDForTest(busID string) domain.PortID {
	return extractPortFromBusID(busID)
}
