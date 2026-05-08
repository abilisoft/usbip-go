// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

// Example metrics demonstrates wiring an Exporter's Prometheus
// metrics (spec §11.5.5) into a custom HTTP server. The process binds
// a USB/IP listener, registers the metric catalog via
// WithExporterMetricsRegisterer, and serves /metrics on a separate
// address so scrapers do not share the protocol listener.
//
// This example is the only one that pulls a third-party dependency
// (github.com/prometheus/client_golang) because the Exporter metrics
// registerer is a prometheus.Registerer. The other examples
// (client, server, events, reconnect) use stdlib only.
//
// Usage:
//
//	sudo go run ./examples/metrics USBIP_ADDR METRICS_ADDR BUSID
//
// Example:
//
//	sudo go run ./examples/metrics :3240 127.0.0.1:9240 1-1.2
//
// Scrape with:
//
//	curl -s http://127.0.0.1:9240/metrics | grep usbip_
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	// shutdownTimeout bounds Exporter.Shutdown and http.Server.Shutdown.
	shutdownTimeout = 30 * time.Second

	// readHeaderTimeout is a slowloris defense on the metrics server.
	readHeaderTimeout = 5 * time.Second

	// exampleVersion labels usbip_build_info. Real deployments inject
	// the tag via -ldflags "-X main.version=v1.2.3"; hard-coding here
	// keeps the example self-contained.
	exampleVersion = "example"

	wantArgs  = 4
	exitUsage = 2
)

func main() {
	if len(os.Args) != wantArgs {
		fmt.Fprintln(os.Stderr, "usage: metrics USBIP_ADDR METRICS_ADDR BUSID")
		os.Exit(exitUsage)
	}

	err := run(os.Args[1], os.Args[2], os.Args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(usbipAddr, metricsAddr, busIDArg string) error {
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	busID, err := domain.ParseBusID(busIDArg)
	if err != nil {
		return fmt.Errorf("parse busid %q: %w", busIDArg, err)
	}

	reg := prometheus.NewRegistry()
	logger := slog.Default()

	exp, err := usbip.NewExporter(
		usbip.WithExporterLogger(logger),
		usbip.WithExporterMetricsRegisterer(reg),
		usbip.WithExporterBuildInfo(exampleVersion, "none", runtime.Version()),
	)
	if err != nil {
		return fmt.Errorf("new exporter: %w", err)
	}

	err = exp.Bind(ctx, busID)
	if err != nil {
		return fmt.Errorf("bind %s: %w", busID, err)
	}

	lc := &net.ListenConfig{}

	listener, err := lc.Listen(ctx, "tcp", usbipAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", usbipAddr, err)
	}

	defer func() { _ = listener.Close() }()

	metricsSrv := &http.Server{
		Addr:              metricsAddr,
		Handler:           promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	go func() {
		merr := metricsSrv.ListenAndServe()
		if merr != nil && !errors.Is(merr, http.ErrServerClosed) {
			logger.Error("metrics server failed", slog.Any("err", merr))
		}
	}()

	logger.Info("serving",
		slog.String("usbip", listener.Addr().String()),
		slog.String("metrics", metricsAddr))

	serveErr := exp.Serve(ctx, listener)

	sctx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	_ = metricsSrv.Shutdown(sctx)
	_ = exp.Shutdown(sctx)

	if serveErr != nil {
		return fmt.Errorf("serve: %w", serveErr)
	}

	return nil
}
