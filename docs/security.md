# Security posture

This document states the operator-facing security model for usbip-go.
It is deliberately blunt: USB/IP is a plaintext, unauthenticated
protocol, and this library matches upstream `usbip-utils` behaviour.
Nothing in this implementation makes the protocol safer than the
network you run it on.

The authoritative requirements are in
`openspec/specs/security-release-quality/spec.md`; this document is
the consolidated operator reference.

## Threat model

**Assume the wire is trusted.** USB/IP has no in-protocol
authentication, no encryption, and no integrity check. Anyone who
can TCP-connect to port 3240 can:

- Issue `OP_REQ_DEVLIST` and learn every exported device.
- Issue `OP_REQ_IMPORT` and attach a device to themselves.
- Send arbitrary URBs to the attached device until the kernel
  detaches them.

The kernel enforces correctness of the URB stream, not
authorisation of the peer.

**Deploy USB/IP only over networks you already trust** — private
LANs, WireGuard or Tailscale overlays, SSH tunnels, or localhost
between cooperating processes. Do not expose port 3240 on the public
internet. Do not expose it on a shared or guest network.

## TLS is out of scope

The project does not wrap USB/IP in TLS. The upstream
kernel/userspace ecosystem does not either; doing so unilaterally
would break interop and create a false sense of security (the
kernel-owned URB path after handoff would still be plaintext).
See `openspec/specs/transport-networking/spec.md` — TLS remains out
of scope for the USB/IP wire path.

If you need confidentiality on the wire, tunnel the TCP connection
itself: WireGuard, Tailscale, an SSH `-L` forward, or stunnel in
front of the daemon. These approaches integrate transparently; the client
and daemon see a plain TCP connection.

## Defence-in-depth tools the daemon provides

### CIDR allow-list

`usbip-go serve --allow-cidr` (repeatable) short-circuits any accepted
connection whose remote address is not inside at least one listed
CIDR. Default: empty list, permit all — matching upstream behaviour.
One or more entries switches the daemon into fail-closed ACL mode.

Example:

```text
usbip-go serve --allow-cidr 10.0.0.0/8 --allow-cidr 192.168.0.0/16
```

Rejected connections are emitted as structured `slog` records with
`outcome="rejected_acl"`, so operators can observe probe traffic
through journald or log aggregation.

This is an enforcement seam, not authentication. A host inside your
allow-list is trusted by the daemon regardless of who is at the
keyboard. Pair CIDR allow-listing with a firewall or WireGuard ACL
for real protection.

### Resource limits

The daemon ships with bounds on every accept-side resource. Flags,
defaults, purpose:

| Flag | Default | Purpose |
|---|---|---|
| `--max-sessions` | 128 | Total concurrent sessions. New accepts past this count are politely refused. |
| `--max-sessions-per-peer` | 8 | Per-source-IP cap. Prevents a single malicious client exhausting the global budget. |
| `--accept-rate-limit` | `10/s` | Token bucket on new accepts. Excess connections are closed immediately. |
| `--max-handshake-bytes` | 64 KiB | Hard cap per OP request/response. Rejects malicious oversized payloads. |
| `--handshake-timeout` | `10s` | Deadline on completing a handshake. Slowloris defence. |
| `--shutdown-timeout` | `30s` | Graceful drain budget before force-close. |

Every rejection is logged with a closed-set `outcome` field so
operators can track ambient abuse without packet captures.

### Systemd hardening

The project-supplied unit at
[`contrib/systemd/usbip-go.service`](../contrib/systemd/usbip-go.service)
pins:

```ini
CapabilityBoundingSet=CAP_SYS_ADMIN CAP_DAC_OVERRIDE CAP_CHOWN
```

This caps the daemon's reachable capability set even when it runs as root.
`CAP_SYS_ADMIN` and `CAP_DAC_OVERRIDE` cover USB/IP sysfs operations;
`CAP_CHOWN` lets the daemon apply `--status-socket-group`. Operators who run
the daemon via their own unit file should pin only the capabilities required
by their configuration, plus the standard hardening directives
(`NoNewPrivileges=yes`, `ProtectSystem=strict`,
`ProtectHome=true`, `PrivateTmp=yes`, `RestrictSUIDSGID=yes`).

## Privilege model

usbip-go writes to sysfs under
`/sys/bus/usb/drivers/usbip-host/` and
`/sys/devices/platform/vhci_hcd.0/`. Those nodes default to mode
`0200` owned by root, so `CAP_SYS_ADMIN` plus `CAP_DAC_OVERRIDE` are
required in practice.

Four deployment patterns, in order of decreasing privilege:

1. **Run as root.** Simplest; the default systemd unit does this.
2. **Run under a dedicated `usbip-go` user with a custom systemd unit and
   `setcap`.** Configure `User=usbip-go` and a group that can own the status
   socket, then grant the sysfs capabilities:

   ```text
   sudo setcap 'cap_sys_admin,cap_dac_override=+ep' /usr/bin/usbip-go
   ```

   Then the daemon starts as the `usbip-go` system user, not root.
3. **Create a `usbip-go` Unix group and expose the status UDS to
   members only.** The daemon's `--status-socket-group usbip-go` flag
   `chgrp`s the UDS to that group at bind time, with mode `0660`.
   Operators who need to call `usbip-go drain` join `usbip-go`; regular
   users do not.
4. **No setcap, drop privileges** — not supported today. Exporter handoff and
   device lifecycle operations continue to require privileged sysfs access
   after startup, so the daemon does not have an unprivileged steady state.

The library never silently succeeds with insufficient privileges.
Sysfs access paths map `EACCES` and `EPERM` to `usbip.ErrPermission` while
preserving the operation, sysfs path, and underlying errno. The CLI maps that
sentinel to its permission exit code; this document identifies the capabilities
the operator must grant.

## Kernel modules

The exporter daemon requires `usbip_core` and `usbip_host`; importer commands
require `usbip_core` and `vhci_hcd`. Role-specific operations probe their
required modules before kernel work. Missing modules yield
`ErrKernelModuleMissing` with a `modprobe` hint. The status document reports
all three module states, while `/readyz` gates only on the exporter pair.

The status-socket JSON surfaces module state so operators can alert
on regressions through log/status polling. See [`ops.md`](ops.md)
for the operational recipe.

## Summary checklist

Before deploying to production:

- [ ] Port 3240 is not reachable from untrusted networks.
- [ ] Clients and servers are on a common trusted overlay
      (WireGuard, Tailscale, SSH tunnel, private VLAN) or
      firewalled.
- [ ] `--allow-cidr` is set when the daemon accepts from multiple
      IP ranges.
- [ ] Resource-limit flags are tuned for expected fan-out.
- [ ] Structured logs with `outcome="rejected_*"` are wired into alerting.
- [ ] Systemd unit pins `CapabilityBoundingSet` and standard
      hardening directives.
- [ ] Status-socket `kernel_modules` state is polled or otherwise alerted on.
- [ ] You have accepted the plaintext-protocol warning in writing —
      the library does not, and will not, make USB/IP secure on
      its own.
