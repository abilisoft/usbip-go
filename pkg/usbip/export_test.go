package usbip

import internalapp "github.com/abilisoft/usbip-go/internal/app"

// NewImporterFromInternalForTest wraps an already-constructed internal
// Importer in a public *Importer so facade tests can exercise forwarding
// without exposing adapter injection on the public surface. Consumers
// can never reach this helper — it lives in an _test.go file.
func NewImporterFromInternalForTest(inner *internalapp.Importer) *Importer {
	return &Importer{inner: inner}
}

// NewExporterFromInternalForTest mirrors NewImporterFromInternalForTest
// for the Exporter wrapper.
func NewExporterFromInternalForTest(inner *internalapp.Exporter) *Exporter {
	return &Exporter{inner: inner}
}
