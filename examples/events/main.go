// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

// Example events demonstrates iterating over Importer.Watch and
// Exporter.WatchSessions. Both return iter.Seq[Event], Go 1.23+'s
// range-over-function shape, so consumers can drive them with a
// plain `for ev := range ...` loop.
//
// Usage:
//
//	go run ./examples/events
//
// The program prints one JSON record per event for 30 seconds, then
// exits. On a machine with no attached ports and no accepted
// sessions, it simply blocks until the deadline — the point is to
// show the iterator shape, not to generate traffic.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

const (
	// watchDuration caps how long the example observes events. Short
	// enough to be a demo; long enough to catch at least one event on
	// a busy host.
	watchDuration = 30 * time.Second

	// watcherCount is the number of goroutines started by the example:
	// one for Importer.Watch, one for Exporter.WatchSessions.
	watcherCount = 2
)

func main() {
	err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ctx, cancel := context.WithTimeout(ctx, watchDuration)
	defer cancel()

	imp, err := usbip.NewImporter(usbip.WithImporterLogger(slog.Default()))
	if err != nil {
		return fmt.Errorf("new importer: %w", err)
	}

	defer func() { _ = imp.Close() }()

	exp, err := usbip.NewExporter(usbip.WithExporterLogger(slog.Default()))
	if err != nil {
		return fmt.Errorf("new exporter: %w", err)
	}

	// iter.Seq ranges drain when yield returns false or ctx dies;
	// both goroutines exit cleanly under the parent deadline without
	// explicit cancellation plumbing.
	var wg sync.WaitGroup

	wg.Add(watcherCount)

	enc := json.NewEncoder(os.Stdout)

	var encMu sync.Mutex

	go func() {
		defer wg.Done()

		for ev := range imp.Watch(ctx) {
			emit(&encMu, enc, "importer", ev)
		}
	}()

	go func() {
		defer wg.Done()

		for ev := range exp.WatchSessions(ctx) {
			emit(&encMu, enc, "exporter", ev)
		}
	}()

	wg.Wait()

	return nil
}

// emit serialises the event as one JSON line with a source tag and
// the event's kind discriminator per json-contracts OpenSpec. Encode errors on stdout
// are logged and skipped: missing one event record is preferable to
// crashing the watch loop on a transient write failure.
func emit(mu *sync.Mutex, enc *json.Encoder, source string, ev usbip.Event) {
	mu.Lock()
	defer mu.Unlock()

	err := enc.Encode(map[string]any{
		"source":  source,
		"kind":    ev.EventKind().String(),
		"payload": ev,
	})
	if err != nil {
		slog.Default().Warn("event encode failed",
			slog.String("source", source),
			slog.Any("err", err))
	}
}
