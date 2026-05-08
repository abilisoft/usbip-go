# Troubleshooting

Operator-first triage guide. Start with the decision tree for the
most common failure, then fall through to the per-error reference
for anything the tree doesn't cover.

## Decision tree: "my device won't attach"

```
START: usbip attach HOST BUSID fails.
  |
  +-- Error text contains "kernel module missing"?
  |     |
  |     +-- YES -> sudo modprobe vhci-hcd usbip-core
  |     |          Run again. If it still fails, check
  |     |          dmesg for vhci-hcd load errors and
  |     |          verify the kernel has CONFIG_USBIP_VHCI_HCD=m.
  |     |
  |     +-- NO  -> continue.
  |
  +-- Error text contains "permission denied" or "operation not permitted"?
  |     |
  |     +-- YES -> Are you running as root or with CAP_SYS_ADMIN+CAP_DAC_OVERRIDE?
  |     |           |
  |     |           +-- NO  -> sudo the client OR setcap the binary:
  |     |           |          sudo setcap 'cap_sys_admin,cap_dac_override=+ep' /usr/bin/usbip
  |     |           |
  |     |           +-- YES -> Is SELinux / AppArmor enforcing on /sys/devices/platform/vhci_hcd.0?
  |     |                      Check dmesg + auditd. Adjust policy or set permissive
  |     |                      temporarily to isolate.
  |     |
  |     +-- NO  -> continue.
  |
  +-- Error text contains "device not found"?
  |     |
  |     +-- YES -> Is the device actually exported on the server?
  |     |          |
  |     |          +-- On server: usbip list --local
  |     |          +-- If listed but not bound: usbip bind BUSID
  |     |          +-- If not listed: device is not attached to the server host.
  |     |
  |     +-- NO  -> continue.
  |
  +-- Error text contains "no free port"?
  |     |
  |     +-- YES -> usbip port  (check currently-attached ports)
  |     |          Detach something: usbip detach <port_id>
  |     |          Or boot with vhci-hcd num_ports=N for a larger table:
  |     |          echo 'options vhci-hcd num_ports=16' | sudo tee /etc/modprobe.d/vhci.conf
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
  |     |          Refused? Daemon is down -> systemctl status usbipd on server.
  |     |          Timeout? Firewall or network path -> check iptables/nftables and route.
  |     |
  |     +-- NO  -> continue.
  |
  +-- Error text contains "already bound"?
  |     |
  |     +-- YES -> Server already exports the device to someone (possibly you, stale).
  |     |          Server: usbip unbind BUSID; then retry attach.
  |     |
  |     +-- NO  -> continue.
  |
  +-- Still failing? Run with --log-level=trace on both sides and
      open an issue with the trace + wire capture (wire-trace.md).
```

## Common error sentinels

Every error returned by `pkg/usbip` is classifiable via `errors.Is`
against one of these sentinels. The CLI surfaces the sentinel name
in log output so you can grep directly.

| Sentinel | Typical cause | First remediation |
|---|---|---|
| `ErrKernelModuleMissing` | `vhci-hcd` / `usbip-host` / `usbip-core` not loaded, or no access to `/sys/module/`. | `sudo modprobe vhci-hcd usbip-core usbip-host`. Add to `/etc/modules-load.d/`. |
| `ErrPermission` | Sysfs write needs `CAP_SYS_ADMIN` + `CAP_DAC_OVERRIDE`. | `sudo` the caller or `setcap` the binary. |
| `ErrDeviceNotFound` | BusID does not exist on the server, or is not bound. | Server: `usbip list --local`; bind the target with `usbip bind`. |
| `ErrDeviceAlreadyBound` | Device is already exported. | Server: `usbip unbind BUSID` then retry. |
| `ErrNoFreePort` | All vhci ports are occupied. | Detach a port, or boot the kernel with more vhci ports. |
| `ErrProtocolMismatch` | Server sent a version byte != `0x0111` or unknown opcode. | Version mismatch or corrupted wire. Capture with tcpdump. |
| `ErrProtocolError` | Server sent a well-formed OP frame with a non-zero `status`. | Check server logs for the underlying reason. |
| `ErrBusIDInvalid` | Busid does not match `^[0-9]+-[0-9]+(\.[0-9]+)*$`. | Re-read from `usbip list --local`; do not edit by hand. |
| `ErrImporterClosed` | `Importer.Close()` already ran. | Construct a new `Importer`. |
| `ErrExporterShutdown` | `Exporter.Shutdown()` already ran. | Construct a new `Exporter`. |
| `ErrServeAlreadyRunning` | Second `Serve` on the same `Exporter`. | `Serve` instances are single-use. |

## Recovering from missing kernel modules

```
$ lsmod | grep -E 'usbip|vhci'
# No output — modules are not loaded.

$ sudo modprobe usbip_core
$ sudo modprobe vhci-hcd
$ sudo modprobe usbip-host

$ cat /sys/module/usbip_core/version
$ ls /sys/devices/platform/vhci_hcd.0/
```

If `modprobe` says "module not found", the kernel was built without
USB/IP support. Check `CONFIG_USBIP_CORE`, `CONFIG_USBIP_VHCI_HCD`,
and `CONFIG_USBIP_HOST` in your kernel config. Distributions usually
ship them as loadable modules in `linux-tools-$(uname -r)` or
`kmod-usbip`.

Make the modules persistent:

```
echo -e 'usbip_core\nvhci_hcd\nusbip_host' | sudo tee /etc/modules-load.d/usbip-go.conf
```

## Recovering from a stuck port

`usbip detach N` fails with `ErrDeviceNotFound` when the kernel
still owns the port but the daemon lost track. Check the kernel
view:

```
$ cat /sys/devices/platform/vhci_hcd.0/status
```

Ports in `SDEV_ST_ERROR` or `SDEV_ST_USED` that are not in
`usbip port` output can be force-detached via sysfs:

```
$ echo <port_id> | sudo tee /sys/devices/platform/vhci_hcd.0/detach
```

This is the last-resort path; prefer `usbip detach` when it works.

## Daemon not accepting connections

```
$ sudo systemctl status usbipd usbipd.socket
$ sudo journalctl -u usbipd -f
```

Socket-activation quirk: `systemctl status usbipd` may show
"inactive (dead)" between clients. That is normal — the socket unit
accepts inbound TCP and wakes the daemon on demand.

Verify the listener is bound:

```
$ sudo ss -tnlp | grep 3240
LISTEN 0  128  0.0.0.0:3240  0.0.0.0:*  users:(("systemd",pid=1,fd=NN))
```

If the listener is absent, the socket unit failed. Check its
logs:

```
$ sudo journalctl -u usbipd.socket -f
```

## When to capture a wire trace

Escalate to [`wire-trace.md`](wire-trace.md) when:

- The error mentions `ErrProtocolMismatch` or `ErrProtocolError`.
- A client works against one server but not another.
- You need to file an upstream-interop bug.
- The client hangs mid-handshake with no error for >30 seconds.

A pcap plus the daemon trace log is almost always enough to
diagnose the fault.
