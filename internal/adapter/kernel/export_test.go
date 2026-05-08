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

// ParseStatusFileForTest exposes the unexported parseStatusFile so
// ports-level tests can pin contract edges (e.g. defensive guards
// against vhciPorts=0) without synthesising a full adapter + sysfs
// layout. The adapter parameter supplies the logger and fs.FS that
// parseStatusFile threads through its Warn path.
func ParseStatusFileForTest(
	a *ImporterAdapter, body, source string, controllerIdx, vhciPorts uint32,
) ([]ParsedPortForTest, error) {
	rows, err := a.parseStatusFile(body, source, controllerIdx, vhciPorts)
	if err != nil {
		return nil, err
	}

	out := make([]ParsedPortForTest, 0, len(rows))
	for _, r := range rows {
		out = append(out, ParsedPortForTest{
			Hub:    r.hub,
			Port:   r.port,
			Status: r.status,
			Speed:  r.speed,
			DevID:  r.devID,
			BusID:  r.busID,
		})
	}

	return out, nil
}

// ParsedPortForTest mirrors parsedPort's fields through exported
// names. The production type stays unexported so callers cannot depend
// on its representation; tests consume this shape for assertions.
type ParsedPortForTest struct {
	Hub    string
	Port   domain.PortID
	Status domain.Status
	Speed  domain.Speed
	DevID  domain.DeviceID
	BusID  domain.BusID
}

// VHCIEventMapperForTest is the test-side façade over the internal
// vhciEventMapper. It carries the full Topology the mapper uses so
// unit tests can drive MapEventForTest against a field map without
// standing up an EventsAdapter.
type VHCIEventMapperForTest struct {
	inner vhciEventMapper
}

// NewVHCIEventMapperForTest constructs a mapper against the supplied
// topology snapshot. Mirrors the internal newVHCIEventMapper.
func NewVHCIEventMapperForTest(topo Topology) VHCIEventMapperForTest {
	return VHCIEventMapperForTest{inner: newVHCIEventMapper(topo)}
}

// MapEventForTest calls the internal mapEvent and returns its
// (domain.Event, bool) pair verbatim.
func (m VHCIEventMapperForTest) MapEventForTest(fields map[string]string) (domain.Event, bool) {
	return m.inner.mapEvent(fields)
}
