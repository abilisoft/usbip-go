## Context

The release pipeline stages GoReleaser output as a draft, delegates provenance
to SLSA's reusable generic generator, and publishes only after that job. The
first `v1.0.1` execution exposed three interacting defects: SLSA rejects a raw
commit ref as an unverifiable builder identity; its uploader defaults to a
public release unless `draft-release` is set; and redirected Make stdout mixed
bootstrap/recipe diagnostics with git-cliff data. A separate UI launcher used
the repository token to create a lightweight ref, which the active tag ruleset
correctly rejected because it was not a signed annotated tag.

The v1 Go/library surface and release artifact matrix are unaffected. The
change is confined to release initiation, orchestration, documentation, and
policy tests. It explicitly supersedes the release initiation contract from
the completed `add-ui-release-trigger` change, whose UI path proved incompatible
with the live signed-tag ruleset.

## Goals / Non-Goals

**Goals:**

- Preserve one unpublished draft until valid provenance exists.
- Use the verifier-compatible SLSA builder identity without changing the
  generator code resolved by v2.1.0.
- Make release-note emptiness and contents reflect git-cliff alone.
- Keep the tag ruleset strong and document one release entry point that works
  with it.
- Encode the full contract in hermetic, mutation-resistant policy tests.

**Non-Goals:**

- Do not weaken or bypass repository/tag rulesets.
- Do not manually synthesize or upload provenance.
- Do not change binaries, packages, checksums, SBOM formats, or public APIs.
- Do not add signing secrets or a long-lived release credential to Actions.
- Do not make privileged kernel/KVM tests run on hosted runners.

## Decisions

1. **Reference SLSA at literal `@v2.1.0`.** This is the upstream-required
   exception to normal action SHA pinning: the verifier trusts a semantic-tag
   reusable-workflow identity. The tag resolves to the same commit previously
   pinned, so executable generator code does not change.
2. **Pass `draft-release: 'true'` and retain the final repository-owned publish
   job.** SLSA reuses the GoReleaser draft and uploads provenance without making
   it public. The final job has an ordinary successful `needs: [provenance]`
   dependency and no `always()`/continue-on-error escape hatch.
3. **Reserve Make target stdout for data.** Bootstrap diagnostics are redirected
   to stderr and the changelog recipe is quiet. Filtering generated notes was
   rejected because it could hide new contamination and let false evidence pass.
4. **Remove the GitHub UI launcher.** Keeping a nonfunctional path, weakening
   tag rules, or storing a signing key in Actions were rejected. A maintainer
   creates and verifies a signed annotated tag on current `main`; its push starts
   the same automated gated workflow.
5. **Use a repository-owned static policy test.** Actionlint validates YAML
   shape but not cross-tool release semantics. The test pins trigger shape,
   generator identity/inputs/permissions, draft configuration, dependency
   fail-closed behavior, and changelog stdout mechanics without network access.

## Risks / Trade-offs

- **[A release now requires local signing access]** → This is already required by
  the active tag ruleset; contributor instructions include tag verification.
- **[A future SLSA update needs coordinated edits]** → The exact version is
  intentionally repeated in workflow, policy test, spec, and security docs so
  drift fails review/CI rather than silently changing trust identity.
- **[Removing the UI form is operationally breaking]** → The form was unusable
  under the live ruleset; the supported signed-tag path remains automated.
- **[A draft from a failed run contains stale assets]** → GoReleaser's
  `use_existing_draft` and `replace_existing_draft` settings rebuild that single
  draft; publication still waits for fresh provenance.

## Migration Plan

1. Merge the workflow, tests, accepted specs, and documentation through normal
   protected-branch review.
2. Verify all main checks and mutation testing on the merged commit.
3. With explicit approval, replace the unpublished `v1.0.1` signed tag so it
   points to fixed `main`; retain the existing unpublished draft for replacement.
4. Let the tag push rebuild artifacts, attach provenance, and publish only after
   every release job succeeds.
5. Verify tag signature/target, release state, asset hashes, checksum signature,
   provenance, package contents, and stamped binary metadata.

Rollback is a normal PR reverting the orchestration change while leaving any
failed draft unpublished. It does not restore the incompatible UI launcher or
weaken tag rules.

## Open Questions

None.
