// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

// This file documents the role-specific option guarantee from spec
// public-library-api OpenSpec via a compile-time counter-example. Uncomment the snippet to
// observe the compile error — intentionally left commented so the
// package still builds.
//
// Uncommenting the following snippet MUST produce:
//
//     cannot use usbip.WithExporterMaxSessions(1) (value of type
//     usbip.ExporterOption) as usbip.ImporterOption value in argument
//     to usbip.NewImporter
//
// The identical behaviour holds for ExporterOption arguments passed to
// NewImporter, and vice versa. The split Option types make role-mixing
// a type error rather than a runtime failure (public-library-api OpenSpec rationale).
//
// func compileTimeRoleSafety() {
// 	_, _ = usbip.NewImporter(usbip.WithExporterMaxSessions(1))
// 	_, _ = usbip.NewExporter(usbip.WithImporterStatusPollInterval(time.Second))
// }
