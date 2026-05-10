// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGoReleaserStampsPrintedBuildDate pins the release metadata
// wiring across .goreleaser.yml and version.go. The binary prints the
// package variable named buildDate; a ldflags target named main.date
// silently writes a different, unused symbol and leaves the visible
// build date at the compiled default.
func TestGoReleaserStampsPrintedBuildDate(t *testing.T) {
	t.Parallel()

	configBytes, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yml"))
	require.NoError(t, err)

	versionBytes, err := os.ReadFile("version.go")
	require.NoError(t, err)

	config := string(configBytes)
	versionSource := string(versionBytes)

	require.Contains(t, config, "-X main.buildDate={{.CommitDate}}")
	require.NotContains(t, config, "-X main.date={{.CommitDate}}")
	require.Contains(t, versionSource, "buildDate = \"unknown\"",
		"keep this test aligned with the variable printed by version.go")
	require.True(t, strings.Contains(config, "main.version") && strings.Contains(config, "main.commit"),
		"release builds must continue stamping version and commit alongside buildDate")
}
