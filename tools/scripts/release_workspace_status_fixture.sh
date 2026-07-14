#!/usr/bin/env bash

set -euo pipefail

# Deterministic workspace status for the production release-stamping gate.
# printf is a Bash builtin, so the fixture needs no ambient executable.
printf '%s\n' \
	'STABLE_VERSION 9.8.7' \
	'STABLE_GIT_COMMIT 0123456789abcdef0123456789abcdef01234567' \
	'STABLE_GIT_SHA_SHORT 0123456' \
	'STABLE_GIT_DIRTY false' \
	'STABLE_BUILD_DATE 2026-07-03T04:05:06Z'
