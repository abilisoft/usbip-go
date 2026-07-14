#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir

script_path() {
	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/pure_go_check.sh"
		return
	fi

	printf '%s\n' "${script_dir}/pure_go_check.sh"
}

tmp=${TEST_TMPDIR:-$(mktemp -d)}
repo="${tmp}/repo"
mkdir -p "${repo}/vendor/example.com/dependency"

cat >"${repo}/go.mod" <<'EOF'
module example.com/pure-go-check

go 1.26
EOF

cat >"${repo}/main.go" <<'EOF'
package main

func main() {}
EOF

cat >"${repo}/vendor/example.com/dependency/cgo.go" <<'EOF'
package dependency

import "C"
EOF

(
	cd "${repo}"
	CGO_ENABLED=0 "$(script_path)"
)

cat >"${repo}/cgo.go" <<'EOF'
package main

import "C"
EOF

if (
	cd "${repo}"
	CGO_ENABLED=0 "$(script_path)"
) >"${tmp}/first-party.out" 2>"${tmp}/first-party.err"; then
	printf '%s\n' 'expected first-party cgo source to fail the pure-Go check' >&2
	exit 1
fi

grep -Fq './cgo.go:3:import "C"' "${tmp}/first-party.out"
grep -Fq 'CGO_ENABLED=0 policy violation' "${tmp}/first-party.err"
