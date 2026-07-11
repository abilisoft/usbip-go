# Capturing USB/IP wire traces

Every non-trivial bug report on the protocol path benefits from a
pcap plus the trace-level daemon log. This document records the
tcpdump / tshark recipe used internally to regenerate the wire
fixtures that pin the codec, and the recipe operators should follow
when filing bug reports.

See also:

- [`scripts/capture-wire-fixtures.sh`](../scripts/capture-wire-fixtures.sh)
  — the scripted version that regenerates the project's own fixtures
  against upstream `usbipd` + `usbip-vudc`.
- [`internal/adapter/wire/conformance_test.go`](../internal/adapter/wire/conformance_test.go)
  — reviewed captures stored as inline hexadecimal constants; temporary binary
  extracts are intentionally ignored.
- [`protocol.md`](protocol.md) — byte-level spec for every frame the
  capture contains.

## What to capture

USB/IP handshake traffic is entirely on TCP port 3240. Everything
after the handshake is kernel-owned URB traffic that flows over the
same socket but is handed off to the kernel via
`/sys/devices/platform/vhci_hcd.0/attach` — that traffic never
touches our codec, so fixture captures only need the handshake
exchange.

The handshake for a successful attach consists of two TCP streams:

- `usbip-go list HOST` — `OP_REQ_DEVLIST` then `OP_REP_DEVLIST`.
- `usbip-go attach HOST BUSID` — `OP_REQ_IMPORT` then
  `OP_REP_IMPORT`.

Both requests open their own TCP connection. The list connection closes after
its reply; a successful attach reads the full import reply and then hands that
connection's socket fd to the kernel for URB traffic. Keep the capture running
until the command completes so the final handshake bytes are present, and use
the size gates to avoid including post-handoff URBs in a fixture.

## Live capture recipe

```text
# 1. On the client host, start tcpdump BEFORE running the client.
sudo tcpdump -i any -s 0 -w /tmp/usbip-trace.pcap 'tcp port 3240' &
TCPDUMP_PID=$!

# 2. Drive the failing client command.
usbip-go list 10.0.0.5
usbip-go attach 10.0.0.5 1-1.2

# 3. Stop tcpdump.
sudo kill "$TCPDUMP_PID"
```

For a loopback capture on the server itself, replace `-i any` with
`-i lo`. Loopback frames have no link layer, which simplifies
payload extraction (see below).

Keep `-s 0` — truncated payloads are the single most common reason
captures are useless for bug reports. `0` means "no snap length
limit".

## Inspecting with tshark

```text
tshark -r /tmp/usbip-trace.pcap -Y 'tcp.port == 3240 && tcp.len > 0' \
  -T fields -e frame.number -e tcp.stream \
  -e ip.src -e tcp.srcport -e ip.dst -e tcp.dstport -e tcp.len
```

A successful `usbip-go list` + `usbip-go attach` pair produces two TCP streams
and four USB/IP application messages. It does **not** imply four packets: TCP
may segment or retransmit any message. Reconstruct each stream and direction,
then apply the message-size gates below.

## Extracting payload bytes

`tshark -T fields -e tcp.payload` prints every matching packet's payload as a
hex string. First identify the list and attach stream numbers from the table
above. Then extract one stream and direction at a time; the example below uses
`STREAM=0` and must be repeated for the other stream:

```text
STREAM=0
for dir in req rep; do
  case "$dir" in
    req) f="tcp.stream == ${STREAM} && tcp.dstport == 3240 && tcp.len > 0" ;;
    rep) f="tcp.stream == ${STREAM} && tcp.srcport == 3240 && tcp.len > 0" ;;
  esac
  tshark -r /tmp/usbip-trace.pcap -Y "$f" -T fields -e tcp.payload \
    | tr -d '\n:' \
    | xxd -r -p > "/tmp/usbip-trace.$dir.bin"
done
```

The resulting `.req.bin` / `.rep.bin` concatenate every captured payload in
that stream and direction. A single USB/IP message can span multiple TCP
segments, so taking only the first segment produces false short reads. A
retransmission can duplicate bytes in this simple extraction; the size gates
must reject that capture unless it is reconstructed with TCP reassembly.

## Size gates

Every payload size is pinned by `openspec/specs/wire-protocol/spec.md`. Abort any fixture
regeneration where the payload is outside its documented range:

| Frame | Expected size |
|---|---|
| `OP_REQ_DEVLIST` | exactly 8 bytes |
| `OP_REP_DEVLIST` | 12 bytes when `nDevices = 0`, otherwise 12 + 312 × nDevices + 4 × sum(bNumInterfaces) bytes (8 header + 4 device-count + per-device payload) |
| `OP_REQ_IMPORT` | exactly 40 bytes (8 header + 32 busid) |
| `OP_REP_IMPORT` | 8 bytes on error (header-only, non-zero `status`) or 320 bytes on success |

A byte count outside these ranges means the pcap truncated the body
or caught post-handshake URB bytes. In both cases the fixture is
invalid and must be recaptured. `scripts/capture-wire-fixtures.sh`
enforces this automatically.

## Regenerating the project's test fixtures

The project's reviewed inline hex captures in
`internal/adapter/wire/conformance_test.go` are regenerated from temporary
files produced by
[`scripts/capture-wire-fixtures.sh`](../scripts/capture-wire-fixtures.sh)
on a Linux host with:

- Kernel modules: `usbip_core`, `vhci_hcd`, `usbip_host`,
  `usbip_vudc`, `libcomposite`, `usb_f_mass_storage`.
- Upstream userspace: `usbip` and `usbipd` from the kernel tools
  (`linux-tools-$(uname -r)` on Debian/Ubuntu).
- `tcpdump`, `tshark`, `xxd`, and a pseudo-UDC provided by
  `usbip_vudc`.

The script:

1. Loads the kernel modules and mounts configfs if needed.
2. Creates a virtual UDC gadget with known `idVendor` /
   `idProduct` / `bcdDevice` so fixtures are stable across runs.
3. Starts upstream `usbipd -e` (device/vudc mode) against port
   3240, checking that no foreign daemon already holds the port.
4. Starts `tcpdump` on loopback.
5. Runs upstream `usbip list -r 127.0.0.1` and
   `usbip attach -r 127.0.0.1 -b $BUSID`.
6. Stops `tcpdump`, extracts each request and reply payload via
   `tshark`, enforces the size gates, and writes the binaries into
   `internal/adapter/wire/testdata/`.
7. Emits a temporary JSON manifest (`real_capture.manifest.json`) with capture
   time, kernel version, upstream usbip-utils version, and per-file size.
8. Cleans up the gadget, the daemon, and the tcpdump process via
   `trap`.

Review the generated bytes against the existing inline constants, update those
constants only when the protocol evidence justifies a change, and delete the
temporary capture artifacts. `.gitignore` prevents accidental commits of the
binary extracts and pcap.

## Include with every bug report

For a protocol-path bug report, attach:

1. The pcap (`/tmp/usbip-trace.pcap` or equivalent).
2. Trace-level daemon log from the exporter:

   ```text
   sudo usbip-go serve --log-level=trace 2>&1 | tee /tmp/usbip-serve.log
   ```

3. Output of `usbip-go version` (or `cat /proc/version`).
4. The status-socket snapshot:

   ```text
   sudo curl --unix-socket /run/usbip-go/status.sock \
     http://unused/ | tee /tmp/status.json
   ```

5. `dmesg | grep -E 'usbip|vhci'` from both client and server hosts
   for the time window of the capture.

With those five artefacts, the bug is almost always reproducible
from first principles — no back-and-forth about kernel versions or
module state.
