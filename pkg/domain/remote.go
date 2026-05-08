package domain

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ErrRemoteEndpointInvalid is returned when ParseRemote cannot interpret its input.
var ErrRemoteEndpointInvalid = errors.New("invalid remote endpoint")

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

// ParseRemote accepts "host", "host:port", "[v6]:port", or bare "v6"
// forms and returns a RemoteEndpoint. Port defaults to DefaultPort
// when omitted. Port 0 is rejected as a sentinel value.
func ParseRemote(s string) (RemoteEndpoint, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return RemoteEndpoint{}, fmt.Errorf("%w: empty", ErrRemoteEndpointInvalid)
	}

	host, portStr, err := splitHostPort(s)
	if err != nil {
		return RemoteEndpoint{}, err
	}

	if host == "" {
		return RemoteEndpoint{}, fmt.Errorf("%w: empty host", ErrRemoteEndpointInvalid)
	}

	if portStr == "" {
		return RemoteEndpoint{Host: host, Port: DefaultPort}, nil
	}

	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return RemoteEndpoint{}, fmt.Errorf("%w: port %q: %w", ErrRemoteEndpointInvalid, portStr, err)
	}

	if port == 0 {
		return RemoteEndpoint{}, fmt.Errorf("%w: port 0 is reserved", ErrRemoteEndpointInvalid)
	}

	return RemoteEndpoint{Host: host, Port: uint16(port)}, nil
}

// splitHostPort splits s into host and port strings, handling IPv6
// bracket syntax. Returns ("host", "", nil) for bare hosts without a
// colon-delimited port, and ("host", "port", nil) for standard forms.
func splitHostPort(s string) (string, string, error) {
	// IPv6 with brackets: "[::1]:1234" (or just "[::1]").
	if strings.HasPrefix(s, "[") {
		closeIdx := strings.IndexByte(s, ']')
		if closeIdx < 0 {
			return "", "", fmt.Errorf("%w: missing ] in %q", ErrRemoteEndpointInvalid, s)
		}

		host := s[1:closeIdx]
		rest := s[closeIdx+1:]

		switch {
		case rest == "":
			return host, "", nil
		case strings.HasPrefix(rest, ":"):
			return host, rest[1:], nil
		default:
			return "", "", fmt.Errorf("%w: unexpected suffix %q", ErrRemoteEndpointInvalid, rest)
		}
	}

	// Bare IPv6 (contains multiple colons, no brackets): treat as host-only.
	if strings.Count(s, ":") > 1 {
		return s, "", nil
	}

	// host or host:port.
	if idx := strings.LastIndexByte(s, ':'); idx >= 0 {
		return s[:idx], s[idx+1:], nil
	}

	return s, "", nil
}
