## 1. Hosted Kernel Gate

- [x] 1.1 Add fail-closed VM and guest-preparation scripts for a checksum-pinned full kernel, required modules, configfs, and UDC surfaces
- [x] 1.2 Add hermetic shell regression tests for successful provisioning and every critical failure path
- [ ] 1.3 Run the dedicated kernel workflow on pinned free GitHub-hosted Ubuntu and execute the Bazel integration target as root

## 2. Release Documentation

- [x] 2.1 Replace temporary all-tags-retracted wording with version-agnostic supported-release policy and installation guidance
- [x] 2.2 Add a versionless pkg.go.dev badge/link and document the hosted-runner provisioning exception accurately
- [x] 2.3 Update main OpenSpec requirements and `openspec/TRACEABILITY.md` with precise implementation evidence

## 3. Validation and Publication

- [x] 3.1 Run focused provisioning-script tests, workflow lint, Markdown/YAML lint, and strict OpenSpec validation
- [x] 3.2 Run fresh repository lint, unit, conformance, coverage, API, security, release-check, and release-snapshot gates
- [ ] 3.3 Dispatch and pass live kernel integration on the GitHub-hosted runner without skipped prerequisites
- [ ] 3.4 Create a signed Conventional Commit, audit attribution/signature metadata, push the branch, and open a pull request
