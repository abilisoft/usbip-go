// Package wire implements the USB/IP OP-level protocol codec: opcodes,
// header layout, device descriptor (312 bytes), OP_REQ_DEVLIST /
// OP_REP_DEVLIST, and OP_REQ_IMPORT / OP_REP_IMPORT. Encoding is
// byte-faithful to the upstream kernel implementation.
package wire
