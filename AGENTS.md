# Repository Agent Instructions

These instructions apply to the entire repository. Treat them as durable project
policy, not suggestions.

## Working Style

- Use the applicable Go skills for dependency, API, architecture, concurrency,
  correctness, testing, performance, CLI, lint, and CI work.
- Be thorough and evidence-driven. Fix root causes; do not paper over failures,
  weaken checks, or claim unverified success.
- Use Context7 for current documentation whenever work concerns a library,
  framework, SDK, API, CLI tool, or cloud service. Resolve the library ID first,
  then query the full question. If Context7 is unavailable, use current official
  primary documentation.
- Prefix every shell command with `rtk`, following
  `/home/silkrad/.codex/RTK.md`.
- Work on a branch and through a pull request; do not make feature or cleanup
  changes directly on `main`.

## Go and API Quality

- Write idiomatic, simple, maintainable Go. Apply Go naming, error handling,
  context, concurrency, resource-lifecycle, safety, and testing conventions.
- Do not use magic numbers or magic strings. Give semantic values clear names,
  centralize shared constants, and prefer types that make invalid states hard to
  represent. Literal values that are intrinsically local and self-evident are
  acceptable; unexplained domain, protocol, timeout, limit, status, path, or
  configuration literals are not.
- Keep the design DRY without introducing speculative abstractions. Prefer
  narrow interfaces owned by their consumers and explicit dependency injection.
- Preserve public v1 source and behavioral compatibility unless a breaking
  change is explicitly approved. Protect the public surface with API-diff
  baselines and regression tests.
- Add focused regression tests for every bug fix. Exercise race-prone code with
  the race detector and use deterministic synchronization instead of sleeps.
- Maintain exhaustive unit, integration, conformance, mutation, coverage, and
  cross-platform/cross-architecture validation appropriate to the change.
- Treat patch coverage as a required gate as well as total and per-package
  coverage. Investigate every uncovered changed line; do not hide patch gaps
  behind a healthy repository-wide percentage.

## Specifications and Documentation

- OpenSpec is the source of truth for current behavior and architectural
  decisions. Update the relevant specs and `openspec/TRACEABILITY.md` whenever
  behavior, architecture, API, operations, security, build, or release semantics
  change.
- Do not introduce ADRs. Proposed behavior belongs in an OpenSpec change;
  accepted current behavior belongs in the main OpenSpec specs.
- Keep traceability evidence precise and repository-grounded. Keep README and
  operator/developer documentation concise, accurate, and synchronized with the
  implementation.

## Build, Dependencies, and CI

- Keep routine build, test, lint, security, packaging, and release operations
  hermetic through Bazel/Bzlmod. Do not reintroduce Nix or Task as parallel build
  systems.
- Keep Make as the stable human/CI entry point over Bazel. Keep visible Make
  targets alphabetically ordered.
- Keep dependencies and actions current and reproducibly pinned. Update locks,
  checksums, API baselines, generated metadata, and documentation together.
- GitHub workflows should call the corresponding Make/Bazel entry points rather
  than duplicate project logic. Preserve required status-check context names.
- Keep linting strict. Never disable a lint, add a blanket exclusion, lower a
  quality threshold, ignore a security result, or bypass a gate merely to make
  CI pass. Correct the code or configuration instead.
- Preserve Bazel sandboxing and hermeticity. Grant only the narrow execution
  capabilities a test genuinely requires.
- Stable releases must remain gated on tests, API compatibility, security,
  architecture, coverage, mutation quality, cross-compilation, packaging, and
  dedicated kernel integration. Publish only after provenance and required
  artifacts have succeeded.

## Git and Attribution

- Every commit must follow Conventional Commits and must be cryptographically
  signed. Use the configured Git identity and signing key. If signing cannot be
  completed or verified, stop and ask; never fall back to an unsigned commit.
- Never add `Co-authored-by` or any other co-author trailer. This prohibition is
  absolute.
- Before publishing work, verify the commits created for it are signed, have
  Conventional Commit subjects, use the intended author/committer identity, and
  contain no unintended attribution trailers.
- Audit the complete repository history for author/committer names and emails,
  signature status, Conventional Commit subjects, and attribution trailers. The
  current history contains an accidental contributor identity caused by a wrong
  email; identify the exact commits and safe remediation.
- Never rewrite published history, alter existing authorship, or force-push as a
  surprise. Present the audit findings and proposed mapping/rewrite plan first,
  then obtain explicit approval before any destructive history operation.

## Completion Standard

- Run fresh, relevant validation after the final edit; do not rely solely on an
  earlier green run.
- Report exactly what passed, what could not run, and any external prerequisite
  (for example, the dedicated USB/IP kernel runner).
- Do not declare the work complete while known correctness, API, security,
  attribution, signature, or release-integrity findings remain unresolved or
  undisclosed.
