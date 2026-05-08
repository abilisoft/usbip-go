//go:build linux

package kernel_test

import (
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestParseStatusFile_TranslatesKernelVDEVStatus pins the contract
// that the vhci status parser maps the raw `sta` column the kernel
// writes — values 4-7 drawn from the VDEV_ST_* half of
// usbip_device_status — onto the domain.Status enum the rest of the
// code consumes. The kernel enum intentionally collides with the
// SDEV_ST_* (server-side) range, so the parser must translate at the
// boundary; without the translation, a freshly-reset VHCI port shows
// up as domain.StatusError and findFreePort returns
// domain.ErrNoFreePort even when every port is idle.
//
// This reproduction exercises every documented vdev code the kernel
// writes during normal port lifecycle transitions.
func TestParseStatusFile_TranslatesKernelVDEVStatus(t *testing.T) {
	cases := []struct {
		name       string
		kernelSta  string // raw column as the kernel writes it
		wantStatus domain.Status
	}{
		{"null / unused", "004", domain.StatusNull},
		{"not-assigned", "005", domain.StatusNotAssigned},
		{"used", "006", domain.StatusUsed},
		{"error", "007", domain.StatusError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mfs := statusFS("", nil, 16)

			a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
			require.NoError(t, err)

			body := "hub port sta spd dev      sockfd local_busid\n" +
				"hs  0000 " + tc.kernelSta + " 000 00000000 000000 0-0\n"

			rows, err := kernel.ParseStatusFileForTest(a, body, "status", 0, 16)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, tc.wantStatus, rows[0].Status,
				"kernel sta=%s must translate to %s, not %s",
				tc.kernelSta, tc.wantStatus, rows[0].Status)
		})
	}
}
