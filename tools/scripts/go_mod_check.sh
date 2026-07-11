#!/usr/bin/env bash

set -euo pipefail

readonly cache_root="${TEST_TMPDIR:-${TMPDIR:-/tmp}}/go-mod-check"

export GOCACHE="${GOCACHE:-${cache_root}/build}"
export GOMODCACHE="${GOMODCACHE:-${cache_root}/modules}"
export GOTMPDIR="${GOTMPDIR:-${cache_root}/tmp}"
export GOTOOLCHAIN=local
export GOWORK=off

mkdir -p "${GOCACHE}" "${GOMODCACHE}" "${GOTMPDIR}"

for module_dir in . tools; do
	(
		cd "${module_dir}"
		go mod tidy -diff
		go mod verify
	)
done
