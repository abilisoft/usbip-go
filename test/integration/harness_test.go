//go:build integration_linux

package integration_test

import (
	"testing"

	"github.com/abilisoft/usbip-go/test/integration"
	"github.com/stretchr/testify/require"
)

// TestHarnessSetupVUDCSmoke is the meta-test that proves the harness
// itself works before any real integration test layers on top: it asks
// SetupVUDC for a gadget, asserts a non-empty busid comes back, and
// lets t.Cleanup tear the configfs tree down. A follow-on SetupVUDC
// would pick a different UDC name (random suffix in the gadget
// directory) so the test is safe under parallel runs. Skips cleanly on
// any unit lacking the kernel prerequisites per spec §8.4.
func TestHarnessSetupVUDCSmoke(t *testing.T) {
	dev := integration.SetupVUDC(t)

	require.NotEmpty(t, dev.BusID, "harness must return a non-empty busid")
	require.NotEmpty(t, dev.Name, "harness must return a non-empty gadget name")
}
