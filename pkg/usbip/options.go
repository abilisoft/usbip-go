package usbip

import internalapp "github.com/abilisoft/usbip-go/internal/app"

// importerConfig carries the option-tunable fields for NewImporter.
// The struct is unexported so the option shape can evolve without a
// breaking change; callers only manipulate the With* functions declared
// alongside. Fields are added as the matching With* is introduced.
type importerConfig struct{}

// ImporterOption configures an Importer at construction time. Apply
// options by passing them to NewImporter; options mutate an internal
// config struct in declaration order so the last option wins for any
// field.
type ImporterOption func(*importerConfig)

// importerConfigToInternal translates the public-facing importerConfig
// into the matching slice of internalapp.ImporterOption values. The
// current config has no public-tunable fields; the function is wired
// now so later With* additions have one place to grow.
func importerConfigToInternal(_ importerConfig) []internalapp.ImporterOption {
	return nil
}

// exporterConfig carries the option-tunable fields for NewExporter.
// The split from importerConfig prevents a single Option type from
// accepting importer-only or exporter-only tunables at the wrong
// constructor (spec §5.7: role-specific options).
type exporterConfig struct{}

// ExporterOption configures an Exporter at construction time. Apply
// options by passing them to NewExporter; options mutate an internal
// config struct in declaration order so the last option wins for any
// field.
type ExporterOption func(*exporterConfig)

// exporterConfigToInternal translates the public-facing exporterConfig
// into the matching slice of internalapp.ExporterOption values. See
// importerConfigToInternal for the rationale.
func exporterConfigToInternal(_ exporterConfig) []internalapp.ExporterOption {
	return nil
}
