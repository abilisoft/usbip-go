// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

var (
	errOtherReason   = errors.New("something else")
	errRandExhausted = errors.New("rand exhausted")
	errSomeOther     = errors.New("some other error")
)

// stringAddr is a net.Addr whose String() returns an arbitrary value.
// Used to test the host:port and bare-IP branches of ipFromAddr without
// needing a real *net.TCPAddr.
type stringAddr struct{ s string }

func (a stringAddr) Network() string { return "tcp" }
func (a stringAddr) String() string  { return a.s }

// TestIPFromAddr_Nil pins the nil-addr short-circuit: ipFromAddr must
// return nil when addr is nil so callers do not have to nil-check the
// return before using it.
func TestIPFromAddr_Nil(t *testing.T) {
	t.Parallel()

	require.Nil(t, ipFromAddr(nil))
}

// TestClassifyDecodeImportErr_AllRejectionSentinels pins the metric
// classification for every domain-rejection sentinel that can surface
// from DecodeOpRepImport. ST_DEV_BUSY (→ ErrDeviceAlreadyBound) and
// ST_DEV_ERR (→ ErrDeviceUnavailable) historically fell through to
// AttachOutcomeProtocolMismatch, mis-bucketing peer rejections as
// wire framing faults in observability dashboards.
func TestClassifyDecodeImportErr_AllRejectionSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want AttachOutcome
	}{
		{
			name: "ErrDeviceNotFound -> kernel_error",
			err:  domain.ErrDeviceNotFound,
			want: AttachOutcomeKernelError,
		},
		{
			name: "ErrDeviceAlreadyBound -> kernel_error",
			err:  domain.ErrDeviceAlreadyBound,
			want: AttachOutcomeKernelError,
		},
		{
			name: "ErrDeviceUnavailable -> kernel_error",
			err:  domain.ErrDeviceUnavailable,
			want: AttachOutcomeKernelError,
		},
		{
			name: "wire framing fault -> protocol_mismatch",
			err:  errSomeOther,
			want: AttachOutcomeProtocolMismatch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := classifyDecodeImportErr(tc.err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestIPFromAddr_TCPAddr pins the *net.TCPAddr fast-path: a well-typed
// addr yields its IP directly without going through string parsing.
func TestIPFromAddr_TCPAddr(t *testing.T) {
	t.Parallel()

	addr := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 1234}
	got := ipFromAddr(addr)
	require.Equal(t, addr.IP, got)
}

// TestIPFromAddr_HostPortString pins the ParseAddrPort branch: a
// host:port string addr must be parsed and the IP portion returned.
func TestIPFromAddr_HostPortString(t *testing.T) {
	t.Parallel()

	addr := stringAddr{"192.168.1.100:9999"}
	got := ipFromAddr(addr)
	require.NotNil(t, got)
	require.True(t, got.Equal(net.ParseIP("192.168.1.100")),
		"expected 192.168.1.100 but got %v", got)
}

// TestIPFromAddr_BareIPString pins the ParseAddr fallback branch: when
// the addr string is a bare IP (no port) ParseAddrPort fails and
// ParseAddr must succeed.
func TestIPFromAddr_BareIPString(t *testing.T) {
	t.Parallel()

	addr := stringAddr{"172.16.0.5"}
	got := ipFromAddr(addr)
	require.NotNil(t, got)
	require.True(t, got.Equal(net.ParseIP("172.16.0.5")),
		"expected 172.16.0.5 but got %v", got)
}

// TestIPFromAddr_Unparseable pins the fail-closed branch: an addr
// string that is neither host:port nor a bare IP must return nil so
// the ACL does not silently allow an unparseable peer.
func TestIPFromAddr_Unparseable(t *testing.T) {
	t.Parallel()

	addr := stringAddr{"not-an-ip-at-all"}
	got := ipFromAddr(addr)
	require.Nil(t, got)
}

// TestACLAllow_NilReceiver pins the nil-receiver branch of
// (*aclChecker).allow: a nil ACL must permit every peer (empty
// allow-list means permit-all per v1 contract §11.5.2).
func TestACLAllow_NilReceiver(t *testing.T) {
	t.Parallel()

	var a *aclChecker

	addr := &net.TCPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 9000}
	require.True(t, a.allow(addr),
		"nil aclChecker must permit all peers")
}

// TestACLAllow_UnparseableAddr pins the fail-closed branch of
// allow() when ipFromAddr returns nil: an unparseable addr is denied
// rather than permitted (defense-in-depth).
func TestACLAllow_UnparseableAddr(t *testing.T) {
	t.Parallel()

	a, err := parseACL([]string{"10.0.0.0/8"})
	require.NoError(t, err)

	addr := stringAddr{"not-parseable"}
	require.False(t, a.allow(addr),
		"unparseable addr must be denied (fail-closed)")
}

// TestNewAcceptLimiter_DisabledOnZeroRPS pins the rps<=0 branch of
// newAcceptLimiter: a zero or negative rps disables rate limiting
// entirely so the disabled allow() always returns true.
func TestNewAcceptLimiter_DisabledOnZeroRPS(t *testing.T) {
	t.Parallel()

	l := newAcceptLimiter(0, 10)
	require.Nil(t, l.bucket, "zero rps must produce a nil bucket (disabled)")
	require.True(t, l.allow(), "disabled limiter must always allow")

	l2 := newAcceptLimiter(-1.0, 10)
	require.Nil(t, l2.bucket, "negative rps must produce a nil bucket")
}

// TestNewAcceptLimiter_DefaultBurstOnZeroBurst pins the burst<=0 branch:
// when a non-zero rps is supplied but burst is zero, the limiter must
// fall back to defaultAcceptBurst so it is immediately usable.
func TestNewAcceptLimiter_DefaultBurstOnZeroBurst(t *testing.T) {
	t.Parallel()

	l := newAcceptLimiter(5.0, 0)
	require.NotNil(t, l.bucket, "non-zero rps with zero burst must produce an active limiter")
	require.Equal(t, defaultAcceptBurst, l.bucket.Burst(),
		"zero burst must resolve to defaultAcceptBurst")
}

// TestAcceptLimiter_NilBucketAlwaysAllows pins the nil-bucket branch of
// allow(): a zero-value acceptLimiter (no bucket) must always return
// true, mirroring the "disabled" semantics documented in the code.
func TestAcceptLimiter_NilBucketAlwaysAllows(t *testing.T) {
	t.Parallel()

	l := acceptLimiter{}
	require.True(t, l.allow())
}

// TestHandshakeLimitReader_UnboundedPassthrough pins the readUnbounded
// path (limit<=0): the reader must pass bytes through classifyReadErr
// without any byte-counting overhead.
func TestHandshakeLimitReader_UnboundedPassthrough(t *testing.T) {
	t.Parallel()

	src := strings.NewReader("hello world")
	r := newHandshakeLimitReader(src, 0)

	var buf bytes.Buffer

	n, err := r.Read(make([]byte, 5))
	require.NoError(t, err)
	require.Equal(t, 5, n)

	_ = buf
}

// TestClassifyDisconnectReason_ContextCanceled pins the context.Canceled
// branch: a canceled-context error must map to DisconnectReasonShutdown
// so dashboards count graceful shutdown disconnects separately from
// kernel errors.
func TestClassifyDisconnectReason_ContextCanceled(t *testing.T) {
	t.Parallel()

	got := classifyDisconnectReason(context.Canceled)
	require.Equal(t, DisconnectReasonShutdown, got)
}

// TestClassifyDisconnectReason_OtherError pins the fallthrough branch:
// an unrecognised error maps to KernelError.
func TestClassifyDisconnectReason_OtherError(t *testing.T) {
	t.Parallel()

	got := classifyDisconnectReason(errOtherReason)
	require.Equal(t, DisconnectReasonKernelError, got)
}

// TestEventEndsSession_DeviceUnbound pins the DeviceUnboundEvent case
// in eventEndsSessionForBusID: a matching busID must return true so
// waitForSessionEnd unwinds the handler.
func TestEventEndsSession_DeviceUnbound(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")

	ev := domain.DeviceUnboundEvent{Device: domain.Device{BusID: busID}}
	require.True(t, eventEndsSessionForBusID(ev, busID),
		"DeviceUnboundEvent for same busID must end the session")
}

// TestEventEndsSession_DeviceUnbound_DifferentBusID pins the non-match
// case: a DeviceUnboundEvent for a different busID must not end this session.
func TestEventEndsSession_DeviceUnbound_DifferentBusID(t *testing.T) {
	t.Parallel()

	ev := domain.DeviceUnboundEvent{Device: domain.Device{BusID: "2-1"}}
	require.False(t, eventEndsSessionForBusID(ev, domain.BusID("1-1")))
}

// TestEventEndsSession_UnrelatedEvent pins the default branch: event
// types other than PortDetachedEvent and DeviceUnboundEvent must return
// false so unrelated events do not prematurely end the session.
func TestEventEndsSession_UnrelatedEvent(t *testing.T) {
	t.Parallel()

	ev := domain.DeviceBoundEvent{Device: domain.Device{BusID: "1-1"}}
	require.False(t, eventEndsSessionForBusID(ev, domain.BusID("1-1")),
		"DeviceBoundEvent must not end the session")
}

// TestSessionIDError_ErrorAndUnwrap pins newSessionIDError and the two
// methods it produces: Error() must embed the underlying message and
// Unwrap() must return the exact original error so errors.Is chains
// work downstream.
func TestSessionIDError_ErrorAndUnwrap(t *testing.T) {
	t.Parallel()

	cause := errRandExhausted
	wrapped := newSessionIDError(cause)

	require.Contains(t, wrapped.Error(), "generate session id")
	require.Contains(t, wrapped.Error(), "rand exhausted")
	require.ErrorIs(t, wrapped, cause,
		"errors.Is must reach the original cause via Unwrap")
}

// TestClassifyBindError_AllCases pins every branch of classifyBindError
// so a mutation that removes or swaps cases surfaces as a test failure.
func TestClassifyBindError_AllCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		err     error
		outcome BindOutcome
	}{
		{"already_bound", domain.ErrDeviceAlreadyBound, BindOutcomeAlreadyBound},
		{"not_found", domain.ErrDeviceNotFound, BindOutcomeNotFound},
		{"permission", domain.ErrPermission, BindOutcomePermission},
		{"other", errSomeOther, BindOutcomeError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := classifyBindError(tc.err)
			require.Equal(t, tc.outcome, got)
		})
	}
}

// TestClassifyUnbindError_AllCases pins every branch of classifyUnbindError.
func TestClassifyUnbindError_AllCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		err     error
		outcome UnbindOutcome
	}{
		{"not_bound", domain.ErrDeviceNotBound, UnbindOutcomeNotBound},
		{"permission", domain.ErrPermission, UnbindOutcomePermission},
		{"other", errSomeOther, UnbindOutcomeError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := classifyUnbindError(tc.err)
			require.Equal(t, tc.outcome, got)
		})
	}
}

// TestFixedBackoffReset_Whitebox pins that FixedBackoff.Reset() is callable
// without panicking. FixedBackoff carries no mutable state so Reset is
// a no-op; the test exists to ensure the function body is reachable and
// the coverage profile records it.
func TestFixedBackoffReset_Whitebox(t *testing.T) {
	t.Parallel()

	b := FixedBackoff{Delay: 5 * 1e9} // 5s
	b.Reset()                         // must not panic

	require.Equal(t, 5*int64(1e9), b.Next(0).Nanoseconds())
}

// TestExponentialBackoffReset_Whitebox pins that ExponentialBackoff.Reset()
// is callable. The implementation is a no-op (pure function of attempt),
// but coverage requires it to be exercised.
func TestExponentialBackoffReset_Whitebox(t *testing.T) {
	t.Parallel()

	b := NewExponentialBackoff(ExponentialBackoffConfig{
		Min: 10 * 1e6, // 10ms
		Max: 1 * 1e9,  // 1s
	})
	b.Reset() // must not panic or error
}
