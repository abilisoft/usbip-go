// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestBind_VHCIGuard_FailsClosedOnRealReadlinkError pins the
// fail-closed contract: when readLink returns an UNEXPECTED error
// (anything other than fs.ErrNotExist, which represents the
// MapFS-cant-symlink case), the guard MUST refuse to proceed. A
// permissive "any error means inapplicable" branch would let a
// permission fault on a vhci-rooted device silently bypass the
// loop guard and re-attempt the destructive bind sequence.
//
// Implements the test by wrapping a MapFS in a fakeReadLinkFS that
// returns a non-ENOENT error (fs.ErrPermission). The MapFS-only
// path returns ErrNotExist via the no-symlink-support branch in
// readLink; the wrapped FS returns ErrPermission to simulate a
// real ACL-denied readlink that production would expose.
func TestBind_VHCIGuard_FailsClosedOnRealReadlinkError(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("3-1")

	// Module skeleton so the preflight passes; bind would otherwise
	// fail before the guard runs.
	mfs := fstest.MapFS{
		"sys/module/usbip_core":                &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host":                &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + string(busID): &fstest.MapFile{Mode: fs.ModeDir},
	}

	wrapped := &errorReadLinkFS{
		inner:     mfs,
		linkError: fs.ErrPermission,
	}

	a, err := kernel.NewExporterAdapter(kernel.WithFS(wrapped))
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.Error(t, err,
		"vhci guard must fail-closed on a real readlink error rather than silently bypass")
	require.True(t, errors.Is(err, fs.ErrPermission) || errors.Is(err, domain.ErrPermission),
		"unexpected readlink error must propagate; surfaced=%v", err)
}

// errorReadLinkFS wraps fs.FS and implements fs.ReadLinkFS with a
// configurable ReadLink error. The wrapping must NOT embed the
// inner FS, because embedded fstest.MapFS is itself fs.ReadLinkFS
// and the inner ReadLink would shadow the wrapper's via method
// promotion — defeating the test's intent. Open is delegated
// explicitly so the rest of the adapter's filesystem work goes
// through the unmodified MapFS.
type errorReadLinkFS struct {
	inner     fs.FS
	linkError error
}

// Open delegates to the wrapped FS.
func (e *errorReadLinkFS) Open(name string) (fs.File, error) {
	return e.inner.Open(name)
}

// ReadLink always returns the configured error.
func (e *errorReadLinkFS) ReadLink(string) (string, error) {
	return "", e.linkError
}

// Lstat delegates to fs.Stat on the inner FS so the type satisfies
// io/fs.ReadLinkFS (which requires both ReadLink and Lstat). Tests
// driving the vhci guard never inspect this output directly; the
// pass-through is enough.
func (e *errorReadLinkFS) Lstat(name string) (fs.FileInfo, error) {
	return fs.Stat(e.inner, name)
}
