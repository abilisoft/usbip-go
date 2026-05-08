// Package usbip is the public facade for the usbip-go library. It re-exports
// the stable surface (constructors, configuration options, sentinel errors)
// and wraps the internal application services so that consumers never reach
// into the `internal/` tree.
package usbip
