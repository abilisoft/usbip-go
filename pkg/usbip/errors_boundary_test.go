package usbip_test

import (
	"testing"

	internalapp "github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestFacadeLifecycleSentinelsDeclared pins the public lifecycle +
// protocol sentinels declared by spec §5.7 that must be matchable via
// errors.Is through the pkg/usbip boundary. Previously callers had to
// import internal/app (ErrImporterClosed / ErrAlreadyShutdown /
// ErrServeAlreadyRunning) or reach into pkg/domain (ErrProtocolError)
// because pkg/usbip did not re-export them.
func TestFacadeLifecycleSentinelsDeclared(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"ErrImporterClosed", usbip.ErrImporterClosed},
		{"ErrExporterShutdown", usbip.ErrExporterShutdown},
		{"ErrServeAlreadyRunning", usbip.ErrServeAlreadyRunning},
		{"ErrAlreadyShutdown", usbip.ErrAlreadyShutdown},
		{"ErrProtocolError", usbip.ErrProtocolError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Error(t, tc.err, "usbip.%s must be declared", tc.name)
		})
	}
}

// TestFacadeInternalSentinelsDoNotLeak enforces the boundary: the
// public sentinels defined in pkg/usbip are NOT identical to the
// internal/app sentinels. errors.Is against the internal identity
// through the facade-declared public sentinel must return false, so
// downstream callers cannot accidentally couple their error handling
// to internal/app's identity.
func TestFacadeInternalSentinelsDoNotLeak(t *testing.T) {
	t.Parallel()

	require.NotErrorIs(t, usbip.ErrImporterClosed, internalapp.ErrImporterClosed,
		"usbip.ErrImporterClosed must not be identity-equal to internal/app.ErrImporterClosed")
	require.NotErrorIs(t, usbip.ErrExporterShutdown, internalapp.ErrAlreadyShutdown,
		"usbip.ErrExporterShutdown must not be identity-equal to internal/app.ErrAlreadyShutdown")
	require.NotErrorIs(t, usbip.ErrServeAlreadyRunning, internalapp.ErrServeAlreadyRunning,
		"usbip.ErrServeAlreadyRunning must not be identity-equal to internal/app.ErrServeAlreadyRunning")
}

// TestAlreadyShutdownReExportsDomain pins that usbip.ErrAlreadyShutdown
// re-exports the domain sentinel unchanged. spec §5.7 already lists
// domain.ErrAlreadyShutdown on the public surface, so aliasing — not a
// distinct identity — is the right treatment for that specific error.
func TestAlreadyShutdownReExportsDomain(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, usbip.ErrAlreadyShutdown, domain.ErrAlreadyShutdown)
	require.ErrorIs(t, domain.ErrAlreadyShutdown, usbip.ErrAlreadyShutdown)
}

// TestProtocolErrorReExportsDomain pins that usbip.ErrProtocolError is
// the same identity as domain.ErrProtocolError. The protocol-error
// classification is a domain-level concept; callers must be able to
// match on either form interchangeably.
func TestProtocolErrorReExportsDomain(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, usbip.ErrProtocolError, domain.ErrProtocolError)
	require.ErrorIs(t, domain.ErrProtocolError, usbip.ErrProtocolError)
}

// TestImporterAfterCloseYieldsPublicSentinel proves the facade
// translates internal ErrImporterClosed into the public
// usbip.ErrImporterClosed when surfaced to callers. Before the fix
// the forwarding method returned the internal identity unchanged,
// forcing consumers to import internal/app to classify the error.
func TestImporterAfterCloseYieldsPublicSentinel(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)

	imp := usbip.NewImporterFromInternalForTest(s.inner)
	require.NoError(t, imp.Close())

	_, err := imp.ListRemote(t.Context(), usbip.RemoteEndpoint{Host: "peer"})
	require.ErrorIs(t, err, usbip.ErrImporterClosed)
	require.NotErrorIs(t, err, internalapp.ErrImporterClosed,
		"facade must translate internal/app.ErrImporterClosed, not leak it")
}

// TestTranslateInternalErrCovers drives every branch of the
// internal→public sentinel translator directly. The branches for
// ErrServeAlreadyRunning and the pass-through path cannot be reached
// via forwarding-method tests because the only internal call site for
// ErrServeAlreadyRunning is startServing — which is reached only via
// Serve under concurrent invocation, beyond the scope of a facade
// unit test.
func TestTranslateInternalErrCovers(t *testing.T) {
	t.Parallel()

	require.NoError(t, usbip.TranslateInternalErrForTest(nil))

	require.ErrorIs(t,
		usbip.TranslateInternalErrForTest(internalapp.ErrServeAlreadyRunning),
		usbip.ErrServeAlreadyRunning,
	)
	require.NotErrorIs(t,
		usbip.TranslateInternalErrForTest(internalapp.ErrServeAlreadyRunning),
		internalapp.ErrServeAlreadyRunning,
	)

	// Pass-through: an unrelated error carries through unchanged so
	// adapter-level wrap chains (transport/kernel/codec) survive.
	require.ErrorIs(t,
		usbip.TranslateInternalErrForTest(domain.ErrDeviceNotFound),
		domain.ErrDeviceNotFound,
	)
}

// TestFacadeAttachInProgressIsPublicallyMatchable pins the public
// sentinel contract. The internal acquireAttachSlot rejects
// concurrent Attach calls for the same (remote, busid) with
// ErrAttachInProgress; the public facade MUST expose an
// errors.Is-compatible sentinel so callers can classify the rejection
// without reaching into internal/app.
func TestFacadeAttachInProgressIsPublicallyMatchable(t *testing.T) {
	t.Parallel()

	require.Error(t, usbip.ErrAttachInProgress,
		"usbip.ErrAttachInProgress must be declared as a public sentinel")

	require.ErrorIs(t,
		internalapp.ErrAttachInProgress,
		usbip.ErrAttachInProgress,
		"internal/app.ErrAttachInProgress must match against the "+
			"public facade's usbip.ErrAttachInProgress so consumers "+
			"can classify concurrent-Attach rejection without "+
			"importing internal/app")
}

// TestExporterServeAfterShutdownYieldsPublicSentinel mirrors the
// importer case for Serve post-Shutdown: the public facade must return
// a public sentinel (usbip.ErrExporterShutdown) not the internal
// identity.
func TestExporterServeAfterShutdownYieldsPublicSentinel(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)

	exp := usbip.NewExporterFromInternalForTest(s.inner)

	ctx, cancel := newBoundedCtx(t)
	t.Cleanup(cancel)

	require.NoError(t, exp.Shutdown(ctx))

	err := exp.Serve(ctx, stubListener{})
	require.ErrorIs(t, err, usbip.ErrExporterShutdown)
	require.NotErrorIs(t, err, internalapp.ErrAlreadyShutdown,
		"facade must translate internal/app.ErrAlreadyShutdown, not leak it")
}
