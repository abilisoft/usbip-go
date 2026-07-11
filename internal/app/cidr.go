// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"net"
	"net/netip"
)

// aclChecker holds a pre-parsed set of CIDR prefixes. A nil receiver
// (or one with no nets) is the "allow everyone" form per security-release-quality OpenSpec
// — ACL is defense-in-depth, not mandatory.
type aclChecker struct {
	nets []*net.IPNet
}

// parseACL compiles cidrs into an aclChecker. Returns ErrACLInvalid
// wrapped with the offending string on the first parse failure so the
// operator can correct the config before Serve starts — catching this
// at Serve time would be a silent security footgun. An empty cidrs
// slice yields an empty checker whose allow() method permits every
// peer; the caller does not have to branch on a nil pointer.
func parseACL(cidrs []string) (*aclChecker, error) {
	nets := make([]*net.IPNet, 0, len(cidrs))

	for _, s := range cidrs {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %w", ErrACLInvalid, s, err)
		}

		nets = append(nets, n)
	}

	return &aclChecker{nets: nets}, nil
}

// allow reports whether addr should be permitted. A nil receiver
// always permits (matches the "empty allow-list means permit-all"
// contract in security-release-quality OpenSpec). An unparseable addr is rejected because
// the default for defense-in-depth is fail-closed.
func (a *aclChecker) allow(addr net.Addr) bool {
	if a == nil || len(a.nets) == 0 {
		return true
	}

	ip := ipFromAddr(addr)
	if ip == nil {
		return false
	}

	for _, n := range a.nets {
		if n.Contains(ip) {
			return true
		}
	}

	return false
}

// ipFromAddr extracts the net.IP from a net.Addr, handling both
// *net.TCPAddr and host:port strings returned by less-typed addresses.
// Returns nil when no IP is recoverable.
func ipFromAddr(addr net.Addr) net.IP {
	if addr == nil {
		return nil
	}

	if t, ok := addr.(*net.TCPAddr); ok && t != nil {
		return t.IP
	}

	ap, err := netip.ParseAddrPort(addr.String())
	if err == nil {
		a := ap.Addr()

		return net.IP(a.AsSlice())
	}

	a, err := netip.ParseAddr(addr.String())
	if err != nil {
		return nil
	}

	return net.IP(a.AsSlice())
}
