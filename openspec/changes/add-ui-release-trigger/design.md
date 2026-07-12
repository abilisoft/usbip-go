## Context

The merged release workflow triggers only on a stable tag push. This preserves a
real tag context for GoReleaser and the pinned SLSA generator, but requires the
maintainer to use local Git or a CLI. GitHub's normal Releases UI is not a safe
substitute because its tag control is coupled to publishing a GitHub Release,
while this repository must build, attest, and only then publish its draft.

GitHub Actions makes `workflow_dispatch` events created with `GITHUB_TOKEN` an
explicit exception to recursive-workflow suppression. The SLSA generator uploads
assets only when the caller context is a tag, so the manual branch-context run
cannot perform release work directly.

## Goals / Non-Goals

**Goals:**

- Offer **Actions → Release → Run workflow** with a stable tag input.
- Keep direct stable tag pushes working without behavioral differences.
- Make both paths converge on the exact same tag-context validation and release
  jobs.
- Reject non-default-branch manual starts, stale default-branch commits,
  malformed tags, and existing tags before release work.
- Preserve least privilege, fail-closed behavior, draft publication ordering,
  SLSA attachment, and existing release artifact contracts.

**Non-Goals:**

- Using GitHub's **Publish release** form as the pipeline entry point.
- Automatically selecting the next SemVer value.
- Replacing local signed tags; the direct tag-push path remains supported.
- Changing runtime, protocol, CLI, Go API, or kernel-integration behavior.

## Decisions

### Use one workflow with a two-run manual handoff

`release.yml` gains a required string input named `tag`. A manual run selected
on the default branch performs only the release-start job. That job validates
the request, creates `refs/tags/<tag>` at the still-current default-branch SHA,
then dispatches `release.yml` again with that tag as the workflow ref. The second
run sees `github.ref_type == 'tag'` and executes the same jobs used by a direct
tag push.

This two-run handoff is intentional: the SLSA reusable workflow detects
`github.ref` and uploads provenance only for `refs/tags/*`. Running all jobs in
the first branch-context invocation would either lose that attachment contract
or require a divergent release implementation.

**Alternative considered:** add `workflow_dispatch` and create a tag midway
through the same run. Rejected because the immutable Actions context remains a
branch and the SLSA generator would not see a tag ref.

**Alternative considered:** use GitHub's Releases form. Rejected because it can
publish before validation, artifact creation, and provenance complete.

### Use GITHUB_TOKEN for an atomic lightweight ref and explicit redispatch

The start job receives only `actions: write` and `contents: write`. Its
repository-owned script creates the tag through the Git refs API, then invokes
`gh workflow run release.yml --ref <tag>`. A tag created by `GITHUB_TOKEN` does
not recursively trigger `push`, while the explicit `workflow_dispatch` does,
so exactly one tag-context release run is created.

The manual path creates a lightweight tag, matching GitHub's web-oriented
maintainer experience. Maintainers who require a signed annotated Git tag retain
the direct local tag-push path.

Concurrency groups include the ref type and requested tag. The short branch
launcher therefore cannot block or replace its tag-context handoff, while a
direct tag push and an explicit tag dispatch for the same tag still serialize.

### Validate before mutation and roll back a failed handoff

The start script accepts all GitHub context through environment variables,
requires exact `vMAJOR.MINOR.PATCH`, confirms the selected ref is the default
branch, fetches the current default-branch SHA through the API, and rejects a
stale dispatch before creating a ref. Ref creation is atomic and fails if the
tag already exists.

If the explicit redispatch fails after ref creation, the script deletes only
the tag it just created and returns failure. Once redispatch succeeds, later
release failures leave the tag in place, matching direct tag-push semantics and
allowing diagnosis rather than silently moving or deleting a published ref.

### Share validation and expose precise regression tests

The workflow delegates the stateful manual-start operation to a narrow shell
script. Hermetic tests provide a fake `gh`, assert every validation failure,
verify the create/dispatch sequence, and verify rollback after dispatch failure.
The tag-context release validation remains in the workflow and is exercised by
actionlint plus repository coverage checks.

## Risks / Trade-offs

- **[The start run succeeds but the tag-context run later fails]** → The tag is
  retained exactly as with a direct tag push; rerun/diagnose without mutating it.
- **[Default branch advances during manual start]** → Compare the dispatch SHA
  with the live default-branch ref immediately before tag creation and fail stale
  requests.
- **[Two release runs look confusing]** → Name the first job “Start release” and
  document that it hands off to a second tag-context run.
- **[Dispatch fails after tag creation]** → Delete only the newly created tag and
  fail loudly; tests pin this rollback.
- **[Write-capable token is exposed to branch code]** → Reject non-default branch
  dispatches before checkout and scope write permissions only to the start job.

## Migration Plan

1. Merge the workflow, script, tests, OpenSpec, and documentation changes.
2. Validate the manual path with a non-release test tag only if an explicit live
   repository test is approved; otherwise rely on mocked API regressions and the
   next stable release.
3. For the next release, either push the stable tag locally or run the Release
   workflow on `main` with the stable tag input.
4. Confirm the tag-context run builds the draft, uploads provenance, and
   publishes it.

Rollback is a normal pull request removing `workflow_dispatch` and the start
job/script; direct tag pushes remain unaffected throughout.

## Open Questions

None.
