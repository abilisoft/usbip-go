//go:build linux

package kernel_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
)

// TestParseStatusFile_SurfacesScannerError pins the contract that a
// bufio.Scanner failure (ErrTooLong or any future wrapped read error)
// must surface instead of being silently swallowed as EOF. The parser
// previously skipped the scanner.Err() check after its for-Scan loop,
// so a status row longer than the default 64 KiB buffer truncated to
// zero parsed rows with no indication to the caller. Kernel status
// rows are small in practice; this guard is defense-in-depth so a
// future kernel change that widens a column (or a corrupt sysfs read)
// cannot produce a silently-empty port list.
func TestParseStatusFile_SurfacesScannerError(t *testing.T) {
	t.Parallel()

	mfs := statusFS("", nil, 16)

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	// One row wider than bufio.MaxScanTokenSize (64 KiB) — Scan returns
	// false with bufio.ErrTooLong; scanner.Err() surfaces it when the
	// parser checks. The parser must not return (nil, nil) and
	// silently pretend the status file was empty.
	oversized := strings.Repeat("x", 1<<17) // 128 KiB, no newline needed

	rows, perr := kernel.ParseStatusFileForTest(a, oversized, "status", 0, 16)
	require.Error(t, perr,
		"scanner failure must surface as an error, not silent empty result")
	require.Empty(t, rows,
		"error path must not return partial rows either")
}
