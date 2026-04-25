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
// See `docs/high-latency-plan.md` for the documented WAN/satellite
// presets that combine the individual fields below.
type TransportOptions = netopts.TransportOptions
