// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

// Package transport provides context-aware TCP dial and listen primitives
// for USB/IP sessions. It wraps `net.Dialer` / `net.ListenConfig`, enables
// `TCP_NODELAY` on established connections, and surfaces errors with
// structured context so upstream layers can log or classify them.
package transport
