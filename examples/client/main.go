// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

// Example client demonstrates attaching a remote USB/IP device via
// pkg/usbip.Importer and detaching it cleanly after a brief hold.
//
// Usage:
//
//	go run ./examples/client HOST BUSID
//
// Produces output similar to:
//
//	attached bus-id 1-1 to port 0 at 10.0.0.5:3240
//	detached port 0
//
// The example exits non-zero on any failure. It is intentionally the
// smallest path through the public API: NewImporter, Attach, Detach,
// Close. Error handling uses the sentinels exported by pkg/usbip so
// consumers can see how errors.Is-based classification is expected to
// work.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
)

const (
	// holdDuration is how long the example keeps the device attached
	// before detaching. Short enough to be demo-friendly; long enough
	// that sysfs state is observable.
	holdDuration = 5 * time.Second

	// wantArgs is the exact argument count (HOST BUSID plus argv[0]).
	wantArgs = 3

	// exitUsage is the conventional BSD-style usage exit code.
	exitUsage = 2
)

func main() {
	if len(os.Args) != wantArgs {
		fmt.Fprintln(os.Stderr, "usage: client HOST BUSID")
		os.Exit(exitUsage)
	}

	err := run(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(hostArg, busIDArg string) error {
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	remote, err := domain.ParseRemote(hostArg)
	if err != nil {
		return fmt.Errorf("parse remote %q: %w", hostArg, err)
	}

	busID, err := domain.ParseBusID(busIDArg)
	if err != nil {
		return fmt.Errorf("parse busid %q: %w", busIDArg, err)
	}

	imp, err := usbip.NewImporter(usbip.WithImporterLogger(slog.Default()))
	if err != nil {
		if errors.Is(err, usbip.ErrKernelModuleMissing) {
			return fmt.Errorf("kernel module missing; run: sudo modprobe vhci_hcd: %w", err)
		}

		return fmt.Errorf("new importer: %w", err)
	}

	defer func() { _ = imp.Close() }()

	port, err := imp.Attach(ctx, remote, busID, usbip.AttachOptions{})
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "attached bus-id %s to port %d at %s\n",
		port.BusID, port.ID, port.Remote)

	select {
	case <-time.After(holdDuration):
	case <-ctx.Done():
	}

	err = imp.Detach(ctx, port.ID)
	if err != nil {
		return fmt.Errorf("detach: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "detached port %d\n", port.ID)

	return nil
}
