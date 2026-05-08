//go:build linux

// Package kernel implements the Linux kernel adapter: sysfs reads/writes,
// netlink uevent listener, and the fd-passing handshake for usbip-host and
// vhci_hcd.
//
// Three role-specific structs share a common substrate:
//
//   - ImporterAdapter satisfies app.ImporterKernel (vhci_hcd + usbip_core).
//   - ExporterAdapter satisfies app.ExporterKernel (usbip_host + usbip_core).
//   - EventsAdapter satisfies app.KernelEvents (netlink uevent fan-out).
//
// Every file in this package carries //go:build linux. Non-Linux builds
// skip the package entirely; the app layer depends only on the interfaces
// in internal/app, not on the concrete types here.
package kernel
