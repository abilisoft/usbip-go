// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

const bazelTestBinaryEnv = "USBIP_GO_TEST_BINARY"

const goCoverageDirEnv = "GOCOVERDIR"

const bazelBinPathSegment = "/bin/"

// BuildBinary returns an absolute path to the requested command binary. Bazel
// tests consume the already-built binary supplied through bazelTestBinaryEnv;
// direct `go test` runs fall back to compiling ./cmd/<name>/ in a temporary
// directory. Caller passes the leaf command directory name (today only
// "usbip-go" — the project ships one binary per OpenSpec, but the helper stays
// generic in case a future cmd/ leaf needs the same fallback behavior).
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
func BuildBinary(ctx context.Context, t *testing.T, name string) string {
	t.Helper()

	if configured := os.Getenv(bazelTestBinaryEnv); configured != "" {
		return resolveBazelBinary(t, name, configured)
	}

	tmp, err := filepath.Abs(t.TempDir())
	require.NoError(t, err)

	out := filepath.Join(tmp, name)
	root := RepoRoot(t)

	ctx, cancel := context.WithTimeout(ctx, binaryBuildTimeout)
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

// BinaryCommandContext returns a command for the requested project binary.
// Bazel coverage instruments data-dependency binaries as well as the test
// executable, so subprocesses need their own GOCOVERDIR. Without it, the Go
// runtime writes a coverage warning to stderr and breaks tests that assert a
// clean command invocation.
func BinaryCommandContext(
	ctx context.Context,
	t *testing.T,
	name string,
	args ...string,
) *exec.Cmd {
	t.Helper()

	// Building the test fixture is setup work and must not consume the command's
	// runtime deadline. In direct `go test` runs, several parallel smoke tests
	// may compile the binary concurrently; their short execution contexts are
	// intentionally sized for the finished command, not a cold Go build.
	binary := BuildBinary(t.Context(), t, name)
	cmd := exec.CommandContext(ctx, binary, args...)
	if os.Getenv(bazelTestBinaryEnv) != "" && os.Getenv(goCoverageDirEnv) == "" {
		cmd.Env = append(os.Environ(), goCoverageDirEnv+"="+t.TempDir())
	}

	return cmd
}

// resolveBazelBinary maps the location-expanded runfile path onto the current
// test's runfiles tree. rules_go supplies TEST_SRCDIR and TEST_WORKSPACE for
// Bazel tests; an absolute value remains useful for other test runners that
// choose to provide the same environment contract.
func resolveBazelBinary(t *testing.T, name, configured string) string {
	t.Helper()

	path := configured
	if !filepath.IsAbs(path) {
		testSrcDir := os.Getenv("TEST_SRCDIR")
		testWorkspace := os.Getenv("TEST_WORKSPACE")

		require.NotEmpty(t, testSrcDir, "%s requires TEST_SRCDIR for a relative path", bazelTestBinaryEnv)
		require.NotEmpty(t, testWorkspace, "%s requires TEST_WORKSPACE for a relative path", bazelTestBinaryEnv)

		relative := bazelRunfileRelativePath(path)
		require.True(t, filepath.IsLocal(relative), "prebuilt test binary path %q must stay within runfiles", relative)

		path = filepath.Join(testSrcDir, testWorkspace, relative)
	}

	require.Equal(t, name, filepath.Base(path), "prebuilt test binary must match requested command")

	return path
}

// bazelRunfileRelativePath converts a location-expanded exec path such as
// bazel-out/<config>/bin/cmd/tool/tool_/tool into the workspace-relative path
// used by the runfiles tree. Already-relative runfile paths pass through.
func bazelRunfileRelativePath(path string) string {
	normalized := filepath.ToSlash(path)
	if marker := strings.LastIndex(normalized, bazelBinPathSegment); marker >= 0 {
		normalized = normalized[marker+len(bazelBinPathSegment):]
	}

	return filepath.FromSlash(normalized)
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
