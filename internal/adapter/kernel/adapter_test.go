//go:build linux

package kernel_test

import (
	"errors"
	"io/fs"
	"log/slog"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
)

// Compile-time interface assertions live in the test file so importing the
// kernel package from non-Linux builds (during interface refactors) does
// not accidentally force the constraint. The three role adapters satisfy
// three distinct interfaces declared in internal/app.
var (
	_ app.ImporterKernel = (*kernel.ImporterAdapter)(nil)
	_ app.ExporterKernel = (*kernel.ExporterAdapter)(nil)
	_ app.KernelEvents   = (*kernel.EventsAdapter)(nil)
)

func TestNewImporterAdapter_ReturnsNonNil(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewImporterAdapter()
	require.NoError(t, err)
	require.NotNil(t, a)
}

func TestNewExporterAdapter_ReturnsNonNil(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewExporterAdapter()
	require.NoError(t, err)
	require.NotNil(t, a)
}

func TestNewEventsAdapter_ReturnsNonNil(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewEventsAdapter()
	require.NoError(t, err)
	require.NotNil(t, a)
}

// fakeNetlinkSocket is a test-scope NetlinkSocket that answers no
// events. Concrete type avoids nilnil lint noise and keeps the dialer
// closure simple.
type fakeNetlinkSocket struct{}

func (fakeNetlinkSocket) Receive() ([]byte, error) { return nil, errTestSocketClosed }
func (fakeNetlinkSocket) Close() error             { return nil }

// errTestSocketClosed is an err113-compatible sentinel for the test
// fake's Receive() call. No real consumer ever sees it because the
// fake is injected only for option-plumbing assertions.
var errTestSocketClosed = errors.New("fake netlink socket closed")

// TestOptionsApplyToImporter confirms every With* option threads through
// NewImporterAdapter into the underlying commonAdapter state. The test
// uses the package-internal accessors exported via export_test.go so the
// public API stays minimal.
func TestOptionsApplyToImporter(t *testing.T) {
	t.Parallel()

	myFS := fstest.MapFS{"sys/module/usbip_core": &fstest.MapFile{Mode: fs.ModeDir}}
	writer := func(string, string) error { return nil }
	dialer := func() (kernel.NetlinkSocket, error) { return fakeNetlinkSocket{}, nil }
	logger := slog.New(slog.DiscardHandler)
	clock := testutil.NewFakeClockAt(time.Unix(0, 0))

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(myFS),
		kernel.WithWriteFunc(writer),
		kernel.WithNetlinkDialer(dialer),
		kernel.WithLogger(logger),
		kernel.WithClock(clock),
	)
	require.NoError(t, err)
	require.NotNil(t, a)

	got := kernel.ExportCommonFromImporter(a)
	require.Same(t, logger, got.Logger)
	require.NotNil(t, got.FS)
	require.NotNil(t, got.Write)
	require.NotNil(t, got.NetlinkDial)
	require.Same(t, clock, got.Clock)
}

func TestOptionsApplyToExporter(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	a, err := kernel.NewExporterAdapter(kernel.WithLogger(logger))
	require.NoError(t, err)

	got := kernel.ExportCommonFromExporter(a)
	require.Same(t, logger, got.Logger)
}

func TestOptionsApplyToEvents(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	a, err := kernel.NewEventsAdapter(kernel.WithLogger(logger))
	require.NoError(t, err)

	got := kernel.ExportCommonFromEvents(a)
	require.Same(t, logger, got.Logger)
}

// TestDefaultFSIsOsDirFS ensures the default WithFS is os.DirFS("/") so
// production callers can omit the option.
func TestDefaultFSIsOsDirFS(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewImporterAdapter()
	require.NoError(t, err)

	got := kernel.ExportCommonFromImporter(a)
	require.NotNil(t, got.FS, "default FS must not be nil")
}
