// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip

import (
	"fmt"

	internalapp "github.com/abilisoft/usbip-go/internal/app"
)

// resolveImporterConfig applies public options in declaration order and
// validates every platform-independent value before a platform implementation
// attempts to construct kernel or transport adapters.
func resolveImporterConfig(opts []ImporterOption) (importerConfig, error) {
	cfg := newImporterConfig()

	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	err := internalapp.ValidateTransportOptions(cfg.transportOptions)
	if err != nil {
		return importerConfig{}, fmt.Errorf(
			"usbip.NewImporter: %w: %s",
			ErrTransportOptionsInvalid,
			err.Error(),
		)
	}

	return cfg, nil
}

// resolveExporterConfig is the Exporter counterpart of
// resolveImporterConfig. Transport, accept-rate, and ACL validation must
// precede platform availability so unsupported hosts still report malformed
// public configuration through the documented facade sentinel.
func resolveExporterConfig(opts []ExporterOption) (exporterConfig, error) {
	cfg := exporterConfig{}

	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	err := internalapp.ValidateTransportOptions(cfg.transportOptions)
	if err != nil {
		return exporterConfig{}, fmt.Errorf(
			"usbip.NewExporter: %w: %s",
			ErrTransportOptionsInvalid,
			err.Error(),
		)
	}

	err = internalapp.ValidateExporterACL(cfg.allowCIDRs)
	if err != nil {
		return exporterConfig{}, fmt.Errorf(
			"construct exporter: %w",
			translateInternalErr(err),
		)
	}

	if cfg.acceptRateLimitSet {
		err = internalapp.ValidateAcceptRateLimit(cfg.acceptRateLimit)
		if err != nil {
			return exporterConfig{}, fmt.Errorf(
				"construct exporter: %w",
				translateInternalErr(err),
			)
		}
	}

	return cfg, nil
}
