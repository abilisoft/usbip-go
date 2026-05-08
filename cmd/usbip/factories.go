package main

import (
	"context"
	"fmt"
	"iter"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// Importer is the subset of *usbip.Importer methods used by the CLI
// subcommands. Keeping an interface here (rather than the concrete
// pointer) is the substitutable seam tests override via newImporter.
// The concrete *usbip.Importer satisfies this contract because its
// methods have the same signatures.
type Importer interface {
	ListRemote(ctx context.Context, r usbip.RemoteEndpoint) ([]usbip.Device, error)
	ListPorts(ctx context.Context) ([]usbip.Port, error)
	Attach(
		ctx context.Context,
		r usbip.RemoteEndpoint,
		busID usbip.BusID,
		opts usbip.AttachOptions,
	) (usbip.Port, error)
	Detach(ctx context.Context, id usbip.PortID) error
	Watch(ctx context.Context) iter.Seq[usbip.Event]
	Close() error
}

// Exporter is the subset of *usbip.Exporter methods used by the CLI
// subcommands.
type Exporter interface {
	ListAvailable(ctx context.Context) ([]usbip.Device, error)
	Bind(ctx context.Context, busID usbip.BusID) error
	Unbind(ctx context.Context, busID usbip.BusID) error
}

// newImporter is the package-level factory that constructs an Importer
// for subcommand use. Production wiring returns a *usbip.Importer built
// from the public facade; tests assign a mock-returning closure.
var newImporter = func(opts ...usbip.ImporterOption) (Importer, error) {
	imp, err := usbip.NewImporter(opts...)
	if err != nil {
		return nil, fmt.Errorf("construct importer: %w", err)
	}

	return imp, nil
}

// newExporter mirrors newImporter for the Exporter role.
var newExporter = func(opts ...usbip.ExporterOption) (Exporter, error) {
	exp, err := usbip.NewExporter(opts...)
	if err != nil {
		return nil, fmt.Errorf("construct exporter: %w", err)
	}

	return exp, nil
}
