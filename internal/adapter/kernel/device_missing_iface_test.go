//go:build linux

package kernel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
)

// TestListLocalDevices_SkipsMissingOptionalInterface pins the
// documented readInterfaces contract ("Missing interfaces (ENOENT on
// optional sysfs attrs) are tolerated — some USB peripherals expose
// only a subset of their configured interfaces under sysfs"). The
// sysfs reader converts MapFS/kernel ENOENT into
// domain.ErrDeviceNotFound (classifyENOENT on kindDevice); the chain
// does not carry fs.ErrNotExist back, so a match-predicate narrowed to
// fs.ErrNotExist would miss the wrapped form and readInterfaces would
// surface the absent interface as a fatal error, dropping the whole
// device from ListLocalDevices.
func TestListLocalDevices_SkipsMissingOptionalInterface(t *testing.T) {
	t.Parallel()

	attrs := makeDeviceAttrs()
	// Advertise two interfaces but only populate :1.0. :1.1's sysfs
	// entries never get created so the reader's first ReadHex16
	// (bInterfaceClass) returns ErrDeviceNotFound-via-ENOENT. The
	// documented tolerance says: skip the absent interface, keep the
	// device. Pre-fix: ListLocalDevices returns an error.
	attrs["bNumInterfaces"] = "2\n"

	mfs := mergeFS(deviceSysfs("1-1", attrs), moduleDirs())

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := a.ListLocalDevices(context.Background())
	require.NoError(t, err,
		"absent optional interface must be tolerated, not dropped")
	require.Len(t, got, 1,
		"the device itself must still be reported")
	require.Len(t, got[0].Interfaces, 1,
		"only the present interface (:1.0) should populate Interfaces")
}
