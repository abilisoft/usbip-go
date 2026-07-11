// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package testutil_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/stretchr/testify/require"
)

const environmentAssignmentSeparator = "="

func TestBuildBinaryResolvesBazelRunfile(t *testing.T) {
	const (
		binaryName         = "usbip-go"
		commandArgument    = "version"
		goCoverageDirEnv   = "GOCOVERDIR"
		workspace          = "workspace"
		runfile            = "cmd/usbip-go/usbip-go_/usbip-go"
		testExecutableMode = 0o750
	)

	testSrcDir := t.TempDir()
	want := filepath.Join(testSrcDir, workspace, filepath.FromSlash(runfile))

	require.NoError(t, os.MkdirAll(filepath.Dir(want), testExecutableMode))
	require.NoError(t, os.WriteFile(want, nil, testExecutableMode))

	t.Setenv("USBIP_GO_TEST_BINARY", "bazel-out/k8-fastbuild/bin/"+runfile)
	t.Setenv("TEST_SRCDIR", testSrcDir)
	t.Setenv("TEST_WORKSPACE", workspace)
	t.Setenv(goCoverageDirEnv, "")

	require.Equal(t, want, testutil.BuildBinary(t.Context(), t, binaryName))

	cmd := testutil.BinaryCommandContext(t.Context(), t, binaryName, commandArgument)
	require.Equal(t, want, cmd.Path)
	require.Equal(t, []string{want, commandArgument}, cmd.Args)

	coverageDir, ok := lastEnvironmentValue(cmd.Environ(), goCoverageDirEnv)
	require.True(t, ok)
	require.NotEmpty(t, coverageDir)
	require.DirExists(t, coverageDir)
}

func lastEnvironmentValue(environment []string, name string) (string, bool) {
	for _, assignment := range slices.Backward(environment) {
		key, value, found := strings.Cut(assignment, environmentAssignmentSeparator)
		if found && key == name {
			return value, true
		}
	}

	return "", false
}
