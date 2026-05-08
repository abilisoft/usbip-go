package main

import (
	"context"
	"errors"
	"log/slog"
)

// errRunNotImplemented is a placeholder returned by runDaemon until
// Task 8.4 lands. The test suite uses the version/drain subcommands
// exclusively until then; production startup is gated behind that task.
var errRunNotImplemented = errors.New("usbipd: run not implemented")

// runDaemon is the root-command default action. Task 8.4 wires the
// listener + Exporter + status server + signal plumbing into the real
// implementation; Task 8.1's skeleton logs the sentinel via the
// logger installed by PersistentPreRunE and returns the error so the
// exit-code classifier maps it to exit 1.
func runDaemon(ctx context.Context, _ *Config) error {
	log := loggerFromCtx(ctx)
	if log == nil {
		log = slog.Default()
	}

	log.Error("runDaemon invoked before 8.4 wiring landed",
		slog.String("err", errRunNotImplemented.Error()))

	return errRunNotImplemented
}
