//go:build linux

package kernel_test

import (
	"context"
	"fmt"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// poisonFS wraps an fs.FS and forces Open on a specific path to fail
// with injectErr. All other operations delegate to inner. Used to
// simulate EACCES / EIO on sysfs reads that MapFS cannot reproduce.
type poisonFS struct {
	inner     fs.FS
	target    string
	injectErr error
}

func (p poisonFS) Open(name string) (fs.File, error) {
	if name == p.target {
		return nil, &fs.PathError{Op: "open", Path: name, Err: p.injectErr}
	}

	f, err := p.inner.Open(name)
	if err != nil {
		return nil, fmt.Errorf("poisonFS delegate: %w", err)
	}

	return f, nil
}

// TestBind_CurrentDriver_SurfacesPermissionError pins currentDriver's
// documented contract: "Both paths' reads fail with unexpected
// (non-ENOENT) errno → surface verbatim." When driver_name is present
// but unreadable (EACCES), the helper previously concluded
// ErrDeviceNotBound because the interface directory still existed —
// discarding the permission signal and giving operators the wrong
// error class. The fix requires distinguishing "absent" (ENOENT-ish,
// legitimately unbound) from "present-but-unreadable" (real I/O or
// permission error, must surface).
func TestBind_CurrentDriver_SurfacesPermissionError(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1.2")
	iface := string(busID) + ":1.0"
	base := bindFS(string(busID))

	// Poison the driver_name read with fs.ErrPermission; readLink on
	// "driver" will still return fs.ErrNotExist via the MapFS fallback
	// in readLink. ifaceDir itself stays present so the stat branch
	// succeeds. Bind must surface ErrPermission (not
	// ErrDeviceNotBound).
	poisoned := poisonFS{
		inner:     base,
		target:    "sys/bus/usb/devices/" + iface + "/driver/driver_name",
		injectErr: fs.ErrPermission,
	}

	var rec writeRecord

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(poisoned),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrPermission,
		"permission failure on driver_name must surface as ErrPermission, not ErrDeviceNotBound")
	require.NotErrorIs(t, err, domain.ErrDeviceNotBound,
		"permission error must not be misclassified as unbound")
}
