// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration

import (
	"fmt"
	"net"
	"strconv"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// ListenerCloser is the minimal net.Listener surface the integration
// tests need: Close to release the socket, LocalAddr accessor is via
// the net.Listener embedding so no method is re-declared. Tests use it
// so they can Close() the listener without retaining a concrete type
// across helper boundaries.
type ListenerCloser interface {
	net.Listener
}

// TCPListen binds addr on the TCP stack and returns the listener plus
// the resolved RemoteEndpoint so a client can dial it. addr is passed
// through to net.Listen so a :0 port picks a kernel-chosen port that
// LocalAddr then reports. The RemoteEndpoint's Port is parsed from the
// listener's actual addr string because addr may have been :0.
func TCPListen(addr string) (ListenerCloser, domain.RemoteEndpoint, error) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, domain.RemoteEndpoint{}, fmt.Errorf("tcp listen on %s: %w", addr, err)
	}

	host, portStr, splitErr := net.SplitHostPort(lis.Addr().String())
	if splitErr != nil {
		_ = lis.Close()

		return nil, domain.RemoteEndpoint{}, fmt.Errorf("split listener addr: %w", splitErr)
	}

	port, convErr := strconv.ParseUint(portStr, 10, 16)
	if convErr != nil {
		_ = lis.Close()

		return nil, domain.RemoteEndpoint{}, fmt.Errorf("parse listener port: %w", convErr)
	}

	return lis, domain.RemoteEndpoint{Host: host, Port: uint16(port)}, nil
}
