//go:build tools
// +build tools

// Package tools pins dev-tool versions via go.mod.
// Run `task install-tools` to install into $GOBIN.
package tools

import (
	_ "github.com/go-gremlins/gremlins/cmd/gremlins"
	_ "github.com/goreleaser/goreleaser/v2"
	_ "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
	_ "github.com/matryer/moq"
	_ "golang.org/x/vuln/cmd/govulncheck"
	_ "mvdan.cc/gofumpt"
)
