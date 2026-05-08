// Package main is the usbip client CLI entrypoint (spec §7.1).
package main

import (
	"fmt"
	"os"
)

func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}

	os.Exit(code)
}

// run executes the root cobra command with args and returns the
// mapped exit code. Extracted so tests can drive the entrypoint
// without invoking os.Exit.
func run(args []string) (int, error) {
	cmd := newRootCmd()
	cmd.SetArgs(args)

	err := cmd.Execute()
	if err != nil {
		return MapError(err), err
	}

	return 0, nil
}
