// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire

// OpCode is a USBIP handshake opcode as transmitted in the 8-byte
// OP header (v1 contract §6.2). Values are big-endian u16 on the wire.
type OpCode uint16

// OpCode constants as defined in v1 contract §6.2.
const (
	// OpReqDevlist is the client's request to list exportable devices.
	OpReqDevlist OpCode = 0x8005
	// OpRepDevlist is the server's response to OpReqDevlist.
	OpRepDevlist OpCode = 0x0005
	// OpReqImport is the client's request to import (attach) a device.
	OpReqImport OpCode = 0x8003
	// OpRepImport is the server's response to OpReqImport.
	OpRepImport OpCode = 0x0003
)

// isKnownOpCode reports whether op is one of the four supported USBIP
// handshake opcodes. Anything else triggers ErrProtocolMismatch per the
// spec error matrix (§6.2).
func isKnownOpCode(op OpCode) bool {
	switch op {
	case OpReqDevlist, OpRepDevlist, OpReqImport, OpRepImport:
		return true
	default:
		return false
	}
}

// isReplyOpCode reports whether op is an OP_REP_* reply opcode. Status
// byte != 0 in a reply header is a fatal protocol error; the same
// condition on a request is impossible in practice (clients send
// status=0), so the decoder only flags status on replies.
//
// Callers MUST first validate op via isKnownOpCode — an unknown opcode
// returns false from this function, matching the conservative "not a
// reply" interpretation.
func isReplyOpCode(op OpCode) bool {
	return op == OpRepDevlist || op == OpRepImport
}
