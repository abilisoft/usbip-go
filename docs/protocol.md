# USB/IP wire protocol reference

Byte-level reference for the USB/IP handshake protocol as implemented
by `internal/adapter/wire`. The layouts described here are the
authoritative format the codec encodes and decodes against, so
integrators can read the wire without cross-referencing Go source.

The post-handshake URB traffic (`CMD_SUBMIT`, `RET_SUBMIT`, etc.) is
handled entirely by the kernel after the USBIP `sockfd` is handed off
via sysfs — it never touches the Go codec — so this document does not
cover URB packets.

## Endianness

Every multi-byte integer on the wire is network byte order
(big-endian). `encoding/binary.BigEndian` is the only encoding the
codec uses. `binary.NativeEndian` is never permitted.

String fields are fixed-size byte arrays; they have no endianness.
Short strings are NUL-padded to the declared width. The codec writes
NUL padding on encode and trims at the first NUL on decode.

## Protocol version

```
const ProtocolVersion uint16 = 0x0111   // 1.1.1
```

Every inbound or outbound OP header carries this version. A mismatch
on read returns `ErrProtocolMismatch` with the observed version in
the `oops` context.

## Common OP header (8 bytes)

Every request and reply begins with the same fixed-width header.

```
offset  size  field             notes
------  ----  ----------------  -----
   0      2   version           big-endian u16; must equal 0x0111
   2      2   opcode            big-endian u16
   4      4   status            big-endian u32; 0 on requests
```

### Opcodes

| Name | Value | Direction |
|---|---|---|
| `OpReqDevlist` | `0x8005` | client -> server |
| `OpRepDevlist` | `0x0005` | server -> client |
| `OpReqImport`  | `0x8003` | client -> server |
| `OpRepImport`  | `0x0003` | server -> client |

A non-zero `status` on a reply header is well-formed but signals a
server-side failure. The codec surfaces it as `ErrProtocolError`
with `status` in the `oops` context.

## `OP_REQ_DEVLIST` (client -> server)

Body: none. Header alone, 8 bytes. The server replies with
`OP_REP_DEVLIST` containing every exportable device.

## `OP_REP_DEVLIST` (server -> client)

```
offset  size   field
------  -----  ----------------------------------------------------
   0      4    nDevices                 (u32, device count)
   4      -    device[0]                (312 bytes + 4 * nInterfaces)
  ...    ...   device[1] ...
```

Offsets are relative to the start of the body (the 8-byte OP header
precedes it on the wire, so the `nDevices` field sits at absolute
offset 8 in the full reply). Each device record is a 312-byte fixed
descriptor followed by `bNumInterfaces` 4-byte interface descriptors.
See the descriptor layouts below.

`nDevices = 0` is legal. The codec returns `(nil, nil)` for an empty
listing.

Extra trailing bytes after all `nDevices` records are logged at warn
level and silently ignored (permissive on read).

## `OP_REQ_IMPORT` (client -> server)

```
offset  size  field
------  ----  ----------------------
   0     32   busid[32]     NUL-padded ASCII BusID
```

Total request size: 8 (header) + 32 (busid) = 40 bytes.

## `OP_REP_IMPORT` (server -> client)

```
offset  size  field
------  ----  ----------------------------------
   0    312   device descriptor   (see below)
```

Total reply size: 8 (header) + 312 (descriptor) = 320 bytes. The
server also hands the socket fd to the kernel via
`/sys/devices/platform/vhci_hcd.0/attach`; the reply confirms the
device the kernel now owns.

## Device descriptor (312 bytes)

```
offset  size  field                    wire type
------  ----  -----------------------  ---------
   0    256   path                     char[256]  NUL-padded ASCII
 256     32   busid                    char[32]   NUL-padded ASCII
 288      4   busnum                   u32  big-endian
 292      4   devnum                   u32  big-endian
 296      4   speed                    u32  big-endian (see Speed table)
 300      2   idVendor                 u16  big-endian
 302      2   idProduct                u16  big-endian
 304      2   bcdDevice                u16  big-endian
 306      1   bDeviceClass             u8
 307      1   bDeviceSubClass          u8
 308      1   bDeviceProtocol          u8
 309      1   bConfigurationValue      u8
 310      1   bNumConfigurations       u8
 311      1   bNumInterfaces           u8
```

`path` is the sysfs path (local only). For devices decoded from a
remote `OP_REP_DEVLIST`, the empty string is acceptable on the wire
and mapped to `Device.Path == ""`.

`busid` is the stable Linux USB topology identifier, e.g. `1-1.2`.
It is validated on decode against the pattern
`^[0-9]+-[0-9]+(\.[0-9]+)*$` — see `pkg/domain/busid.go`.

### Speed values

`speed` is a 32-bit integer. Canonical values match the kernel
`usb_device_speed` enum:

| Value | `pkg/domain.Speed` | Description |
|---|---|---|
| 0 | `SpeedUnknown` | unknown |
| 1 | `SpeedLow` | 1.5 Mbit/s |
| 2 | `SpeedFull` | 12 Mbit/s |
| 3 | `SpeedHigh` | 480 Mbit/s |
| 4 | `SpeedWireless` | wireless (historical) |
| 5 | `SpeedSuper` | 5 Gbit/s |
| 6 | `SpeedSuperPlus` | 10 Gbit/s |

## Interface descriptor (4 bytes each)

Every device in `OP_REP_DEVLIST` is followed by `bNumInterfaces`
4-byte interface descriptors:

```
offset  size  field                  wire type
------  ----  ---------------------  ---------
   0      1   bInterfaceClass        u8
   1      1   bInterfaceSubClass     u8
   2      1   bInterfaceProtocol     u8
   3      1   padding                u8  (reserved, zero on encode, ignored on decode)
```

**There is no `bAlternateSetting` on the wire.** The codec sets
`domain.Interface.Alt = 0` on decode. Alt is populated only from
local sysfs reads — see
[`internal/adapter/kernel`](../internal/adapter/kernel).

## Malformed-input error matrix

The codec enforces a defensive decode path. Every failure mode maps
to a public sentinel:

| Condition | Returned error | Wrap |
|---|---|---|
| Short read on OP header (<8 bytes) | `io.ErrUnexpectedEOF` | `oops.Wrap(err, "read op header")` |
| Clean EOF before any header byte | `io.EOF` (unchanged) | not wrapped |
| `version != 0x0111` | `ErrProtocolMismatch` | `oops.With("got", v).With("want", 0x0111)` |
| Unknown opcode in received header | `ErrProtocolMismatch` | `oops.With("opcode", op)` |
| `status != 0` on a reply | `ErrProtocolError` | `oops.With("status", s).With("op", op)` |
| Short read in body (truncated mid-field) | `io.ErrUnexpectedEOF` | `oops.With("field", name).Wrap(err)` |
| Padded string exceeds its fixed size | `ErrBusIDInvalid` (busid) / `ErrProtocolError` (path) | `oops.With("len", len).With("max", max)` |
| Non-NUL-terminated padded string | truncated at first non-printable / end of buffer; logged as `slog.Warn` | — |
| `OP_REP_DEVLIST` with `nDevices = 0` | valid; returns `(nil, nil)` | — |
| `OP_REP_DEVLIST` truncated mid-device | `io.ErrUnexpectedEOF` | `oops.With("truncated_at", N)` |
| `OP_REP_DEVLIST` with extra trailing bytes | logged as `slog.Warn`; silently ignored | — |
| Interface count exceeds remaining bytes | `io.ErrUnexpectedEOF` | `oops.With("msg", "truncated interfaces")` |

`ErrProtocolMismatch` covers "bytes don't match the spec at all".
`ErrProtocolError` covers "well-formed frames that encode a server-
reported failure" (non-zero `status`). They are two distinct
sentinels in `pkg/usbip/errors.go`.

Consumers match via `errors.Is` for sentinels or
`errors.As(&oopsErr)` for the enriched context attributes.

## Padded-string helpers

`internal/adapter/wire` exposes two helpers used by every field
with a fixed NUL-padded shape:

```go
func writePaddedString(w io.Writer, s string, size int) error
func readPaddedString(r io.Reader, size int) (string, error)
```

`writePaddedString` returns `ErrBusIDInvalid` (for busid-width
writes) or `ErrProtocolError` (for path-width writes) when `s`
exceeds `size - 1` bytes — the trailing byte is reserved for the NUL
terminator even when the string fills the field.

## Fixture-based conformance

The codec's inputs are pinned against real captures from upstream
`usbipd`. Fixtures live in
[`internal/adapter/wire/testdata/`](../internal/adapter/wire/testdata).
The conformance suite decodes each capture, re-encodes, and asserts
byte-for-byte equality — see [`wire-trace.md`](wire-trace.md) for
the capture recipe.
