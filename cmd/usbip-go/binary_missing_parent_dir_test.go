// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main_test

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/testutil"
)

// TestUSBIPDGoBinary_MissingStatusSocketParent_ExitAndStderr exercises
// the operator-facing failure mode end-to-end: a manually invoked
// daemon (no systemd RuntimeDirectory creating /run/usbip-go) must
// report the missing-parent path on stderr and exit non-zero. Pinned
// because the in-process bindStatusSocket test cannot prove that the
// error survives mainBody → exit-code mapping → stderr emission.
//
// Builds the real binary because the production exit path runs through
// os.Exit and cobra's Execute, neither of which the package-level tests
// reach.
func TestUSBIPDGoBinary_MissingStatusSocketParent_ExitAndStderr(t *testing.T) {
	t.Parallel()

	bin := buildUsbipdGoBinaryForTest(t)

	tmp, err := filepath.Abs(t.TempDir())
	require.NoError(t, err)

	missing := filepath.Join(tmp, "definitely-does-not-exist", "status.sock")

	// Bound the daemon spawn so a regression where it hangs instead of
	// exiting immediately surfaces as a timeout, not a hung CI run.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Pick an unprivileged port; --listen :0 lets the kernel pick.
	// Capture stdout and stderr separately so we can pin which stream
	// the operator-facing error lands on (stderr is the systemd
	// journal expectation).
	cmd := exec.CommandContext(ctx, bin,
		"serve",
		"--listen", "127.0.0.1:0",
		"--status-socket", missing,
		"--log-level", "error",
		"--log-format", "json",
	)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	require.Error(t, runErr,
		"daemon must exit non-zero when --status-socket parent dir is absent")

	// A hung daemon would also produce *exec.ExitError once
	// CommandContext kills it. Pin that we did NOT cancel via deadline,
	// so a regression that hangs cannot satisfy the rest of the test.
	require.NotErrorIs(t, ctx.Err(), context.DeadlineExceeded,
		"daemon must exit on its own — context timeout indicates it hung instead of exiting on bad status socket")

	var exitErr *exec.ExitError
	require.ErrorAs(t, runErr, &exitErr,
		"daemon must exit cleanly (not crash) on missing-parent — got %T: %v", runErr, runErr)
	require.NotZero(t, exitErr.ExitCode(),
		"non-zero exit signals systemd / supervisor to report failure")

	stderrStr := stderr.String()
	require.True(t, strings.Contains(stderrStr, missing) || strings.Contains(stderrStr, missing+".lock"),
		"stderr (not stdout) must name the missing path so journald-style log capture surfaces it; got stderr=%q stdout=%q",
		stderrStr, stdout.String())
}

// TestUSBIPDGoBinary_VersionExitsZeroWithoutDaemonBind pins that
// `usbip-go version` runs without hitting the daemon bind path. A
// command-wiring regression where version triggers the listener bind
// or status-socket setup would either hang the version subcommand or
// fail with a permission/ENOENT error operators would not expect from
// a metadata query. Catches that whole class of bug end-to-end.
func TestUSBIPDGoBinary_VersionExitsZeroWithoutDaemonBind(t *testing.T) {
	t.Parallel()

	bin := buildUsbipdGoBinaryForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, bin, "version")

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	require.NoError(t, err,
		"version must exit 0 — bound daemon paths must NOT engage on a metadata subcommand. stderr=%q stdout=%q",
		stderr.String(), stdout.String())
	require.NotErrorIs(t, ctx.Err(), context.DeadlineExceeded,
		"version must return promptly — a hang means version triggered listener bind or status-socket wait")

	out := stdout.String()
	require.Contains(t, out, "usbip-go version",
		"stdout must carry the unified binary's stamped version string")
	require.Empty(t, stderr.String(),
		"a clean version invocation must not write anything to stderr")
}

// buildUsbipdGoBinaryForTest compiles ./cmd/usbip-go into an
// absolute-path temp binary, returning the path. Thin wrapper over
// the canonical testutil.BuildBinary so a regression to the build
// flags lands in one place.
func buildUsbipdGoBinaryForTest(t *testing.T) string {
	t.Helper()

	return testutil.BuildBinary(t, "usbip-go")
}
