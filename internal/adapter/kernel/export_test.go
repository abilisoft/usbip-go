//go:build linux

package kernel

import (
	"io/fs"
	"log/slog"

	"github.com/abilisoft/usbip-go/internal/app"
)

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
