package wire_test

import "sync"

// slogDefaultMu serializes tests that mutate the global slog default
// handler. Declared here (a test-only file) to keep the concurrency
// primitive out of live code. Test files are exempt from
// gochecknoglobals via .golangci.yml exclusion.
var slogDefaultMu sync.Mutex
