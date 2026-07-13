#!/usr/bin/env bash

set -euo pipefail

readonly expected_version="9.8.7"
readonly expected_commit="0123456789abcdef0123456789abcdef01234567"
readonly expected_build_date="2026-07-03T04:05:06Z"
readonly metadata_pattern='^usbip-go version ([^[:space:]]+)[[:space:]]+\(commit ([^,]+), built ([^,]+), [^)]+\)[[:space:]]*$'

readonly binary="${TEST_SRCDIR}/${TEST_WORKSPACE}/cmd/usbip-go/usbip-go_/usbip-go"
output=$("${binary}" version)

if [[ ! ${output} =~ ${metadata_pattern} ]]; then
	printf 'release_stamping_test: cannot parse version output: %q\n' "${output}" >&2
	exit 1
fi

assert_equal() {
	local field=$1
	local want=$2
	local got=$3

	if [[ "${got}" != "${want}" ]]; then
		printf 'release_stamping_test: %s = %q, want %q\n' "${field}" "${got}" "${want}" >&2
		exit 1
	fi
}

assert_equal version "${expected_version}" "${BASH_REMATCH[1]}"
assert_equal commit "${expected_commit}" "${BASH_REMATCH[2]}"
assert_equal build-date "${expected_build_date}" "${BASH_REMATCH[3]}"
