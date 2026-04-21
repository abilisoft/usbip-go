//go:build linux

package kernel_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestExtractPortFromBusID locks in the dotted-busid fix (Pass-4
// RANK 1). A busid like "1-1.2" describes a device attached to the
// second port of an intermediate hub hanging off root port 1. The
// domain-level Port.ID is the VHCI root-slot number (the leading
// segment before the first "."), so "1-1.2" must parse as 1.
//
// Pre-fix the function fed the whole "1.2" to strconv.ParseUint and
// returned 0, which silently dropped hub-attached detach uevents in
// the reconnect watcher's isDetachSignal comparator because
// d.Port.ID (0) never matched p.portID (the real root port).
func TestExtractPortFromBusID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  domain.PortID
	}{
		{name: "single root port", input: "1-1", want: 1},
		{name: "double-digit root", input: "1-2", want: 2},
		{name: "one hub level", input: "1-1.2", want: 1},
		{name: "deep hub chain", input: "2-3.4.5", want: 3},
		{name: "empty input", input: "", want: 0},
		{name: "non-numeric", input: "abc", want: 0},
		{name: "trailing dash only", input: "1-", want: 0},
		{name: "leading dash only", input: "-1", want: 0},
		{name: "leading dot after dash", input: "1-.2", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := kernel.ExtractPortFromBusIDForTest(tc.input)
			require.Equal(t, tc.want, got,
				"extractPortFromBusID(%q) = %d, want %d", tc.input, got, tc.want)
		})
	}
}
