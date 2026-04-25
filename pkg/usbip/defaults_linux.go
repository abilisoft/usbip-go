// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package usbip

import (
	"fmt"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/internal/adapter/transport"
	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	internalapp "github.com/abilisoft/usbip-go/internal/app"
)

// newDefaultImporter assembles the Linux production defaults for a
// public-facing *Importer and then applies any caller-supplied options.
// Adapter construction lives here (not in options.go) so the public
// surface does not leak internal types to non-Linux consumers when
// defaults_other.go is the compiled unit.
func newDefaultImporter(opts []ImporterOption) (*Importer, error) {
	cfg := importerConfig{}

	// Skip nil option funcs so callers can compose With* helpers
	// conditionally (e.g. `opt := cond ? With... : nil`) without a
	// runtime panic. Go convention tolerates nil in variadic slots; see
	// http.Handler composition for a standard-library precedent.
	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(&cfg)
	}

	k, err := kernel.NewImporterAdapter()
	if err != nil {
		return nil, fmt.Errorf("build importer kernel adapter: %w", err)
	}

	e, err := kernel.NewEventsAdapter()
	if err != nil {
		return nil, fmt.Errorf("build events adapter: %w", err)
	}

	extra := importerConfigToInternal(cfg)

	// Preallocate the full slice (4 base deps + any option-derived
	// extras) so prealloc is satisfied and the variadic NewImporter
	// receives a single backing array.
	baseOpts := make([]internalapp.ImporterOption, 0, importerBaseOptCount+len(extra))

	baseOpts = append(baseOpts,
		internalapp.WithImporterKernel(k),
		internalapp.WithImporterEvents(e),
		internalapp.WithImporterTransport(transport.New()),
		internalapp.WithImporterCodec(&wire.Codec{}),
	)
	baseOpts = append(baseOpts, extra...)

	return &Importer{
		inner: internalapp.NewImporter(baseOpts...),
		cfg:   cfg,
	}, nil
}

// importerBaseOptCount is the number of required-adapter options that
// newDefaultImporter always supplies (kernel, events, transport, codec).
const importerBaseOptCount = 4

// newDefaultExporter mirrors newDefaultImporter for the Exporter role.
func newDefaultExporter(opts []ExporterOption) (*Exporter, error) {
	cfg := exporterConfig{}

	// Skip nil option funcs per the rationale on newDefaultImporter.
	for _, opt := range opts {
		if opt == nil {
			continue
		}

		opt(&cfg)
	}

	k, err := kernel.NewExporterAdapter()
	if err != nil {
		return nil, fmt.Errorf("build exporter kernel adapter: %w", err)
	}

	e, err := kernel.NewEventsAdapter()
	if err != nil {
		return nil, fmt.Errorf("build events adapter: %w", err)
	}

	extra := exporterConfigToInternal(cfg)

	baseOpts := make([]internalapp.ExporterOption, 0, exporterBaseOptCount+len(extra))

	baseOpts = append(baseOpts,
		internalapp.WithExporterKernel(k),
		internalapp.WithExporterEvents(e),
		internalapp.WithExporterTransport(transport.New()),
		internalapp.WithExporterCodec(&wire.Codec{}),
	)
	baseOpts = append(baseOpts, extra...)

	inner, err := internalapp.NewExporterWithError(baseOpts...)
	if err != nil {
		return nil, fmt.Errorf("construct exporter: %w", err)
	}

	return &Exporter{inner: inner, cfg: cfg}, nil
}

// exporterBaseOptCount mirrors importerBaseOptCount for the exporter.
const exporterBaseOptCount = 4
