#!/usr/bin/env bash

set -euo pipefail

readonly exit_failure=1
readonly exit_success=0

validator_path() {
	if [[ -n ${TEST_SRCDIR:-} && -n ${TEST_WORKSPACE:-} ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/validate_release_notes.sh"
		return
	fi

	printf '%s\n' "$(dirname "${BASH_SOURCE[0]}")/validate_release_notes.sh"
}

tmp=${TEST_TMPDIR:-$(mktemp -d)}

run_case() {
	local name=$1
	local expected_status=$2
	local tag=$3
	local notes=$4
	local expected_message=$5
	local notes_file="${tmp}/${name}.md"
	local output_file="${tmp}/${name}.out"
	local output
	local status=${exit_success}

	printf '%s' "${notes}" >"${notes_file}"
	RELEASE_NOTES_PATH=${notes_file} \
		RELEASE_TAG=${tag} \
		"$(validator_path)" >"${output_file}" 2>&1 || status=$?

	if [[ ${status} -ne ${expected_status} ]]; then
		printf '%s: expected status %d, got %d\n' \
			"${name}" "${expected_status}" "${status}" >&2
		printf '%s\n' "$(<"${output_file}")" >&2
		exit "${exit_failure}"
	fi

	output=$(<"${output_file}")
	if [[ ${output} != *"${expected_message}"* ]]; then
		printf '%s: expected output to contain %q\n' "${name}" "${expected_message}" >&2
		printf '%s\n' "${output}" >&2
		exit "${exit_failure}"
	fi
}

run_case valid "${exit_success}" v1.2.3 \
	$'## [1.2.3] — 2026-07-18\n\n### Bug Fixes\n- Fix release\n' \
	'validated release notes for v1.2.3'
run_case empty "${exit_failure}" v1.2.3 '' 'release notes are empty'
run_case wrong_version "${exit_failure}" v1.2.3 \
	$'## [1.2.4] — 2026-07-18\n' \
	'release notes heading does not match v1.2.3'
run_case prefix_collision "${exit_failure}" v1.2.3 \
	$'## [1.2.30] — 2026-07-18\n' \
	'release notes heading does not match v1.2.3'
run_case no_trailing_newline "${exit_success}" v1.2.3 \
	'## [1.2.3] — 2026-07-18' \
	'validated release notes for v1.2.3'

missing_path_output="${tmp}/missing_path.out"
status=${exit_success}
RELEASE_TAG=v1.2.3 "$(validator_path)" >"${missing_path_output}" 2>&1 || status=$?
if [[ ${status} -ne ${exit_failure} ]] ||
	[[ $(<"${missing_path_output}") != *'release-notes path is required'* ]]; then
	printf 'missing path must fail closed\n' >&2
	exit "${exit_failure}"
fi

missing_tag_output="${tmp}/missing_tag.out"
status=${exit_success}
RELEASE_NOTES_PATH="${tmp}/valid.md" "$(validator_path)" >"${missing_tag_output}" 2>&1 || status=$?
if [[ ${status} -ne ${exit_failure} ]] ||
	[[ $(<"${missing_tag_output}") != *'release tag is required'* ]]; then
	printf 'missing tag must fail closed\n' >&2
	exit "${exit_failure}"
fi
