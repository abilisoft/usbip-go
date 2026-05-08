// This file marks ./build/ as a nested Go module so the top-level
// `go test ./...` does not descend into build/cache/go-mod/* and
// attempt to compile vendored package test files (many of which were
// written before Go modules and fail to resolve Example identifiers
// when visited this way).
//
// The directory is git-ignored and recreated by the Taskfile; this
// file is deliberately tracked so ownership/creation order can never
// leave /build without a module marker.

module github.com/abilisoft/usbip-go/build-sandbox

go 1.26.2
