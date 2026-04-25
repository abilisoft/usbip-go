// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build conformance_linux

// Package conformance hosts wire-level conformance tests for
// usbip-go. Tests build with //go:build conformance_linux so they
// compile on hosted CI without pulling in the integration suite's
// kernel-module dependency. See v1 contract §8.9 for the hosted-CI contract.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// SyntheticUpstream is a TCP server that accepts a single client,
// reads the OP header, and replays a fixture reply that matches the
// opcode. Tests use it to exercise our Importer's handshake without
// needing a real upstream usbipd installed (or its kernel-module
// baggage). The server handles the three opcodes our Importer
// issues: OP_REQ_DEVLIST and OP_REQ_IMPORT; OP_REP_* originate from
// here, committed inline as decoded Go structs so the repo never
// ships binary fixture files.
//
// One connection is accepted per Start() to keep the state machine
// simple — tests that need multiple sessions invoke Start() again
// with a fresh listener.
type SyntheticUpstream struct {
	// DeviceReply is the domain.Device the fixture hands out for
	// both OP_REQ_DEVLIST (as the single-device array) and
	// OP_REQ_IMPORT (as the imported device). Tests set it before
	// Start so the assertion is deterministic.
	DeviceReply domain.Device

	// ImportStatus stores the status byte echoed in OP_REP_IMPORT
	// replies. Zero means success; non-zero triggers the error
	// branch of our Importer's handshake.
	ImportStatus uint32

	lis    net.Listener
	done   chan struct{}
	mu     sync.Mutex
	err    error
	closed bool
	// receivedBusID captures the BusID the handler read off an inbound
	// OP_REQ_IMPORT so tests can assert the client transmitted the
	// expected fixture value. Accessor-guarded by mu for the same
	// reason err is: handler goroutine writes, test reads.
	receivedBusID domain.BusID
}

// StartSyntheticUpstream binds a fresh 127.0.0.1:0 listener and
// spawns the handler goroutine. Returns the server (for Addr and
// Close) and the resolved endpoint so tests can dial without having
// to parse lis.Addr themselves.
func StartSyntheticUpstream(device domain.Device) (*SyntheticUpstream, domain.RemoteEndpoint, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, domain.RemoteEndpoint{}, fmt.Errorf("listen synthetic upstream: %w", err)
	}

	s := &SyntheticUpstream{
		DeviceReply: device,
		lis:         lis,
		done:        make(chan struct{}),
	}

	go s.handle()

	addr, splitErr := endpointFromListener(lis)
	if splitErr != nil {
		_ = lis.Close()

		return nil, domain.RemoteEndpoint{}, splitErr
	}

	return s, addr, nil
}

// endpointFromListener parses lis.Addr() into a RemoteEndpoint. The
// kernel-picked port on a :0 bind is only available via this string;
// we centralise the parsing so every caller agrees on the format.
func endpointFromListener(lis net.Listener) (domain.RemoteEndpoint, error) {
	host, portStr, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		return domain.RemoteEndpoint{}, fmt.Errorf("split addr: %w", err)
	}

	var port uint16

	_, scanErr := fmt.Sscanf(portStr, "%d", &port)
	if scanErr != nil {
		return domain.RemoteEndpoint{}, fmt.Errorf("parse port %q: %w", portStr, scanErr)
	}

	return domain.RemoteEndpoint{Host: host, Port: port}, nil
}

// handle accepts one connection, decodes the opcode, dispatches to
// the matching reply encoder, and closes the connection. Errors are
// stored on the server struct so the test can inspect them after
// Close().
func (s *SyntheticUpstream) handle() {
	defer close(s.done)

	conn, err := s.lis.Accept()
	if err != nil {
		// Accept may return net.ErrClosed if the test calls
		// Close() before any client connects; that is not an
		// error — leave s.err nil.
		if !errors.Is(err, net.ErrClosed) {
			s.setErr(fmt.Errorf("accept: %w", err))
		}

		return
	}

	defer func() { _ = conn.Close() }()

	// 5-second read deadline so a hung client does not wedge the
	// handler; tests set their own ctx-driven deadline but the
	// belt-and-braces timeout here keeps the server from leaking
	// if a future test forgets to send a header.
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	err = s.serveOne(conn)
	if err != nil {
		s.setErr(err)
	}
}

// serveOne consumes one opcode header from r, dispatches to the
// matching reply encoder, and returns. Each opcode's body is read
// verbatim so the server's reply carries the correct data (e.g. the
// busid for OP_REQ_IMPORT). Unknown opcodes produce an error that
// the test inspects via Err().
func (s *SyntheticUpstream) serveOne(conn net.Conn) error {
	version, op, _, err := wire.DecodeHeader(conn)
	if err != nil {
		return fmt.Errorf("decode header: %w", err)
	}

	if version != domain.ProtocolVersion {
		return fmt.Errorf("unexpected version %#x", version)
	}

	switch op {
	case wire.OpReqDevlist:
		// No body to consume; reply straight away.
		err = wire.EncodeOpRepDevlist(conn, []domain.Device{s.DeviceReply})
		if err != nil {
			return fmt.Errorf("encode OP_REP_DEVLIST: %w", err)
		}
	case wire.OpReqImport:
		// Consume the 32-byte busid body so the reply pipeline
		// does not race the client's next write. DecodeHeader
		// already consumed the 8-byte header above; ReadPaddedString
		// is the exact "body without header" helper.
		busid, _, readErr := wire.ReadPaddedString(conn, domain.BusIDSize)
		if readErr != nil {
			return fmt.Errorf("read OP_REQ_IMPORT busid: %w", readErr)
		}

		// Capture the inbound BusID so tests can assert the client
		// transmitted the expected fixture value. Without this the
		// handler replied with s.DeviceReply regardless of request,
		// silently masking client-side BusID mismatches.
		s.setReceivedBusID(domain.BusID(busid))

		if s.ImportStatus != 0 {
			_, err = conn.Write(wire.EncodeHeader(wire.OpRepImport, s.ImportStatus))
			if err != nil {
				return fmt.Errorf("write OP_REP_IMPORT error header: %w", err)
			}

			return nil
		}

		err = wire.EncodeOpRepImport(conn, s.DeviceReply)
		if err != nil {
			return fmt.Errorf("encode OP_REP_IMPORT: %w", err)
		}
	default:
		return fmt.Errorf("unsupported opcode %#x", op)
	}

	return nil
}

// setErr stores the first error observed by the handler goroutine.
// Subsequent errors are dropped — handle() returns after the first.
func (s *SyntheticUpstream) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err == nil {
		s.err = err
	}
}

// setReceivedBusID stores the BusID parsed out of the inbound
// OP_REQ_IMPORT body under s.mu so ReceivedBusID can read it safely
// after the handler goroutine returns.
func (s *SyntheticUpstream) setReceivedBusID(busID domain.BusID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.receivedBusID = busID
}

// ReceivedBusID returns the BusID the handler captured from the
// inbound OP_REQ_IMPORT, or the zero value if no OP_REQ_IMPORT has
// been served. Call after Close() returns so the handler has
// finished writing to s.receivedBusID.
func (s *SyntheticUpstream) ReceivedBusID() domain.BusID {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.receivedBusID
}

// Err returns the error the handler goroutine observed, if any. Call
// only after Close() has returned so the handler has finished
// writing to s.err.
func (s *SyntheticUpstream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.err
}

// Close stops the listener and waits for the handler goroutine to
// exit. Idempotent.
func (s *SyntheticUpstream) Close() error {
	s.mu.Lock()

	if s.closed {
		s.mu.Unlock()

		return nil
	}

	s.closed = true

	s.mu.Unlock()

	_ = s.lis.Close()

	<-s.done

	return nil
}

// Addr returns the listener's address. Callers that already have
// the RemoteEndpoint from StartSyntheticUpstream don't need this;
// tests that build their own net.Dialer use it.
func (s *SyntheticUpstream) Addr() net.Addr {
	return s.lis.Addr()
}

// checkContextCompile is an unused placeholder that keeps the
// context import from being unused; some future checkpoint-driven
// branches may wire ctx into the handler directly.
var (
	_ = context.Background
	_ = io.Copy
)
