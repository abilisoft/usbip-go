# Troubleshooting

Operator-first triage guide. Start with the decision tree for the
most common failure, then fall through to the per-error reference
for anything the tree doesn't cover.

## Decision tree: "my device won't attach"

```text
START: usbip-go attach HOST BUSID fails.
  |
  +-- Error text contains "kernel module missing"?
  |     |
  |     +-- YES -> sudo modprobe vhci_hcd usbip_core
  |     |          Run again. If it still fails, check
  |     |          dmesg for vhci_hcd load errors and
  |     |          verify the kernel has CONFIG_USBIP_VHCI_HCD=m.
  |     |
  |     +-- NO  -> continue.
  |
  +-- Error text contains "permission denied" or "operation not permitted"?
  |     |
  |     +-- YES -> Are you running as root or with CAP_SYS_ADMIN+CAP_DAC_OVERRIDE?
  |     |           |
  |     |           +-- NO  -> sudo the client OR setcap the binary:
  |     |           |          sudo setcap 'cap_sys_admin,cap_dac_override=+ep' /usr/bin/usbip-go
  |     |           |
  |     |           +-- YES -> Is SELinux / AppArmor enforcing on /sys/devices/platform/vhci_hcd.0?
  |     |                      Check dmesg + auditd. Adjust policy or set permissive
  |     |                      temporarily to isolate.
  |     |
  |     +-- NO  -> continue.
  |
  +-- Error text contains "device not found"?
  |     |
  |     +-- YES -> Does the exporter currently advertise the device?
  |     |          |
  |     |          +-- On importer: usbip-go list HOST
  |     |          +-- If absent, on exporter: usbip-go list
  |     |          +-- If present locally: sudo usbip-go bind BUSID
  |     |          +-- If absent locally: device is not attached to the exporter host.
  |     |
  |     +-- NO  -> continue.
  |
  +-- Error text contains "no free port"?
  |     |
  |     +-- YES -> usbip-go port  (check currently-attached ports)
  |     |          Detach something: usbip-go detach <port_id>
  |     |          Or boot with vhci_hcd num_ports=N for a larger table:
  |     |          echo 'options vhci_hcd num_ports=16' | sudo tee /etc/modprobe.d/vhci.conf
  |     |
  |     +-- NO  -> continue.
  |
  +-- Error text contains "protocol mismatch" or "protocol error"?
  |     |
  |     +-- YES -> Server reports a USB/IP version != 0x0111.
  |     |          Either the server is a non-standard build or the wire
  |     |          is corrupted (NAT, middlebox, MTU). Capture a trace:
  |     |          see wire-trace.md.
  |     |
  |     +-- NO  -> continue.
  |
  +-- Error text contains "dial" / "connection refused" / "i/o timeout"?
  |     |
  |     +-- YES -> curl -v telnet://HOST:3240 (or `nc HOST 3240`) from the client.
  |     |          Succeeds? Daemon is running but rejecting you -> check --allow-cidr on server.
  |     |          Refused? Daemon is down -> systemctl status usbip-go on server.
  |     |          Timeout? Firewall or network path -> check iptables/nftables and route.
  |     |
  |     +-- NO  -> continue.
  |
  +-- Error text contains "already bound"?
  |     |
  |     +-- YES -> An importer already owns the exported device.
  |     |          Check the exporter status socket for an active Session.
  |     |          Unbind only after confirming ownership is stale; doing so
  |     |          interrupts an active importer.
  |     |
  |     +-- NO  -> continue.
  |
  +-- Still failing? Run with --log-level=trace on both sides and
      open an issue with the trace + wire capture (wire-trace.md).
```

## Common error sentinels

The public API exposes the following common sentinels for `errors.Is`
classification. The CLI maps them to stable exit codes and human-readable
stderr; it does not print Go identifier names.

| Sentinel | Typical cause | First remediation |
|---|---|---|
| `ErrKernelModuleMissing` | `vhci_hcd` / `usbip_host` / `usbip_core` not loaded, or no access to `/sys/module/`. | Load the role modules with `sudo modprobe usbip_core usbip_host` on exporters or `sudo modprobe usbip_core vhci_hcd` on importers. |
| `ErrPermission` | Sysfs write needs `CAP_SYS_ADMIN` + `CAP_DAC_OVERRIDE`. | `sudo` the caller or `setcap` the binary. |
| `ErrDeviceNotFound` | BusID does not exist on the exporter, is not bound, or is not currently advertised. | Compare `usbip-go list HOST` with the exporter's local `usbip-go list`, then bind the local target if appropriate. |
| `ErrDeviceNotBound` | An exporter device is not bound, or a validated importer detach found that the VHCI Port was already free. | Refresh local/exported device state or `usbip-go port`; do not repeat a detach for a Port the kernel already considers free. |
| `ErrDeviceAlreadyBound` | A bind was repeated or an importer already owns the device. | Inspect active Sessions; unbind only when ownership is confirmed stale. |
| `ErrNoFreePort` | All vhci ports are occupied. | Detach a port, or boot the kernel with more vhci ports. |
| `ErrProtocolMismatch` | Server sent a version byte != `0x0111` or unknown opcode. | Version mismatch or corrupted wire. Capture with tcpdump. |
| `ErrProtocolError` | Server sent a well-formed OP frame with a non-zero `status` on a reply other than `OP_REP_IMPORT` (which maps to `ErrDeviceNotFound`). | Check server logs for the underlying reason. |
| `ErrBusIDInvalid` | Busid does not match `^[0-9]+-[0-9]+(\.[0-9]+)*$`. | Re-read from `usbip-go list`; do not edit by hand. |
| `ErrImporterClosed` | `Importer.Close()` already ran. | Construct a new `Importer`. |
| `ErrExporterShutdown` | `Exporter.Shutdown()` already ran. | Construct a new `Exporter`. |
| `ErrServeAlreadyRunning` | Second `Serve` on the same `Exporter`. | `Serve` instances are single-use. |

## Recovering from missing kernel modules

```text
$ lsmod | grep -E 'usbip|vhci'
# No output — modules are not loaded.

$ sudo modprobe usbip_core
$ sudo modprobe vhci_hcd
$ sudo modprobe usbip_host

$ test -d /sys/module/usbip_core && echo usbip_core-loaded
$ ls /sys/devices/platform/vhci_hcd.0/
```

If `modprobe` says "module not found", the kernel was built without
USB/IP support. Check `CONFIG_USBIP_CORE`, `CONFIG_USBIP_VHCI_HCD`,
and `CONFIG_USBIP_HOST` in your kernel config. Install the distribution's
matching kernel-modules package or select a kernel that enables those options;
the userspace `linux-tools` package alone does not add missing kernel modules.

The `.deb` and `.rpm` packages install persistent module-loading
configuration. Archive and `go install` users should load the needed
role modules before running commands.

## Recovering from a stuck port

Each CLI invocation creates a fresh Importer. A later
`usbip-go detach <port_id>` process delegates to authoritative `vhci_hcd` state
and does not need to be the process that performed the attach. Start with the
supported path:

```text
$ usbip-go port
$ sudo usbip-go detach <port_id>
$ usbip-go port
```

The CLI Port view hides normalized `null` and `available` capacity rows. It
keeps ordinary `not-assigned` rows visible because the kernel has claimed those
virtual devices but has not assigned their local USB addresses yet. The public
Go `Importer.ListPorts` API intentionally returns all normalized usable kernel
rows so library callers can inspect capacity. If detach reports that the device
is not bound, refresh `usbip-go port`; the kernel already considers that valid
Port free.

Do not confuse a genuine claimed `not-assigned` row with Linux's
`status_show_not_ready` placeholder. An ordinary row uses the normal six-digit
socket field, such as `000000`. The synthetic placeholder uses the exact
otherwise-zero not-assigned shape with a sixteen-zero socket field,
`0000000000000000`, while the VHCI controller lacks driver data. The kernel
adapter rejects that snapshot: `Importer.ListPorts` and allocation return an
error with no partial or synthetic Ports. Retry after the controller becomes
ready rather than interpreting the placeholder as attached devices or free
capacity.

For deeper diagnosis, inspect each controller's raw status file:

```text
$ cat /sys/devices/platform/vhci_hcd.*/status
```

Importer rows use `VDEV_ST_USED` and `VDEV_ST_ERROR` semantics, not exporter
`SDEV_ST_*` states. The status row's `local_busid` names the importer-side USB
device and can differ from the exporter BusID; correlate lifecycle operations
with the kernel-flat PortID returned by attach. Direct sysfs detach is a last
resort after recording the CLI error, proving that exact PortID is active and
in range, and ensuring no concurrent attach or reconnect owns it. The
controller detach file at `/sys/devices/platform/vhci_hcd.0/detach` expects the
verified kernel-flat PortID used by the CLI; do not convert it to a
controller-local coordinate or echo a guessed ID.

Only after those checks, a verified Port in `VDEV_ST_ERROR` or
`VDEV_ST_USED` can be force-detached via sysfs:

```text
$ echo <verified_port_id> | sudo tee /sys/devices/platform/vhci_hcd.0/detach
```

## Daemon not accepting connections

```text
$ sudo systemctl status usbip-go usbip-go.socket
$ sudo journalctl -u usbip-go -f
```

Socket-activation quirk: `systemctl status usbip-go` may show
"inactive (dead)" between clients. That is normal — the socket unit
accepts inbound TCP and wakes the daemon on demand.

Verify the listener is bound:

```text
$ sudo ss -tnlp | grep 3240
LISTEN 0  128  0.0.0.0:3240  0.0.0.0:*  users:(("systemd",pid=1,fd=NN))
```

If the listener is absent, the socket unit failed. Check its
logs:

```text
$ sudo journalctl -u usbip-go.socket -f
```

## When to capture a wire trace

Escalate to [`wire-trace.md`](wire-trace.md) when:

- The error mentions `ErrProtocolMismatch` or `ErrProtocolError`.
- A client works against one server but not another.
- You need to file an upstream-interop bug.
- The client hangs mid-handshake with no error for >30 seconds.

A pcap plus the daemon trace log is almost always enough to
diagnose the fault.
