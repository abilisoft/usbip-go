package domain

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
)

// errInvalidRemote is the base error for remote-endpoint parsing failures.
// Unexported — the spec does not define a public sentinel for this class
// of error, so we keep it internal to satisfy err113 without expanding
// the public API surface. All fmt.Errorf calls in this file wrap this
// sentinel via %w so every returned error is classifiable via
// errors.Is(err, errInvalidRemote) for internal use.
var errInvalidRemote = errors.New("invalid remote endpoint")

// ipv6MinColons is the minimum colon count for a bare multi-colon string
// to be interpreted as an IPv6 literal.
const ipv6MinColons = 2

// hostLabelMaxLen is the RFC 1034 maximum length of a DNS label in bytes.
const hostLabelMaxLen = 63

// RemoteEndpoint identifies a USB/IP peer by host and port.
//
// Port zero is a sentinel meaning "use DefaultPort". The library never
// attempts to dial port 0; callers either construct a RemoteEndpoint
// with an explicit port or leave Port at its zero value to accept the
// default. Any library method that consumes a RemoteEndpoint calls
// NormalizePort internally, so the literal RemoteEndpoint{Host: "h"}
// behaves as "h:3240".
type RemoteEndpoint struct {
	Host string
	Port uint16 // zero means DefaultPort
}

// String returns "host:port" using DefaultPort when r.Port is zero.
// IPv6 hosts are bracketed via net.JoinHostPort.
func (r RemoteEndpoint) String() string {
	port := r.Port
	if port == 0 {
		port = DefaultPort
	}

	return net.JoinHostPort(r.Host, strconv.FormatUint(uint64(port), 10))
}

// NormalizePort returns a copy of r with Port set to DefaultPort when
// the original Port was zero. The input is unchanged.
func (r RemoteEndpoint) NormalizePort() RemoteEndpoint {
	if r.Port == 0 {
		r.Port = DefaultPort
	}

	return r
}

// ParseRemote accepts "host", "host:port", "[v6]:port", bare "v6"
// (multi-colon), or bracketed "[v6]" forms. Port defaults to
// DefaultPort when omitted. Port 0 is rejected as a sentinel value.
// Host strings are validated: no whitespace, no control characters,
// multi-colon forms must parse as a valid IP literal, and hostnames
// must satisfy RFC 1034 + RFC 1123 label rules.
func ParseRemote(s string) (RemoteEndpoint, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return RemoteEndpoint{}, fmt.Errorf("%w: empty", errInvalidRemote)
	}

	host, portStr, err := splitHostPort(s)
	if err != nil {
		return RemoteEndpoint{}, err
	}

	if host == "" {
		return RemoteEndpoint{}, fmt.Errorf("%w: empty host in %q", errInvalidRemote, s)
	}

	err = validateHost(host)
	if err != nil {
		return RemoteEndpoint{}, err
	}

	if portStr == "" {
		return RemoteEndpoint{Host: host, Port: DefaultPort}, nil
	}

	return parseRemoteWithPort(host, portStr)
}

// parseRemoteWithPort converts a textual port to a validated
// RemoteEndpoint. Splitting this out keeps ParseRemote's cognitive
// complexity below the project's cap of 10.
func parseRemoteWithPort(host, portStr string) (RemoteEndpoint, error) {
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return RemoteEndpoint{}, fmt.Errorf("%w: port %q: %w", errInvalidRemote, portStr, err)
	}

	if port == 0 {
		return RemoteEndpoint{}, fmt.Errorf("%w: port 0 is reserved", errInvalidRemote)
	}

	return RemoteEndpoint{Host: host, Port: uint16(port)}, nil
}

// splitHostPort splits s into host and port strings, handling IPv6
// bracket syntax. Returns ("host", "", nil) for bare hosts without a
// colon-delimited port, and ("host", "port", nil) for standard forms.
func splitHostPort(s string) (string, string, error) {
	if strings.HasPrefix(s, "[") {
		return splitBracketedIPv6(s)
	}

	// Bare IPv6 (contains multiple colons, no brackets): treat as
	// host-only. validateHost later checks that it parses as an IP.
	if strings.Count(s, ":") >= ipv6MinColons {
		return s, "", nil
	}

	// host or host:port.
	idx := strings.LastIndexByte(s, ':')
	if idx >= 0 {
		return s[:idx], s[idx+1:], nil
	}

	return s, "", nil
}

// splitBracketedIPv6 handles "[host]" and "[host]:port" forms.
func splitBracketedIPv6(s string) (string, string, error) {
	closeIdx := strings.IndexByte(s, ']')
	if closeIdx < 0 {
		return "", "", fmt.Errorf("%w: missing ] in %q", errInvalidRemote, s)
	}

	host := s[1:closeIdx]
	rest := s[closeIdx+1:]

	switch {
	case rest == "":
		return host, "", nil
	case strings.HasPrefix(rest, ":"):
		return host, rest[1:], nil
	default:
		return "", "", fmt.Errorf("%w: unexpected suffix %q", errInvalidRemote, rest)
	}
}

// validateHost enforces basic sanity on a host string. IPv6 text (which
// contains colons) must parse via net.ParseIP. Hostname labels follow
// RFC 1034 + RFC 1123: each label is 1..63 ASCII alphanumerics or
// hyphens, labels cannot start or end with a hyphen, and no whitespace
// or control characters are allowed anywhere.
func validateHost(h string) error {
	if h == "" {
		return fmt.Errorf("%w: empty host", errInvalidRemote)
	}

	err := hostCharClassCheck(h)
	if err != nil {
		return err
	}

	// Multi-colon => must be a valid IP literal.
	if strings.Count(h, ":") >= ipv6MinColons {
		if net.ParseIP(h) == nil {
			return fmt.Errorf("%w: host %q has multiple colons but is not a valid IP", errInvalidRemote, h)
		}

		return nil
	}

	// IPv4 literal shortcut — accept regardless of hostname rules.
	// Single-colon strings cannot reach here because splitHostPort
	// always peels a port off them, so no colon handling is needed.
	if net.ParseIP(h) != nil {
		return nil
	}

	return validateHostnameLabels(h)
}

// hostCharClassCheck rejects whitespace and control characters anywhere
// in the host string.
func hostCharClassCheck(h string) error {
	for _, r := range h {
		if unicode.IsSpace(r) {
			return fmt.Errorf("%w: host contains whitespace", errInvalidRemote)
		}

		if unicode.IsControl(r) {
			return fmt.Errorf("%w: host contains control character", errInvalidRemote)
		}
	}

	return nil
}

// validateHostnameLabels applies RFC 1034 + RFC 1123 per-label rules to
// a hostname. A trailing dot (FQDN absolute form) is permitted.
func validateHostnameLabels(h string) error {
	labeled := strings.TrimSuffix(h, ".")
	if labeled == "" {
		return fmt.Errorf("%w: host is a bare dot", errInvalidRemote)
	}

	for label := range strings.SplitSeq(labeled, ".") {
		err := validateHostLabel(label)
		if err != nil {
			return err
		}
	}

	return nil
}

// validateHostLabel enforces RFC 1034 + RFC 1123 per-label rules.
func validateHostLabel(label string) error {
	if label == "" {
		return fmt.Errorf("%w: empty hostname label (leading, trailing, or consecutive dot)", errInvalidRemote)
	}

	if len(label) > hostLabelMaxLen {
		return fmt.Errorf("%w: hostname label %q exceeds %d bytes", errInvalidRemote, label, hostLabelMaxLen)
	}

	for i, r := range label {
		err := validateHostLabelRune(label, r, i)
		if err != nil {
			return err
		}
	}

	return nil
}

// isHostLabelChar reports whether r is a permitted character in a
// hostname label: ASCII letter, digit, or hyphen.
func isHostLabelChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '-':
		return true
	default:
		return false
	}
}

// validateHostLabelRune validates a single rune in a hostname label.
func validateHostLabelRune(label string, r rune, i int) error {
	if !isHostLabelChar(r) {
		return fmt.Errorf("%w: hostname label %q contains invalid character %q at offset %d", errInvalidRemote, label, r, i)
	}

	if r == '-' && (i == 0 || i == len(label)-1) {
		return fmt.Errorf("%w: hostname label %q has leading or trailing hyphen", errInvalidRemote, label)
	}

	return nil
}
