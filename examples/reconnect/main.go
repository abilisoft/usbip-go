// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

// Example reconnect demonstrates AttachOptions.AutoReconnect with a
// custom backoff and an OnReconnect callback.
//
// Usage:
//
//	go run ./examples/reconnect HOST BUSID
//
// When the exporter restarts or the link flaps, the importer re-
// establishes the attach using an exponential backoff that caps at
// 30 seconds. Every retry is logged via the OnReconnect hook so
// operators can correlate with their own monitoring.
//
// The program runs until SIGINT/SIGTERM. Detach on shutdown is the
// consumer's responsibility — this example chooses to Close() the
// Importer, which cancels every active port and drains the watcher.
package main

import (
	"context"
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
	// backoffInitial is the delay before the first reconnect attempt.
	backoffInitial = 1 * time.Second

	// backoffCeiling bounds every delay; larger computed values are
	// clamped.
	backoffCeiling = 30 * time.Second

	// backoffJitter shifts the deterministic geometric value by up to
	// 20% so a fleet of importers does not re-dial in lockstep after
	// a shared outage.
	backoffJitter = 0.2

	wantArgs  = 3
	exitUsage = 2
)

func main() {
	if len(os.Args) != wantArgs {
		fmt.Fprintln(os.Stderr, "usage: reconnect HOST BUSID")
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

	logger := slog.Default()

	backoff := usbip.NewExponentialBackoff(usbip.ExponentialBackoffConfig{
		Min:    backoffInitial,
		Max:    backoffCeiling,
		Jitter: backoffJitter,
	})

	imp, err := usbip.NewImporter(
		usbip.WithImporterLogger(logger),
		usbip.WithImporterBackoff(backoff),
	)
	if err != nil {
		return fmt.Errorf("new importer: %w", err)
	}

	defer func() { _ = imp.Close() }()

	opts := usbip.AttachOptions{
		AutoReconnect: true,
		OnReconnect: func(attempt int, cause error) {
			logger.Info("reconnect attempt",
				slog.Int("attempt", attempt),
				slog.Any("cause", cause))
		},
	}

	port, err := imp.Attach(ctx, remote, busID, opts)
	if err != nil {
		return fmt.Errorf("initial attach: %w", err)
	}

	logger.Info("attached (auto-reconnect enabled)",
		slog.Any("port", port.ID),
		slog.String("busid", string(port.BusID)))

	<-ctx.Done()

	return nil
}
