// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build tools
// +build tools

// Package tools pins dev-tool versions via go.mod blank imports so
// `go install -mod=mod <import-path>` resolves the pinned version
// instead of @latest. The hermetic devShell (flake.nix) ships
// goreleaser, golangci-lint, gofumpt, gotools (goimports), moq,
// and govulncheck DIRECTLY from nixpkgs — those binaries do NOT
// belong here, and listing them pulled their full transitive dep
// graph into go.sum (notably the goreleaser tree, which dragged in
// vulnerable in-toto-golang and modelcontextprotocol/registry
// versions that surfaced on the OpenSSF Scorecard Vulnerabilities
// check).
//
// What stays here is the genuine escape hatch — tools that nixpkgs
// does NOT ship and which CI does not install ad-hoc:
//
//   - gremlins: mutation-testing harness; absent from nixpkgs at
//     the version Taskfile's ci:test:mutation depends on.
//
// apidiff is installed ad-hoc in the api-compatibility CI job via
// `go install golang.org/x/exp/cmd/apidiff@<pseudo-version>` so
// the version pin lives at the call site, not here.
package tools

import (
	_ "github.com/go-gremlins/gremlins/cmd/gremlins"
)
