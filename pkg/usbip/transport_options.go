// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip

import (
	"github.com/abilisoft/usbip-go/internal/netopts"
)

// TransportOptions carries TCP-level tuning that the Importer's
// outbound dials and the Exporter's accepted listener connections
// hand to the transport adapter. The type is an alias for the
// internal value owned by `internal/netopts`, which keeps the public
// surface and the internal interface contract in lockstep without a
// shadow struct.
//
// All fields default to zero, which inherits the kernel/Go defaults
// in place before PR 1b. Validation rejects negative durations,
// probe counts, and buffer sizes at constructor time
// (NewImporter / NewExporter return ErrTransportOptionsInvalid).
//
// README.md and openspec/specs/transport-networking/spec.md document
// how the public facade applies these fields on importer dials and
// exporter-owned listeners.
type TransportOptions = netopts.TransportOptions
