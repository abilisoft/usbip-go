// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootCmdHasNoDeadDrainTimeoutFlag pins the invariant that the
// root usbipd command does not advertise a --drain-timeout flag. The
// flag was inherited as a persistent entry but nothing in the daemon
// code path consumes cfg.DrainTimeout; the real --drain-timeout lives
// on the `usbip drain` subcommand, which shadows the dead persistent
// flag anyway. Keeping the persistent shadow silently misleads
// operators who run `usbipd-go --drain-timeout=10s` expecting the daemon
// to honour it.
func TestRootCmdHasNoDeadDrainTimeoutFlag(t *testing.T) {
	t.Parallel()

	root := newRootCmd()

	require.Nil(t, root.PersistentFlags().Lookup("drain-timeout"),
		"root usbipd must not register a persistent --drain-timeout; only the drain subcmd should own it")
	require.Nil(t, root.Flags().Lookup("drain-timeout"),
		"root usbipd must not register a local --drain-timeout either")
}
