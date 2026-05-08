// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// binaryBuildTimeout caps the `go build` subprocess BuildBinary
// shells out to. Five minutes is generous enough for a cold module
// cache on CI runners; well under the test-package timeout so a
// hung build surfaces as a clear binbuild failure rather than the
// outer test hitting -timeout.
const binaryBuildTimeout = 5 * time.Minute

// BuildBinary compiles ./cmd/<name>/ to an absolute-path temp binary
// the test can exec. Caller passes the leaf command directory name
// (today only "usbip-go" — the project ships one binary per
// ADR-0011, but the helper stays generic in case a future cmd/ leaf
// needs the same TMPDIR + buildvcs handling).
//
// Centralised here so binary-smoke tests across `cmd/.../`_test.go
// packages share one helper. Two prior duplicates in
// cmd/usbip-go/binary_smoke_test.go and
// cmd/usbip-go/binary_missing_parent_dir_test.go drifted on TMPDIR
// handling; this is the canonical version.
//
// Notes on the absolute-path dance: `t.TempDir()` may return a
// relative path when TMPDIR resolves relative under a sandbox; an
// `exec.Command(out)` with a relative `out` cannot be resolved by
// fork/exec once the daemon under test changes cwd. `filepath.Abs`
// the temp dir before composing the binary path, and pass the same
// absolute TMPDIR to the build subprocess so go's workdir creation
// does not chase a relative `.go-build-XYZ` either.
func BuildBinary(t *testing.T, name string) string {
	t.Helper()

	tmp, err := filepath.Abs(t.TempDir())
	require.NoError(t, err)

	out := filepath.Join(tmp, name)
	root := RepoRoot(t)

	ctx, cancel := context.WithTimeout(context.Background(), binaryBuildTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", out, "./cmd/"+name+"/")

	cmd.Dir = root

	cmd.Env = append(
		os.Environ(),
		"CGO_ENABLED=0",
		"TMPDIR="+tmp,
	)

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "build %s failed: %s", name, output)

	return out
}

// RepoRoot walks up from PWD looking for go.mod and returns the
// directory holding it. Fatal-fails the test if the walk reaches the
// filesystem root without finding go.mod.
func RepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)

	for {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found above %q", dir)
		}

		dir = parent
	}
}
