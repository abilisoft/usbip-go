// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"github.com/abilisoft/usbip-go/internal/netopts"
)

// TransportOptions is an alias for netopts.TransportOptions. The
// underlying type lives in internal/netopts to break the import cycle
// between internal/app (which declares the consumer-defined Transport
// interface) and internal/adapter/transport (which implements it). The
// DDD layering rule forbids internal/app from importing
// internal/adapter/transport, so the value type sits in a leaf
// package both can reference.
type TransportOptions = netopts.TransportOptions

// ValidateTransportOptions delegates to netopts.Validate. Re-exported
// so callers in this package and its tests can use the app-level
// surface without an additional import. The returned error already
// wraps netopts.ErrTransportOptionsInvalid (which app re-exports as
// ErrTransportOptionsInvalid via type alias), so further wrapping
// would only obscure the field name without adding context — the
// wrapcheck exclusion in .golangci.yml documents this.
func ValidateTransportOptions(opts TransportOptions) error {
	return netopts.Validate(opts)
}
