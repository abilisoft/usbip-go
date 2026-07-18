## MODIFIED Requirements

### Requirement: Releases are reproducible and signed

Release workflows SHALL build pure-Go artifacts through GoReleaser, publish
checksums, generate SBOM/provenance, and support keyless cosign verification.
Normal tag-push releases SHALL support verification against their stable source
tag. The fixed `v1.0.2` recovery SHALL support verification against the pinned
SLSA builder and protected-default-branch recovery workflow identity, while
independent artifact inspection SHALL confirm version `v1.0.2` and source commit
`72aa5a6b585d1f5b6230c8362254ea2a6296ec75`. Documentation SHALL distinguish these event identities rather
than claiming recovery provenance originated from a new tag push.

#### Scenario: User verifies a normal release

- **WHEN** a user downloads an archive from a normal tag-push release
- **THEN** they can verify SLSA provenance against the source tag, the cosign bundle for checksums, and per-artifact sha256

#### Scenario: User verifies the recovered v1.0.2 release

- **WHEN** a user downloads an archive published by the fixed `v1.0.2` recovery
- **THEN** they can verify SLSA provenance against the pinned builder and protected recovery workflow identity
- **AND** they can verify the cosign bundle, per-artifact sha256, and exact `v1.0.2` source-commit stamping
