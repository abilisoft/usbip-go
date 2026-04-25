// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

// Example server demonstrates embedding pkg/usbip.Exporter in a
// custom process: listen on TCP, bind a local device for export, then
// serve the USB/IP accept loop until SIGINT/SIGTERM.
//
// Usage:
//
//	sudo go run ./examples/server ADDR BUSID
//
// ADDR is a bind address (e.g. 0.0.0.0:3240). BUSID is a locally-
// enumerable bus id (e.g. "1-1.2"). Root (or CAP_SYS_ADMIN +
// CAP_DAC_OVERRIDE) is required because Bind writes sysfs under
// /sys/bus/usb/drivers/usb and /sys/bus/usb/drivers/usbip-host — see
// docs/security.md.
//
// This is the library-embed counterpart to cmd/usbipd-go, the production
// daemon. For systemd socket-activation and resource caps, use the
// daemon; for a one-shot or custom lifecycle process, start here.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
)

const (
	// shutdownTimeout bounds Exporter.Shutdown drain and unbind.
	shutdownTimeout = 30 * time.Second

	// wantArgs is the exact argument count (ADDR BUSID plus argv[0]).
	wantArgs = 3

	exitUsage = 2
)

func main() {
	if len(os.Args) != wantArgs {
		fmt.Fprintln(os.Stderr, "usage: server ADDR BUSID")
		os.Exit(exitUsage)
	}

	err := run(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(addr, busIDArg string) error {
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	busID, err := domain.ParseBusID(busIDArg)
	if err != nil {
		return fmt.Errorf("parse busid %q: %w", busIDArg, err)
	}

	logger := slog.Default()

	exp, err := usbip.NewExporter(usbip.WithExporterLogger(logger))
	if err != nil {
		return fmt.Errorf("new exporter: %w", err)
	}

	// Bind before Serve so the device is exportable the moment the
	// first client connects. Unbind runs on shutdown to return the
	// device to its original driver.
	err = exp.Bind(ctx, busID)
	if err != nil {
		return fmt.Errorf("bind %s: %w", busID, err)
	}

	defer func() {
		uctx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()

		uerr := exp.Unbind(uctx, busID)
		if uerr != nil {
			logger.Warn("unbind returned error",
				slog.String("busid", string(busID)),
				slog.Any("err", uerr))
		}
	}()

	lc := &net.ListenConfig{}

	listener, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	defer func() { _ = listener.Close() }()

	logger.Info("serving", slog.String("addr", listener.Addr().String()))

	serveErr := exp.Serve(ctx, listener)

	sctx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	shutdownErr := exp.Shutdown(sctx)
	if shutdownErr != nil {
		logger.Warn("shutdown returned error", slog.Any("err", shutdownErr))
	}

	if serveErr != nil {
		return fmt.Errorf("serve: %w", serveErr)
	}

	return nil
}
