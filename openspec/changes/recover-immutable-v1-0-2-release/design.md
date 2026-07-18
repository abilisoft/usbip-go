## Context

GitHub stores an annotated tag ref as a tag-object SHA, while Actions exposes
the peeled commit as `github.sha`. For the signed `v1.0.2` push, those values are
respectively `f0c7083fdee40e1e31ebc170992fa5f43efe8d60` and
`72aa5a6b585d1f5b6230c8362254ea2a6296ec75`. The current validator incorrectly
requires the latter to equal the former, so the first job failed and no draft,
artifact, or public release was created.

The tag is protected, already externally observable, and must not move. A rerun
uses the frozen workflow revision and cannot consume all of a later fix. A new
tag-push event would require forbidden tag mutation. The recovery therefore
needs current protected control code while building and testing only the exact
immutable source revision.

## Goals / Non-Goals

**Goals:**

- Correct normal annotated-tag validation by comparing object identity and
  peeled target identity separately.
- Keep normal release and publication checkouts pinned to the peeled event
  commit.
- Publish the existing `v1.0.2` source exactly once through repository-owned,
  reviewable, fail-closed automation.
- Run the normal hosted release gates and `make ci-local` against the immutable
  target commit before staging artifacts.
- Retain draft-first publication, keyless checksum signing, SLSA artifact
  provenance, and final live-tag revalidation.
- Make the recovery provenance identity and its verification procedure explicit.

**Non-Goals:**

- Moving, deleting, or recreating `v1.0.2` or any other stable tag.
- Adding a general manual release interface or accepting a user-selected tag,
  object, source commit, artifact set, or release identifier.
- Claiming a workflow-dispatch recovery is a new tag-push provenance event.
- Changing public Go APIs, runtime behavior, artifact formats, or USB/IP
  protocol/security behavior.

## Decisions

### Separate annotated tag-object and peeled commit identities

The live ref object's SHA is compared with `github.event.after`. The annotated
object's direct commit target is compared independently with `github.sha` and
the checked-out commit. Release and publish jobs check out `github.sha`, never
the tag-object SHA. This follows the exact values observed in the failed run and
detects both a changed ref object and a changed target.

An alternative was to peel `github.event.after` inside the validator. That
would discard the event's tag-object identity and could not prove that the live
ref is still the exact object that triggered the workflow.

### Use one fixed-input recovery workflow on protected `main`

The recovery workflow exposes one required choice input whose only supported
value is `v1.0.2`. Its validation job rejects any other API-supplied value, owns
the annotated object SHA and target commit as immutable repository constants,
and exports all three identities only after live GitHub API validation. The
fixed confirmation is recorded in workflow-dispatch provenance without making
the workflow a general release selector. Validation rejects dispatch from any
ref other than protected `main`, an existing public release, a
changed/missing/lightweight/unverified tag, or any mismatched target. Once
`v1.0.2` is public, the same preflight makes subsequent dispatches inert.

A general recovery launcher was rejected because mutable release-selection
inputs would create a second routine release API and weaken the signed fresh-tag
boundary. Publishing
manually was rejected because it would bypass reviewable gates, draft staging,
and provenance generation.

### Make reusable hosted gates accept an explicit immutable source ref

Each reusable security, unit, conformance, architecture, and coverage workflow
accepts an optional `source-ref` input and passes it only to
`actions/checkout`. Existing callers retain their event revision when the input
is omitted. The recovery caller passes the validated target commit, so every
gate runs on the source being released rather than the newer controller commit.
Normal tag releases also pass `github.sha` explicitly.

Duplicating all gate jobs in the recovery workflow was rejected because it
would create parallel CI logic and drift from the normal Make/Bazel contract.

### Separate controller and source checkouts during staging

Recovery staging checks out the exact target commit at the workspace root and
protected control code at the dispatch revision under
`.local/release-control`. Controller-owned validators inspect the live tag and
the root source checkout. All build, test, changelog, and GoReleaser commands run
from the source root, preserving existing Make, Bazel, cache, and GoReleaser
workspace assumptions. No checkout persists credentials. GoReleaser receives a
narrowly scoped `GITHUB_TOKEN` only during draft staging.

This separation avoids executing the known-broken validator frozen in the tag
while ensuring artifacts still come exclusively from the tagged source.

### Preserve provenance while representing recovery honestly

The pinned SLSA generic generator receives hashes of the exact nine nonempty
archives and OS packages and uploads to the existing `v1.0.2` draft using
`upload-tag-name`. Staging captures that draft's release ID. Immediately before
publication, controller code revalidates the same ID, the exact 15-asset roster,
every GitHub SHA-256 asset digest, equality between all 14 remote GoReleaser
assets and their locally staged hashes, and equality between all nine remote
archive/package digests and the subject hashes supplied to the generator.
Because the event is `workflow_dispatch`, its signed provenance identifies the
recovery workflow on protected `main`, not `release.yml@refs/tags/v1.0.2`.
Verification therefore checks the trusted builder, repository and protected
branch workflow identity, the fixed `release-tag=v1.0.2` confirmation recorded
by the workflow, artifact hashes, and binary version/commit stamping. Documentation
must not instruct users to misrepresent this attestation as tag-push provenance.

Using a custom provenance builder or mutating the tag to recreate tag-push OIDC
identity was rejected. The former adds unnecessary trusted code; the latter
breaks the immutability boundary the recovery exists to preserve.

## Risks / Trade-offs

- **Recovery provenance has a different caller identity from normal releases.**
  → Document the exact identity and verification command; keep the SLSA builder
  pinned and bind every subject hash to artifacts built from the fixed commit.
- **A nested checkout could accidentally run controller commands from the
  immutable source.** → Place controller code under ignored `.local`, invoke its
  validators by explicit path, and add structural workflow regression tests for
  checkout refs and command order.
- **Reusable gates could silently test the controller revision.** → Make
  `source-ref` an explicit workflow-call input and assert every checkout consumes
  it; recovery jobs depend on validated outputs.
- **A repeated dispatch or concurrent writer could replace the draft or its
  assets.** → Preflight and final validation reject any existing public
  `v1.0.2` release; staging binds one release ID; the final step revalidates that
  same ID and every remote subject digest immediately before patching only that
  ID public.
- **The one-version workflow becomes dead code after success.** → Keep it as the
  durable audit record; its public-release preflight guarantees no further side
  effect.

## Migration Plan

1. Merge the validator fix, explicit reusable source-ref support, recovery
   workflow, tests, specs, traceability, and documentation through normal review.
2. Wait for all required pull-request and final-main checks to succeed.
3. Dispatch the recovery workflow from protected `main` once.
4. Verify its gates, draft assets, SLSA provenance, checksum signature, published
   release metadata, and exact version/commit stamping before treating it as
   complete.
5. Confirm the Go module proxy and pkg.go.dev observe `v1.0.2` only after the
   public release succeeds.

Rollback before publication is to leave or delete only the unpublished draft;
the tag never changes. After publication, releases and tags remain immutable;
any source defect follows the normal retraction and higher-patch process.

## Open Questions

None.
