# Single binary: usbip

Status: supersedes the two-binary design (usbip-go + usbipd-go).

We ship one binary (`usbip`) with subcommands for all roles. Daemon
mode is `usbip serve`; client operations are `usbip attach`, `usbip
detach`, `usbip list`, etc.

The upstream reference (`usbip` + `usbipd`) split binaries to match
its C Makefile structure and CAP_SYS_ADMIN requirements. Neither
constraint applies here. Go cross-compilation and `go install` make
single-binary distribution trivially simple. Privilege differences
between subcommands are handled at the OS level — systemd
`CapabilityBoundingSet`, `setcap`, or a sudoers entry — not by
shipping separate executables.

The practical benefits: one thing to install and version, one man page,
one AppArmor profile with mode-specific rules, one systemd
`ExecStart=` line. Operators who need strict binary-level separation
can symlink or wrap the single binary; that is simpler than the reverse
(merging two binaries downstream).

**Considered options:**
- Two binaries mirroring upstream — rejected: binary splitting is a C
  build artifact, not a design principle. Privilege separation at the
  OS level is more flexible and does not require downstream operators to
  install two packages.
- Separate packages per role — rejected for the same reason; adds
  packaging complexity with no security benefit that `setcap` does not
  already provide.
