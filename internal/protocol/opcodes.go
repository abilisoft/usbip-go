// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package protocol

// OpCode is a USB/IP handshake opcode transmitted in the eight-byte OP header.
type OpCode uint16

// USB/IP handshake opcodes.
const (
	OpReqDevlist OpCode = 0x8005
	OpRepDevlist OpCode = 0x0005
	OpReqImport  OpCode = 0x8003
	OpRepImport  OpCode = 0x0003
)

// OP_REP_IMPORT status values from the Linux USB/IP protocol.
const (
	ImportStatusNA      uint32 = 1
	ImportStatusDevBusy uint32 = 2
	ImportStatusDevErr  uint32 = 3
	ImportStatusNoDev   uint32 = 4
)
