# Two BusID validators: ParseBusID and ValidateWireBusID

BusID validation uses two distinct functions with different acceptance
rules at different trust boundaries.

`ParseBusID` applies the strict Linux USB topology pattern
(`^[0-9]+-[0-9]+(\.[0-9]+)*$`). It is used for all user-facing entry
points — CLI arguments, config values, `Attach` options. It rejects
anything that does not look like a real kernel USB address.

`ValidateWireBusID` applies a permissive charset check
(`[A-Za-z0-9._-]`) instead of the topology regex. It is used when
decoding the 32-byte busid field from a peer's OP_REP_IMPORT frame,
because real-world exporters include non-standard ids such as
`usbip-vudc.0` (the virtual UDC used in kernel self-tests) that the
topology regex would reject, causing a false decode failure.

The key security constraint: the permissive charset still explicitly
excludes `/`, whitespace, and control bytes because the decoded busid
flows directly into `path.Join` when the exporter opens per-device
sysfs attributes. Allowing a path separator would be a path-traversal
vulnerability. DRY was intentionally sacrificed here — a single
validator that satisfied both constraints would either falsely reject
valid wire peers or open the traversal surface.
