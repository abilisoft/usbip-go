package main

import (
	"context"
	"errors"
)

// errRunNotImplemented is a placeholder returned by runDaemon until
// Task 8.4 lands. The test suite uses the version/drain subcommands
// exclusively until then; production startup is gated behind that task.
var errRunNotImplemented = errors.New("usbipd: run not implemented")

// runDaemon is the root-command default action. Task 8.4 wires the
// listener + Exporter + status server + signal plumbing into the real
// implementation; Task 8.1's skeleton returns a sentinel so the CLI
// shape (flags, --help, version, drain) is exercisable without a live
// daemon.
func runDaemon(_ context.Context, _ *Config) error {
	return errRunNotImplemented
}
