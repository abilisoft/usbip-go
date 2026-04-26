// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"iter"
	"reflect"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// wantImporterWatchSig pins the documented Importer.Watch shape so a
// drift in pkg/usbip surfaces as a regular test failure, not as a
// release-time cross-compile error. Updating this value is a
// deliberate API change and must be paired with a release notes entry.
const wantImporterWatchSig = "func(*usbip.Importer, context.Context) " +
	"iter.Seq[github.com/abilisoft/usbip-go/pkg/domain.Event]"

// wantExporterWatchSessionsSig is the same pin for
// Exporter.WatchSessions, the second of the two iterators the events
// example demonstrates.
const wantExporterWatchSessionsSig = "func(*usbip.Exporter, context.Context) " +
	"iter.Seq[github.com/abilisoft/usbip-go/pkg/domain.Event]"

// TestWatchAPIShapeUnchanged pins the runtime signatures of
// Importer.Watch and Exporter.WatchSessions — the two iterators this
// example consumes. The reflect-based form (rather than a compile-time
// blank-identifier var with an explicit type) sidesteps staticcheck's
// QF1011 "could omit type" suggestion: the explicit shape is the
// whole point, and an inferred type would defeat the regression
// guard.
func TestWatchAPIShapeUnchanged(t *testing.T) {
	t.Parallel()

	importerWatch := (*usbip.Importer).Watch
	exporterWatch := (*usbip.Exporter).WatchSessions

	require.Equal(t, wantImporterWatchSig,
		reflect.TypeOf(importerWatch).String(),
		"Importer.Watch signature drift; review pkg/usbip and update release notes")
	require.Equal(t, wantExporterWatchSessionsSig,
		reflect.TypeOf(exporterWatch).String(),
		"Exporter.WatchSessions signature drift; review pkg/usbip and update release notes")

	// Reference each iterator returned by an unused signature check —
	// keeps the iter and context imports load-bearing without invoking
	// the kernel-touching constructor path.
	var (
		_ iter.Seq[usbip.Event]
		_ context.Context
	)
}
