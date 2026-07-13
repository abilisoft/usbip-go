# Change-local trace evidence

This evidence is scoped to `fix-public-facade-correctness`. The final combined
`openspec/TRACEABILITY.md` is intentionally left for the integration owner.

| Contract | Implementation | Regression evidence |
| --- | --- | --- |
| Importer terminal publication barrier and drain | `internal/app/importer_watch.go`; `internal/app/export_test.go` | `internal/app/terminal_event_drain_test.go::TestImporterWatchCloseBarrierDrainsAcceptedEventExactlyOnce`; `TestImporterWatchSlowSubscriberDropsOverflow`; `TestImporterTerminalDrainStopsWhenConsumerStops` |
| Exporter terminal publication barrier and drain | `internal/app/exporter_watch.go`; `internal/app/export_test.go` | `internal/app/terminal_event_drain_test.go::TestExporterWatchCloseBarrierDrainsAcceptedEventExactlyOnce`; `TestExporterTerminalDrainStopsWhenConsumerStops` |
| Reservation-first Serve and shutdown-cancellable listener setup | `internal/app/exporter.go`; `pkg/usbip/usbip.go` | `internal/app/exporter_serve_test.go::TestExporterServeWithListenerFactory_ShutdownCancelsSetup`; `TestExporterServeWithListenerFactoryRejectsNilListener`; `TestExporterServeWithListenerFactoryClosesListenerReturnedAfterShutdown`; `pkg/usbip/listen_and_serve_test.go::TestExporterListenAndServeRejectsShutdownBeforeBind`; `TestExporterListenAndServeRejectsOverlapBeforeBind` |
| Accept-rate omission, explicit disable, and non-finite rejection | `internal/app/limits.go`; `internal/app/options.go`; `internal/app/exporter.go`; `pkg/usbip/options.go`; `pkg/usbip/defaults_config.go`; `pkg/usbip/errors.go`; `pkg/usbip/usbip.go` | `internal/app/accept_rate_validation_test.go::TestResolveAcceptRateDistinguishesOmittedAndExplicitZero`; `TestValidateAcceptRateLimitRejectsNonFinite`; `TestNewExporterWithErrorRejectsNonFiniteAcceptRate`; `pkg/usbip/defaults_validation_test.go::TestNewExporterRejectsNonFiniteAcceptRate`; `pkg/usbip/defaults_test.go::TestNewExporterAcceptsExplicitDisabledRate`; `pkg/usbip/options_test.go::TestWithExporterAcceptRateLimitTracksExplicitZero`; `TestWithExporterAcceptRateLimitLastOptionWins`; `pkg/usbip/errors_boundary_test.go` |
| Closed Attach lifecycle precedence | `internal/app/importer.go`; `pkg/usbip/usbip.go` | `internal/app/importer_test.go::TestImporterAttachClosedStatePrecedesValidation`; `TestImporterAttachValidationPrecedesBackoffFactory`; `pkg/usbip/errors_boundary_test.go::TestImporterAttachAfterCloseYieldsPublicSentinelBeforeValidation` |
| Shape-stable cancellation-aware module probing | `pkg/usbip/modules.go`; `pkg/usbip/modules_linux.go`; `pkg/usbip/modules_other.go` | `pkg/usbip/modules_test.go::TestProbeKernelModulesCancellationReturnsFullShape`; `pkg/usbip/modules_linux_cancellation_test.go::TestProbeKernelModulesMidProbeCancellationKeepsFullShape`; `pkg/usbip/modules_other_test.go::TestProbeKernelModulesNonLinuxReturnsFullUnknownShape` |
| Unsupported-platform constructor validation precedence | `pkg/usbip/defaults_config.go`; `pkg/usbip/defaults_linux.go`; `pkg/usbip/defaults_other.go`; `internal/app/cidr.go` | `pkg/usbip/defaults_validation_test.go`; `pkg/usbip/defaults_other_constructor_test.go` |
| Per-logical-Attachment factory and mutex-safe legacy custom backoff | `pkg/usbip/backoff.go`; `pkg/usbip/options.go`; `pkg/usbip/usbip.go`; `internal/app/options.go`; `internal/app/importer.go` | `pkg/usbip/backoff_test.go::TestLegacyCustomBackoffAdapterSerializesNextAndReset`; `pkg/usbip/importer_test.go::TestImporterBackoffFactoryCreatesOneIsolatedStrategyPerTopLevelAttach`; `pkg/usbip/options_test.go::TestAttachBackoffOverridesConfiguredFactoryWithoutInvokingIt`; `TestConfiguredBackoffFactoryTranslationIsLazy`; `TestWithImporterBackoffFactoryNilRestoresDefaults`; `TestConfiguredLegacyBackoffTranslatesWithoutFactory`; `internal/app/reconnect_backoff_reset_test.go::TestReconnect_ResetsBackoffAfterSuccess` |

Accepted requirements are synchronized in:

- `openspec/specs/public-library-api/spec.md`
- `openspec/specs/architecture-layering/spec.md`
- `openspec/specs/importer-lifecycle/spec.md`
- `openspec/specs/exporter-daemon/spec.md`
- `openspec/specs/operations-observability/spec.md`
- `openspec/specs/transport-networking/spec.md`

The Bazel `//:api_compatibility` target first reported only the compatible
additions `BackoffFactory`, `WithImporterBackoffFactory`, and
`ErrAcceptRateLimitInvalid`. The baseline was then regenerated with Bazel's
pinned `@org_golang_x_exp//cmd/apidiff` executable, and the same target passed
against the regenerated baseline.
