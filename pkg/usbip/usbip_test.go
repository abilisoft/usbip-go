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
		{"Device", reflect.TypeFor[domain.Device](), reflect.TypeFor[usbip.Device]()},
		{"Port", reflect.TypeFor[domain.Port](), reflect.TypeFor[usbip.Port]()},
		{"Session", reflect.TypeFor[domain.Session](), reflect.TypeFor[usbip.Session]()},
		{"RemoteEndpoint", reflect.TypeFor[domain.RemoteEndpoint](), reflect.TypeFor[usbip.RemoteEndpoint]()},
		{"BusID", reflect.TypeFor[domain.BusID](), reflect.TypeFor[usbip.BusID]()},
		{"Speed", reflect.TypeFor[domain.Speed](), reflect.TypeFor[usbip.Speed]()},
		{"Status", reflect.TypeFor[domain.Status](), reflect.TypeFor[usbip.Status]()},
		{"PortID", reflect.TypeFor[domain.PortID](), reflect.TypeFor[usbip.PortID]()},
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
// use site — not just the same reflect.Type. A direct assignment in
// either direction only compiles when both names resolve to the same
// underlying type.
func TestBusIDValuesInterchangeable(t *testing.T) {
	t.Parallel()

	// A usbip.BusID literal assigns directly into a domain.BusID-typed
	// variable with no explicit conversion. That only type-checks when
	// the two names resolve to the same underlying type — the core
	// alias guarantee. The explicit parameter type on accept forces
	// the check at the call site.
	require.Equal(t, "1-1", acceptDomainBusID(usbip.BusID("1-1")))
	require.Equal(t, "1-1", acceptUsbipBusID(domain.BusID("1-1")))
}

// acceptDomainBusID returns b.String(). Accepting a domain.BusID-typed
// parameter at the use site forces the aliasing check when the caller
// passes a usbip.BusID.
func acceptDomainBusID(b domain.BusID) string { return b.String() }

// acceptUsbipBusID mirrors acceptDomainBusID with the roles swapped.
func acceptUsbipBusID(b usbip.BusID) string { return b.String() }

// TestEventInterfaceIsAliased proves usbip.Event is the same interface
// type as domain.Event. A domain event value must satisfy usbip.Event
// without an explicit conversion.
func TestEventInterfaceIsAliased(t *testing.T) {
	t.Parallel()

	// A domain.Event concrete value assigned into a usbip.Event-typed
	// parameter only type-checks when the two are the same interface.
	ev := domain.Event(domain.DeviceBoundEvent{})

	require.Equal(t, domain.EventDeviceBound, acceptUsbipEvent(ev))
	require.Equal(t,
		reflect.TypeFor[usbip.Event](),
		reflect.TypeFor[domain.Event](),
		"usbip.Event must alias domain.Event")
}

// acceptUsbipEvent forces the parameter's type to usbip.Event so the
// call site only type-checks when the caller's domain.Event value
// satisfies usbip.Event — proving the interface identity.
func acceptUsbipEvent(e usbip.Event) domain.EventKind { return e.EventKind() }
