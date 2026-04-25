// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"io/fs"
	"log/slog"
	"os"

	"github.com/abilisoft/usbip-go/internal/app"
)

// WriteFunc is the injected sysfs write primitive. path is the absolute
// path (e.g. "/sys/bus/usb/drivers/usbip-host/bind"); data is the byte
// payload written verbatim with no trailing newline unless a caller
// includes one.
//
// Tests inject a recorder that captures the call for assertions.
// Production uses writeFile() from sysfs.go which opens with O_WRONLY
// and writes synchronously.
type WriteFunc func(path, data string) error

// NetlinkSocket is the minimal surface the uevent listener needs from a
// NETLINK_KOBJECT_UEVENT socket. Tests inject a fake producing a stream
// of pre-canned uevent payloads.
type NetlinkSocket interface {
	// Receive returns a single uevent payload (NUL-separated KEY=VALUE
	// buffer). On close it returns a wrapped net.ErrClosed-like error;
	// callers exit their read loop on error.
	Receive() ([]byte, error)
	// Close terminates the socket. Safe to call from any goroutine.
	Close() error
}

// NetlinkDialer opens a NETLINK_KOBJECT_UEVENT socket. The zero-arg
// signature keeps the contract trivial for test injection; production
// uses openNetlinkSocket() from uevent.go.
type NetlinkDialer func() (NetlinkSocket, error)

// Option configures a role adapter. All three role constructors accept
// the same option type because the underlying state substrate is
// identical.
type Option func(*commonAdapter)

// WithFS injects an fs.FS rooted at "/" (the whole filesystem, not just
// /sys). Tests pass a testing/fstest.MapFS populated with the minimal
// set of files each case needs.
func WithFS(f fs.FS) Option {
	return func(c *commonAdapter) {
		if f == nil {
			return
		}

		c.fs = f
	}
}

// WithWriteFunc injects a sysfs write primitive. Tests use a recorder;
// production defaults to a wrapper around os.OpenFile.
func WithWriteFunc(w WriteFunc) Option {
	return func(c *commonAdapter) {
		if w == nil {
			return
		}

		c.write = w
	}
}

// WithNetlinkDialer injects a netlink-socket factory used by
// EventsAdapter.Subscribe. The default dialer opens a real
// NETLINK_KOBJECT_UEVENT socket.
func WithNetlinkDialer(d NetlinkDialer) Option {
	return func(c *commonAdapter) {
		if d == nil {
			return
		}

		c.nlDial = d
	}
}

// WithLogger injects the structured logger used for slog.Warn signals
// and debug instrumentation. The zero-value commonAdapter carries a
// no-op logger so callers may omit this option.
func WithLogger(l *slog.Logger) Option {
	return func(c *commonAdapter) {
		if l == nil {
			c.logger = noopLogger()

			return
		}

		c.logger = l
	}
}

// WithClock injects a clock for timeouts and backoff. The default is
// app.RealClock.
func WithClock(k app.Clock) Option {
	return func(c *commonAdapter) {
		if k == nil {
			return
		}

		c.clock = k
	}
}

// osDirFS is a named wrapper used for the default filesystem so tests
// can identify it by reference if needed.
func osDirFS() fs.FS { return os.DirFS("/") }

// noopLogger returns a *slog.Logger that discards all records.
func noopLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// defaultWriteFunc is the production sysfs write primitive. It opens
// path with O_WRONLY and writes data verbatim. The function-valued
// return keeps the default swappable at test-construction time without
// a package-level mutable var.
func defaultWriteFunc() WriteFunc {
	return writeSysfsFile
}

// defaultNetlinkDialer is the production netlink-socket factory. The
// real implementation lives in uevent.go; the closure
// here adapts the concrete-typed openRealNetlinkSocket into the
// interface-valued NetlinkDialer contract.
func defaultNetlinkDialer() NetlinkDialer {
	return func() (NetlinkSocket, error) {
		s, err := openRealNetlinkSocket()
		if err != nil {
			return nil, err
		}

		return s, nil
	}
}
