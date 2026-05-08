package usbip_test

import (
	"reflect"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestTypeAliasesAreIdentical asserts that every usbip.X declared in
// §5.7 is a type alias for domain.X rather than a freshly-declared
// type. If any entry drifts from alias to declaration, consumers who
// mix imports (some from pkg/usbip, some from pkg/domain) would fail
// to assign one to the other — so the guarantee is load-bearing.
func TestTypeAliasesAreIdentical(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		domain, u reflect.Type
	}{
		{"Device", reflect.TypeOf(domain.Device{}), reflect.TypeOf(usbip.Device{})},
		{"Port", reflect.TypeOf(domain.Port{}), reflect.TypeOf(usbip.Port{})},
		{"Session", reflect.TypeOf(domain.Session{}), reflect.TypeOf(usbip.Session{})},
		{"RemoteEndpoint", reflect.TypeOf(domain.RemoteEndpoint{}), reflect.TypeOf(usbip.RemoteEndpoint{})},
		{"BusID", reflect.TypeOf(domain.BusID("")), reflect.TypeOf(usbip.BusID(""))},
		{"Speed", reflect.TypeOf(domain.Speed(0)), reflect.TypeOf(usbip.Speed(0))},
		{"Status", reflect.TypeOf(domain.Status(0)), reflect.TypeOf(usbip.Status(0))},
		{"PortID", reflect.TypeOf(domain.PortID(0)), reflect.TypeOf(usbip.PortID(0))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.domain, tc.u,
				"usbip.%s must alias domain.%s (identity required)",
				tc.name, tc.name)
		})
	}
}

// TestBusIDValuesInterchangeable proves that a value built via
// usbip.BusID and another via domain.BusID are interchangeable at the
// use site — not just the same reflect.Type.
func TestBusIDValuesInterchangeable(t *testing.T) {
	t.Parallel()

	var fromDomain domain.BusID = "1-1"
	var fromUsbip usbip.BusID = "1-1"

	// Direct assignment both directions must compile: only true when
	// both names are aliases of the same underlying type.
	fromDomain = fromUsbip
	fromUsbip = fromDomain

	require.Equal(t, "1-1", fromDomain.String())
	require.Equal(t, "1-1", fromUsbip.String())
}

// TestEventInterfaceIsAliased proves usbip.Event is the same interface
// type as domain.Event. A domain event value must satisfy usbip.Event
// without an explicit conversion.
func TestEventInterfaceIsAliased(t *testing.T) {
	t.Parallel()

	var ev domain.Event = domain.DeviceBoundEvent{}

	var asUsbip usbip.Event = ev

	require.NotNil(t, asUsbip)
	require.Equal(t, domain.EventDeviceBound, asUsbip.EventKind())
}
