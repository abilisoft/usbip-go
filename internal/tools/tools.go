// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build tools
// +build tools

// Package tools pins dev-tool versions via go.mod blank imports so
// `go install -mod=mod <import-path>` resolves the pinned version
// instead of @latest. The hermetic devShell (flake.nix) ships most
// of these binaries directly; this file is the escape hatch for the
// few that are not in nixpkgs (gremlins) or are installed ad-hoc by
// CI jobs (apidiff in the api-surface workflow).
package tools

import (
	_ "github.com/go-gremlins/gremlins/cmd/gremlins"
	_ "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
	_ "github.com/goreleaser/goreleaser/v2"
	_ "github.com/matryer/moq"
	_ "golang.org/x/exp/cmd/apidiff"
	_ "golang.org/x/tools/cmd/goimports"
	_ "golang.org/x/vuln/cmd/govulncheck"
	_ "mvdan.cc/gofumpt"
)
