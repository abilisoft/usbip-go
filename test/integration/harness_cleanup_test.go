// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunGadgetCleanup_MissingRootIsNoOp(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "missing-gadget")
	require.NoError(t, runGadgetCleanup(root))
	require.NoError(t, runGadgetCleanup(root))
}
