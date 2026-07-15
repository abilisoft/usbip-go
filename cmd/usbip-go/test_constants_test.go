// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import "github.com/abilisoft/usbip-go/pkg/usbip"

// Shared CLI fixtures keep command, flag, endpoint, and rendering tests aligned
// on the same representative inputs.
const (
	testActivatedListenAddr = "127.0.0.1:1"
	testAttachCommand       = "attach"
	testBindCommand         = "bind"
	testBusIDHeader         = "BUSID"
	testCompletionCommand   = "completion"
	testDetachCommand       = "detach"
	testDrainTimeoutFlag    = "--drain-timeout"
	testEphemeralListenAddr = "127.0.0.1:0"
	testFixedBackoffKind    = "fixed"
	testHelpFlag            = "--help"
	testInstallCommand      = "install"
	testListCommand         = "list"
	testLoadedModuleState   = "loaded"
	testManufacturer        = "Kingston"
	testNestedBusID         = "1-1.2"
	testOutputJSONFlag      = "--output=json"
	testPollIntervalFlag    = "--poll-interval"
	testPollIntervalValue   = "20ms"
	testPortCommand         = "port"
	testProduct             = "DataTraveler"
	testRemoteHost          = "10.0.0.5"
	testRootBusID           = "1-1"
	testSecondaryBusID      = "2-1"
	testServeCommand        = "serve"
	testStatusAvailableName = "available"
	testStatusNullName      = "null"
	testStatusSocketFlag    = "--status-socket"
	testUnbindCommand       = "unbind"
	testUSBIPCoreModule     = usbip.KernelModuleUSBIPCore
	testUSBIPHostModule     = usbip.KernelModuleUSBIPHost
	testVersionToken        = "version"
	testVHCIHCDModule       = usbip.KernelModuleVHCIHCD
	testWatchCommand        = "watch"
)
