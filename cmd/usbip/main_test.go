package main

import (
	"os"
	"testing"
)

// TestMain disables cobra's flag-completion registration on the test
// root command. In production the binary builds one root command so
// cobra's global flagCompletionFunctions map is safe; in tests we
// construct a fresh root per-test and a data race emerges between
// bash completion generation (which mutates every globally-registered
// flag's Annotations) and ValidateRequiredFlags (which reads them).
func TestMain(m *testing.M) {
	skipFlagCompletionRegistration = true

	os.Exit(m.Run())
}
