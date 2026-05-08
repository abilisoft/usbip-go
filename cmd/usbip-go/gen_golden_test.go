// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"flag"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// updateGolden regenerates testdata/devices.{txt,json} when `-update`
// is passed to `go test`. Otherwise the test is a no-op.
var updateGolden = flag.Bool("update", false, "regenerate testdata/devices.{txt,json} from current renderers")

// TestUpdateGoldenFixtures rewrites testdata/devices.{txt,json} from
// the renderers' current output when -update is set. Without -update
// the test exits immediately after checking the flag, so the regular
// test suite is unaffected.
func TestUpdateGoldenFixtures(t *testing.T) {
	t.Parallel()

	if !*updateGolden {
		return
	}

	var tb bytes.Buffer
	require.NoError(t, tableRenderer{}.Devices(&tb, goldenDevices()))
	require.NoError(t, os.WriteFile("testdata/devices.txt", tb.Bytes(), 0o600))

	var jb bytes.Buffer
	require.NoError(t, jsonRenderer{}.Devices(&jb, goldenDevices()))
	require.NoError(t, os.WriteFile("testdata/devices.json", jb.Bytes(), 0o600))
}
