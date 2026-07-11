// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire

import "github.com/abilisoft/usbip-go/internal/protocol"

// OpCode is the shared USB/IP handshake opcode type.
type OpCode = protocol.OpCode

// OpCode constants as defined in wire-protocol OpenSpec.
const (
	// OpReqDevlist is the client's request to list exportable devices.
	OpReqDevlist = protocol.OpReqDevlist
	// OpRepDevlist is the server's response to OpReqDevlist.
	OpRepDevlist = protocol.OpRepDevlist
	// OpReqImport is the client's request to import (attach) a device.
	OpReqImport = protocol.OpReqImport
	// OpRepImport is the server's response to OpReqImport.
	OpRepImport = protocol.OpRepImport
)

// isKnownOpCode reports whether op is one of the four supported USBIP
// handshake opcodes. Anything else triggers ErrProtocolMismatch per the
// spec error matrix (wire-protocol OpenSpec).
func isKnownOpCode(op OpCode) bool {
	switch op {
	case OpReqDevlist, OpRepDevlist, OpReqImport, OpRepImport:
		return true
	default:
		return false
	}
}
