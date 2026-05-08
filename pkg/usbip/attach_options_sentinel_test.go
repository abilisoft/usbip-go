package usbip_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	internalapp "github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// TestAttachOptionsInvalidSentinelDeclared pins the public export of
// ErrAttachOptionsInvalid. Callers tune AttachOptions at the facade
// layer; if the internal sentinel leaks through translation but has
// no public alias, consumers cannot errors.Is against a public name
// and must reach into internal/app to classify the error.
func TestAttachOptionsInvalidSentinelDeclared(t *testing.T) {
	t.Parallel()

	require.Error(t, usbip.ErrAttachOptionsInvalid,
		"usbip.ErrAttachOptionsInvalid must be declared on the public surface")
}

// TestAttachOptionsInvalidTranslatesFromInternal asserts the
// translation: a wrapper carrying the internal sentinel, once it
// passes through the facade, must match the public sentinel. The
// translation is needed because distinct identities are the project
// convention for non-domain lifecycle errors.
func TestAttachOptionsInvalidTranslatesFromInternal(t *testing.T) {
	t.Parallel()

	translated := usbip.TranslateInternalErrForTest(internalapp.ErrAttachOptionsInvalid)
	require.ErrorIs(t, translated, usbip.ErrAttachOptionsInvalid,
		"translation must map internal ErrAttachOptionsInvalid to the public alias")
}
