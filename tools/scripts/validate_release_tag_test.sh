#!/usr/bin/env bash

set -euo pipefail

readonly exit_failure=1
readonly exit_success=0
readonly expected_commit='0123456789abcdef0123456789abcdef01234567'
readonly expected_tag_object='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'

validator_path() {
	if [[ -n ${TEST_SRCDIR:-} && -n ${TEST_WORKSPACE:-} ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/validate_release_tag.sh"
		return
	fi

	printf '%s\n' "$(dirname "${BASH_SOURCE[0]}")/validate_release_tag.sh"
}

tmp=${TEST_TMPDIR:-$(mktemp -d)}

run_case() {
	local name=$1
	local expected_status=$2
	local expected_message=$3
	shift 3
	local output_file="${tmp}/${name}.out"
	local output
	local status=${exit_success}
	local baseline=(
		"RELEASE_TAG=v1.2.3"
		"RELEASE_EVENT_CREATED=true"
		"RELEASE_EVENT_FORCED=false"
		"RELEASE_EVENT_DELETED=false"
		"RELEASE_TAG_OBJECT_TYPE=tag"
		"RELEASE_TAG_OBJECT_NAME=v1.2.3"
		"RELEASE_TAG_OBJECT_SHA=${expected_tag_object}"
		"RELEASE_TAG_TARGET_TYPE=commit"
		"RELEASE_TAG_SIGNATURE_VERIFIED=true"
		"RELEASE_EVENT_AFTER=${expected_tag_object}"
		"RELEASE_EVENT_TARGET_COMMIT=${expected_commit}"
		"RELEASE_TAG_TARGET_COMMIT=${expected_commit}"
		"RELEASE_CHECKED_OUT_COMMIT=${expected_commit}"
	)

	env "${baseline[@]}" "$@" "$(validator_path)" >"${output_file}" 2>&1 || status=$?

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

run_case fresh "${exit_success}" 'validated fresh stable release tag v1.2.3'
run_case moved "${exit_failure}" 'github.event.created must be true' \
	RELEASE_EVENT_CREATED=false
run_case forced "${exit_failure}" 'github.event.forced must be false' \
	RELEASE_EVENT_FORCED=true
run_case deleted "${exit_failure}" 'github.event.deleted must be false' \
	RELEASE_EVENT_DELETED=true
run_case malformed "${exit_failure}" 'is not a canonical stable release' \
	RELEASE_TAG=v1.2 RELEASE_TAG_OBJECT_NAME=v1.2
run_case prerelease "${exit_failure}" 'is not a canonical stable release' \
	RELEASE_TAG=v1.2.3-rc1 RELEASE_TAG_OBJECT_NAME=v1.2.3-rc1
run_case missing_created "${exit_failure}" 'github.event.created must be true' \
	RELEASE_EVENT_CREATED=
run_case missing_forced "${exit_failure}" 'github.event.forced must be false' \
	RELEASE_EVENT_FORCED=
run_case missing_deleted "${exit_failure}" 'github.event.deleted must be false' \
	RELEASE_EVENT_DELETED=

for tag in v01.2.3 v1.02.3 v1.2.03; do
	run_case "leading_zero_${tag//./_}" "${exit_failure}" \
		'is not a canonical stable release' \
		"RELEASE_TAG=${tag}" "RELEASE_TAG_OBJECT_NAME=${tag}"
done

run_case lightweight "${exit_failure}" 'release ref must point to an annotated tag object' \
	RELEASE_TAG_OBJECT_TYPE=commit RELEASE_TAG_SIGNATURE_VERIFIED=false
run_case wrong_tag_name "${exit_failure}" 'annotated tag object name must match' \
	RELEASE_TAG_OBJECT_NAME=v1.2.4
run_case non_commit_target "${exit_failure}" 'must point directly to a commit' \
	RELEASE_TAG_TARGET_TYPE=tree
run_case unverified_signature "${exit_failure}" 'signature must be verified by GitHub' \
	RELEASE_TAG_SIGNATURE_VERIFIED=false
run_case missing_event_object "${exit_failure}" 'release event tag-object SHA is required' \
	RELEASE_EVENT_AFTER=
run_case missing_event_target "${exit_failure}" 'release event target commit is required' \
	RELEASE_EVENT_TARGET_COMMIT=
run_case missing_live_object "${exit_failure}" 'live release tag-object SHA is required' \
	RELEASE_TAG_OBJECT_SHA=
run_case missing_tag_target "${exit_failure}" 'annotated release tag target commit is required' \
	RELEASE_TAG_TARGET_COMMIT=
run_case missing_checkout "${exit_failure}" 'checked-out release commit is required' \
	RELEASE_CHECKED_OUT_COMMIT=
run_case changed_object_since_event "${exit_failure}" \
	'live annotated tag object does not match the release push event object' \
	RELEASE_TAG_OBJECT_SHA=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
run_case changed_target_since_event "${exit_failure}" \
	'live annotated tag target does not match the release push event commit' \
	RELEASE_TAG_TARGET_COMMIT=1123456789abcdef0123456789abcdef01234567
run_case wrong_checkout "${exit_failure}" \
	'release tag target must equal the checked-out release commit' \
	RELEASE_CHECKED_OUT_COMMIT=2123456789abcdef0123456789abcdef01234567
