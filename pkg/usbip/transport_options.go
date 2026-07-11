// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip

import "github.com/abilisoft/usbip-go/internal/netopts"

// TransportOptions carries TCP tuning for importer dials and
// exporter-owned listeners. It remains an alias for compatibility with the
// v1 API, whose exported type identity is recorded by the API baseline.
//
// Every zero value inherits the Go/kernel default. Negative values are
// rejected by NewImporter and NewExporter with
// ErrTransportOptionsInvalid.
type TransportOptions = netopts.TransportOptions
