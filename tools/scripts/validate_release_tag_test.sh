#!/usr/bin/env bash

set -euo pipefail

readonly exit_failure=1
readonly exit_success=0

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
	local tag=$3
	local created=$4
	local forced=$5
	local deleted=$6
	local tag_object_type=$7
	local tag_object_name=$8
	local tag_target_type=$9
	local signature_verified=${10}
	local event_after=${11}
	local tag_target_commit=${12}
	local default_branch_commit=${13}
	local expected_message=${14}
	local output_file="${tmp}/${name}.out"
	local output
	local status=${exit_success}

	RELEASE_TAG=${tag} \
		RELEASE_EVENT_CREATED=${created} \
		RELEASE_EVENT_FORCED=${forced} \
		RELEASE_EVENT_DELETED=${deleted} \
		RELEASE_TAG_OBJECT_TYPE=${tag_object_type} \
		RELEASE_TAG_OBJECT_NAME=${tag_object_name} \
		RELEASE_TAG_TARGET_TYPE=${tag_target_type} \
		RELEASE_TAG_SIGNATURE_VERIFIED=${signature_verified} \
		RELEASE_EVENT_AFTER=${event_after} \
		RELEASE_TAG_TARGET_COMMIT=${tag_target_commit} \
		RELEASE_DEFAULT_BRANCH_COMMIT=${default_branch_commit} \
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

readonly expected_commit='0123456789abcdef0123456789abcdef01234567'

run_case fresh "${exit_success}" v1.2.3 true false false \
	tag v1.2.3 commit true \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'validated fresh stable release tag v1.2.3'
run_case moved "${exit_failure}" v1.2.3 false false false \
	tag v1.2.3 commit true \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'github.event.created must be true'
run_case forced "${exit_failure}" v1.2.3 true true false \
	tag v1.2.3 commit true \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'github.event.forced must be false'
run_case deleted "${exit_failure}" v1.2.3 true false true \
	tag v1.2.3 commit true \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'github.event.deleted must be false'
run_case malformed "${exit_failure}" v1.2 true false false \
	tag v1.2 commit true \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'is not a canonical stable release'
run_case prerelease "${exit_failure}" v1.2.3-rc1 true false false \
	tag v1.2.3-rc1 commit true \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'is not a canonical stable release'
run_case missing_created "${exit_failure}" v1.2.3 '' false false \
	tag v1.2.3 commit true \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'github.event.created must be true'
run_case missing_forced "${exit_failure}" v1.2.3 true '' false \
	tag v1.2.3 commit true \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'github.event.forced must be false'
run_case missing_deleted "${exit_failure}" v1.2.3 true false '' \
	tag v1.2.3 commit true \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'github.event.deleted must be false'

for tag in v01.2.3 v1.02.3 v1.2.03; do
	run_case "leading_zero_${tag//./_}" "${exit_failure}" "${tag}" true false false \
		tag "${tag}" commit true \
		"${expected_commit}" "${expected_commit}" "${expected_commit}" \
		'is not a canonical stable release'
done

run_case lightweight "${exit_failure}" v1.2.3 true false false \
	commit v1.2.3 commit false \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'release ref must point to an annotated tag object'
run_case wrong_tag_name "${exit_failure}" v1.2.3 true false false \
	tag v1.2.4 commit true \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'annotated tag object name must match'
run_case non_commit_target "${exit_failure}" v1.2.3 true false false \
	tag v1.2.3 tree true \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'must point directly to a commit'
run_case unverified_signature "${exit_failure}" v1.2.3 true false false \
	tag v1.2.3 commit false \
	"${expected_commit}" "${expected_commit}" "${expected_commit}" \
	'signature must be verified by GitHub'
run_case missing_event_target "${exit_failure}" v1.2.3 true false false \
	tag v1.2.3 commit true \
	'' "${expected_commit}" "${expected_commit}" \
	'release event target commit is required'
run_case missing_tag_target "${exit_failure}" v1.2.3 true false false \
	tag v1.2.3 commit true \
	"${expected_commit}" '' "${expected_commit}" \
	'annotated release tag target commit is required'
run_case missing_default_head "${exit_failure}" v1.2.3 true false false \
	tag v1.2.3 commit true \
	"${expected_commit}" "${expected_commit}" '' \
	'default-branch head commit is required'
run_case changed_since_event "${exit_failure}" v1.2.3 true false false \
	tag v1.2.3 commit true \
	"${expected_commit}" 1123456789abcdef0123456789abcdef01234567 "${expected_commit}" \
	'live annotated tag target does not match the release push event target'
run_case wrong_default_head "${exit_failure}" v1.2.3 true false false \
	tag v1.2.3 commit true \
	"${expected_commit}" "${expected_commit}" 2123456789abcdef0123456789abcdef01234567 \
	'release tag target must equal the current default-branch head'
