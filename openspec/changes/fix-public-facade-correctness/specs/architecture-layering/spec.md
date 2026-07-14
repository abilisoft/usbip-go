## MODIFIED Requirements

### Requirement: Non-Linux builds compile with explicit unsupported behavior

The public facade SHALL remain buildable on non-Linux systems while otherwise-valid
importer/exporter construction returns kernel-module-missing behavior.

#### Scenario: Non-Linux caller constructs with otherwise-valid options
- **WHEN** a non-Linux build calls `usbip.NewImporter` or `usbip.NewExporter` with no options or otherwise-valid options
- **THEN** construction returns `ErrKernelModuleMissing` instead of failing compilation

#### Scenario: Non-Linux caller supplies invalid construction configuration
- **WHEN** a non-Linux caller supplies negative TransportOptions, an invalid Exporter ACL CIDR, or a non-finite Exporter accept rate
- **THEN** construction returns `ErrTransportOptionsInvalid`, `ErrACLInvalid`, or `ErrAcceptRateLimitInvalid`, respectively
- **AND** public configuration validation precedes unsupported-platform and kernel-module availability errors
