#!/usr/bin/env bash

set -euo pipefail

readonly RUNNERS=(__RUNNERS__)

if [[ -z "${RUNFILES_DIR:-}" ]]; then
	RUNFILES_DIR="${BASH_SOURCE[0]}.runfiles"
	export RUNFILES_DIR
fi

execroot() {
	cd "$(dirname "$0")/../../../.." && pwd
}

for runner in "${RUNNERS[@]}"; do
	if [[ "${runner}" != /* ]]; then
		runner="$(execroot)/${runner}"
	fi
	"${runner}"
done
