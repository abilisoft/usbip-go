// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

// Package usbip is the public facade for the usbip-go library. It re-exports
// the stable surface (constructors, configuration options, sentinel errors)
// and wraps the internal application services so that consumers never reach
// into the `internal/` tree.
package usbip
