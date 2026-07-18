## Context

The public Go module proxy resolved `v1.0.1` before its signed annotated tag
was moved. Go module versions are content-addressed and proxy/sumdb records are
immutable, so changing the Git ref produced two different module archives for
one semantic version and caused direct resolution to fail checksum
verification. A later tag can publish a Go `retract` directive, but no release
workflow or proxy preflight can repair a version that another proxy or mirror
has already cached.

Before this policy change, the explicitly approved one-time incident repair
deleted the quarantined GitHub draft and replaced the already-moved
`v1.0.1` ref with signed annotated tag object
`632c88ef5f2f084cff40eb3fc06c5b9a24d25cd9`, targeting clean commit
`7b377f09e67f70417cdbc2e76e8ed20ff0a679ee`. Its tree and Go module sums were
verified byte-for-byte against the immutable proxy record. That exceptional
transition closed the existing direct/proxy mismatch before this single-use
policy became effective; it is not a future recovery mechanism or precedent.

The Release workflow already validates canonical stable SemVer syntax and
keeps publication behind prerequisite, draft, and provenance gates. It does
not distinguish a new tag creation from an update, forced move, or deletion
event. The active tag ruleset is the primary immutability boundary; workflow
validation and repository policy provide defense in depth and a visible
failure if a prohibited event nevertheless reaches Actions.

## Goals / Non-Goals

**Goals:**

- Publish an explicit `v1.0.1` retraction from `v1.0.2` or later.
- Fail the current Release workflow before downstream work unless its event
  describes an exact fresh tag creation and the live signed annotated tag
  targets the event commit and current default-branch head.
- Make later release jobs consume the immutable event commit without persisted
  checkout credentials and revalidate the live tag immediately before staging
  and publication.
- Render release notes only for the stable tag at `HEAD` and verify their
  heading identifies that exact pushed version.
- Make the event policy executable, hermetic, and mutation-resistant.
- Establish one durable recovery rule: retain the bad version as unsupported
  and advance SemVer rather than moving or recreating its tag.

**Non-Goals:**

- Do not move, delete, recreate, or publish `v1.0.1` from this change; the
  approved one-time pre-policy repair is already complete.
- Do not add another release launcher or weaken/bypass the tag ruleset.
- Do not query a Go proxy as a pre-publication safety gate; a negative result
  cannot prove that no proxy, checksum database, or mirror cached the version.
- Do not change Go runtime behavior, the public v1 API, artifacts, provenance
  formats, dependencies, or privileged kernel-test policy.

## Decisions

### Retract the poisoned version from the next stable version

`go.mod` lists `v1.0.1` in the existing retraction block with a durable
historical reason: the tag was rewritten after proxy caching during a failed
release, so users must use `v1.0.2` or later. The next higher stable module
version makes that metadata discoverable to Go tooling without pretending that
the cached `v1.0.1` record can be overwritten.

Deleting the version from documentation alone was rejected because Go tooling
would receive no machine-readable warning. Moving the tag again as an ordinary
release operation was rejected because stable module versions are single-use
and remote caches are not mutable.

### Validate event identity through repository-owned helpers

The validation job checks out the current default branch without persisted
credentials and invokes a repository-owned live-tag helper. That helper reads
the live tag ref and annotated object through GitHub's read-only API, then
passes that evidence plus `github.event.after`, `github.event.created`,
`github.event.forced`, and `github.event.deleted` to a pure validation helper.
The pure helper requires exact event values `true`, `false`, and `false`;
canonical stable SemVer without leading zeroes; an annotated tag whose name
matches; GitHub-verified signature state; a direct commit target matching the
event; and equality with the checked-out default-branch head. Missing,
unexpected, stale, lightweight, or inconsistent metadata fails closed.

Loading the helper from the protected default branch prevents the event ref
from substituting helper contents only after GitHub has selected a workflow
revision that contains this checkout. It cannot make an obsolete workflow
revision safe: GitHub selects push workflow code from the event's associated
commit or ref before any job runs.

Validation runs as an ordinary step that exits non-zero. A job-level `if` was
rejected because a skipped validation job is less visible and can complicate
dependency semantics. Inline YAML-only logic was rejected in favor of a helper
that the hermetic Bazel test can execute against positive and negative event
fixtures. Existing job names and the downstream dependency graph remain
unchanged.

After initial validation, the release and publication jobs check out
`github.event.after`, not the mutable tag ref, and disable persisted checkout
credentials. The release job invokes the same live-tag helper immediately
before draft staging, and the publish job invokes it again immediately before
making the draft public. This prevents later jobs from building repository code
selected by a moved ref and makes a post-validation mismatch fail visibly. It
does not make an administrator bypass race atomic; the all-tag ruleset remains
the authoritative control.

### Bind release notes to the pushed tag

The Bazel-provisioned changelog runner uses git-cliff `--current`, which selects
the stable tag at `HEAD`, rather than `--latest`, which can select a different
tag. The workflow redirects only renderer stdout to the release-notes file and
checks both that the file is nonempty and that its first heading identifies the
pushed tag before live-tag revalidation and draft staging.

### Treat tag protection as primary and workflow validation as defense in depth

Maintainers never use a tag-ruleset bypass to move, delete, or recreate an
existing stable tag. The active ruleset covers all tags and may require an
authorized actor for the first creation; that authority must never be used to
reuse a stable version. Once a stable tag has been pushed, any failure consumes
that version: a normal pull request adds its retraction and the next attempt
uses a higher patch version.

The workflow helper rejects prohibited metadata only when the event selects a
workflow revision containing the current validation. It also cannot distinguish
a globally unused name from a consumed name that an administrator deleted and
recreated. The server-side all-tag ruleset is therefore the authoritative
boundary that prevents ref mutation; workflow validation is visible defense in
depth for events that reach it.

A public-proxy lookup is useful only as post-publication evidence. It was
rejected as a prevention mechanism because a negative response from one proxy
cannot prove global absence, and a positive response means reuse is already
unsafe.

### Keep regression coverage focused and hermetic

The existing release workflow policy test retains static assertions for the
trigger, prerequisite graph, draft reuse, provenance, and publication ordering.
A focused shell test invokes the helper for a valid creation and for moved,
forced, deleted, malformed, leading-zero, lightweight, unverified, mismatched,
and missing metadata. It requires no Git repository, network, home directory,
clock, or external release service. Static workflow policy coverage binds the
GitHub API evidence, default-branch checkout, helper invocation, and fail-closed
dependency graph together.

## Risks / Trade-offs

- **[A bad tag consumes a version even if no GitHub Release was published]** →
  This is intentional because a Go proxy may resolve the tag immediately; use
  the next patch version and publish a retraction.
- **[Workflow validation cannot stop an administrator from mutating the ref]** →
  GitHub may select obsolete workflow code from the moved ref. Keep the all-tag
  ruleset active without routine bypass and treat current validation as defense
  in depth, not the immutability boundary.
- **[The validation job adds a checkout before lightweight checks]** → Use the
  already pinned checkout action with shallow, credential-free settings and a
  read-only GitHub tag-object lookup; all build, artifact, and publication work
  remains downstream.
- **[The default branch advances before validation]** → Exact-head comparison
  fails closed when the validation checkout observes the advance. The tag
  remains consumed and recovery uses the next patch.
- **[The live tag changes after initial validation]** → Later jobs consume the
  immutable event commit and revalidate before staging and publication; the
  server-side all-tag ruleset remains authoritative against the unavoidable
  administrator-bypass race between a check and the protected operation.
- **[The Release workflow is still disabled during recovery]** → Verify
  workflow ID `266335789` is `active` immediately before the single-use tag
  push. Do not consume `v1.0.2` while automation is disabled.
- **[GitHub tag lookup or signature verification is unavailable]** → The job
  fails before downstream work. The consumed version is retracted and the next
  patch is used rather than retrying by moving the tag.
- **[A retracted version remains explicitly fetchable]** → Go tooling warns on
  explicit selection and excludes it from normal upgrade selection; published
  policy directs users to `v1.0.2` or later.

## Migration Plan

1. Record the completed one-time incident precondition: the quarantined
   `v1.0.1` draft is deleted, the signed retracted tag targets clean
   `7b377f09e67f70417cdbc2e76e8ed20ff0a679ee`, and direct/proxy module sums
   match. Never restore the old poisoned history.
2. Merge the retraction, validator, tests, accepted specifications, and
   documentation through the protected pull-request workflow.
3. Confirm all required checks and fresh mutation testing on the merged default
   branch; retain existing manual kernel evidence with its unchanged-runtime
   scope.
4. Re-enable Release workflow ID `266335789` and independently verify its state
   is `active` immediately before consuming a new version.
5. Create and verify one new signed annotated `v1.0.2` tag at the exact green
   default-branch head, then push it once through the existing automation.
6. Verify the public release, artifacts, provenance, and that Go tooling reads
   the `v1.0.1` retraction from `v1.0.2`.

Rollback is a normal pull request reverting workflow implementation while
preserving the `v1.0.1` retraction and immutable-version policy. A failed
`v1.0.2` tag is never moved during rollback; it is retracted by a later patch.

## Open Questions

None.
