## Context

The audit found four independent fail-open paths at release and operator
boundaries. The LCOV checker turns an empty denominator into a successful
percentage, the Bazel release configuration enables stamping without wiring
workspace-status values into the Go binary, table cells accept USB descriptor
controls before styling, and the event-only `iter.Seq` cannot distinguish a
clean stop from a failed or lost kernel event source.

The fixes cross shell tooling, Bazel/rules_go, the CLI, `internal/app`, and the
public `pkg/usbip` facade. They must keep normal builds deterministic and
hermetic, preserve the v1 `Importer.Watch` signature, leave JSON contracts
unchanged, and add no runtime dependency.

## Goals / Non-Goals

**Goals:**

- Fail closed when an included LCOV data set has no executable lines.
- Make `make dist` binaries report repository-derived version, commit, and
  deterministic build-date metadata.
- Prevent Device- or peer-controlled terminal controls from reaching the human
  table renderer while preserving printable Unicode.
- Give assured event consumers and the CLI a terminal error channel without
  breaking source compatibility for existing `Watch` callers.
- Protect each behavior with deterministic focused tests, API baselines, main
  specs, and traceability evidence.

**Non-Goals:**

- Changing measured coverage percentages or exclusions when their denominators
  are nonzero.
- Adding wall-clock timestamps, network lookups, or another release system.
- Sanitizing the machine-readable JSON model or removing project-owned ANSI
  styling.
- Removing or changing the signature of `Importer.Watch`.
- Guaranteeing delivery of every app-synthesized event to a slow watcher; the
  existing bounded fanout policy remains unchanged.

## Decisions

### Reject a zero LCOV denominator before percentage calculation

The aggregate checker will test `total_found == 0` after exclusions and exit
with an actionable error before evaluating any threshold. This treats empty,
missing-`LF`, all-zero-`LF`, and excluded-only reports consistently as absent
evidence. A mixed report with an `LF:0` package and measured packages remains
valid: thresholds use the measured aggregate, while the zero-line package is
reported as not coverable and receives no invented percentage or package
threshold result. A hermetic shell test will cover all cases.

Computing `0/0` as either zero or one hundred percent was rejected for both the
aggregate and an individual package: both invent a result when the underlying
evidence is absent.

### Escape control code points at the human table-row boundary

Every dynamic table cell will pass through one helper before it is handed to
lipgloss. Unicode Cc code points (C0, DEL, and C1, including ESC/CSI/OSC
introducers and their C0 terminators) will become visible lowercase `\xNN`
text. Printable Unicode will pass through byte-for-byte. Applying the helper
to complete dynamic rows, rather than only `Manufacturer` and `Product`, makes
the security invariant local to the renderer and also protects future peer
controlled cells.

JSON rendering will not call this helper: `encoding/json` retains its current
schema and escaping semantics. Stripping controls was rejected because it
hides device data and can concatenate otherwise distinct text. Escaping all
non-ASCII text was rejected because it harms legitimate internationalized USB
descriptors.

### Make the error-aware iterator primary and retain `Watch` as a wrapper

`internal/app.Importer.WatchWithErrors` will return
`iter.Seq2[domain.Event, error]`. Ordinary values yield `(event, nil)`;
subscription failure yields a wrapped underlying error once; and an upstream
channel close while both the caller context and Importer remain live yields a
stable internal `ErrEventStreamClosed`. Caller cancellation and `Importer.Close`
end cleanly. Closure classification will re-check both states after receiving
the closed channel so simultaneous shutdown cannot be misreported as source
failure.

The existing lazy subscription and merged app-event fanout remain. The v1
`Watch` method will range the new iterator, forward only ordinary events, and
stop on its first error, preserving its exact `iter.Seq[Event]` shape and legacy
silent-terminal behavior. `pkg/usbip` will expose an additive
`WatchWithErrors`, translate the internal closure sentinel to a public
`ErrEventStreamClosed`, and otherwise preserve wrapped causes.

The CLI-local Importer interface and mocks will use `WatchWithErrors` for
`usbip-go watch`. The command will return a wrapped runtime error on a terminal
stream error and remain successful on signal/context cancellation. Replacing
`Watch` outright was rejected as a public v1 source break; logging alone was
rejected because automation still receives a successful exit status.

### Stamp only release-configured Bazel binaries from stable Git status

The `go_binary` will map fully qualified package variables `version`, `commit`,
and `buildDate` to `{STABLE_VERSION}`, `{STABLE_GIT_COMMIT}`, and
`{STABLE_BUILD_DATE}` through rules_go `x_defs`. The existing release
configuration's global `--stamp` remains the single switch; ordinary unstamped
builds retain the compiled `dev`/`none`/`unknown` fallbacks.

The workspace-status command will emit only stable values used by artifacts.
`STABLE_BUILD_DATE` will be the current commit's strict ISO-8601 committer date,
not the invocation wall clock, so identical source inputs produce identical
metadata and cache keys. Canonical release tags are `vMAJOR.MINOR.PATCH`; the
shared version helper will match that grammar and remove the leading `v` for
PEP 440/package metadata. Development versions continue to include commit
distance, short SHA, and dirty state.

Using `date(1)` was rejected because it makes outputs change without a source
change. Stamping through ambient `go build -ldflags` or Make was rejected
because Bazel must own declared inputs and the release graph.

### Verify contracts at their owning boundaries

Shell tests will build temporary Git repositories with fixed commit dates to
exercise exact tags, development commits, dirty state, fallback state, and
stable workspace-status output. Those tests require real Git semantics, so they
will resolve one exact host Git executable, pass it through `HARNESS_GIT`, and
carry narrow `local` and `requires-git` tags. No pinned Git distribution exists
in the repository tool lock; substituting a fake implementation would stop
testing the behavior that derives release provenance from Git.

A dedicated `make check-release-stamping` gate will build the production
`//cmd/usbip-go:usbip-go` target under `--config=release` with a committed,
constant workspace-status fixture. A Bazel shell test will execute that declared
binary through runfiles and assert the exact version, commit, and build date. The
fixture invokes no repository or host tools, and the test action remains
sandboxable and remote-execution compatible. A second test binary with literal
`x_defs` was rejected because it can remain green while the production release
rule is broken.

Go tests will cover subscription error identity, unexpected closure,
cancellation/Close races, compatibility `Watch`, CLI exit behavior, and terminal
escaping. The public API baseline will be regenerated for the additive method
and sentinel.

## Risks / Trade-offs

- **[Compatibility iterator still cannot report errors]** → Keep that behavior
  explicitly documented and direct assured consumers and the CLI to
  `WatchWithErrors`.
- **[A source close races cancellation or Importer.Close]** → Re-check live
  state after channel closure and classify shutdown as clean.
- **[Visible escapes widen table cells]** → Accept width growth as the secure,
  inspectable representation; dynamic terminal state is never preferable.
- **[Stable Git values cause relinks when provenance changes]** → Use only
  `STABLE_*` keys so Bazel invalidates release artifacts exactly when source
  provenance changes, not on every invocation.
- **[Git provenance fixtures require a host executable]** → Resolve one exact
  Git path, fail clearly when it is unavailable, and confine that exception to
  tests tagged `local` and `requires-git`; keep the production stamping gate
  independent of Git and host tools.
- **[Dirty development metadata is not a clean release identifier]** → Preserve
  the explicit `.dirty` suffix; stable release policy continues to require a
  clean canonical tag.

## Migration Plan

1. Land focused regression tests and the implementation in one change so no
   fail-open interval is introduced.
2. Regenerate and review the public API baseline for the additive surface.
3. Synchronize accepted requirements into main specs and traceability.
4. Run fresh focused tests, race tests, stamping smoke tests, strict OpenSpec
   validation, patch coverage, and the full local CI entry point.

Rollback is a normal commit revert. Existing `Watch` consumers remain source
compatible throughout; consumers may adopt `WatchWithErrors` incrementally.

## Open Questions

None.
