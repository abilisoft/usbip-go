// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

// Shared application fixtures keep exporter and importer lifecycle tests on
// identical representative kernel paths and session-end reasons.
const (
	testKernelSessionEndReason = "kernel session-end"
	testRootDevicePath         = "/sys/devices/pci/usb1/1-1"
)
