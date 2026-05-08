// Package kernel implements the Linux kernel adapter: sysfs reads/writes,
// netlink uevent listener, and the fd-passing handshake for usbip-host and
// vhci_hcd. Every .go file in this package carries `//go:build linux`.
package kernel
