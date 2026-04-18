// Package app holds the USB/IP use-case services (importer, exporter, session
// lifecycle, reconnect loop) and the adapter interfaces they depend on. It is
// transport-free and kernel-free: concrete infrastructure lives in
// `internal/adapter/*` and is injected at construction time.
package app
