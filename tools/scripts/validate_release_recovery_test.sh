#!/usr/bin/env bash

set -euo pipefail

readonly exit_failure=1
readonly exit_success=0
readonly fixed_controller_commit='1111111111111111111111111111111111111111'
readonly fixed_tag_object='f0c7083fdee40e1e31ebc170992fa5f43efe8d60'
readonly fixed_target_commit='72aa5a6b585d1f5b6230c8362254ea2a6296ec75'

validator_path() {
	if [[ -n ${TEST_SRCDIR:-} && -n ${TEST_WORKSPACE:-} ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/validate_release_recovery.sh"
		return
	fi

	printf '%s\n' "$(dirname "${BASH_SOURCE[0]}")/validate_release_recovery.sh"
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
		"RECOVERY_TAG=v1.0.2"
		"RECOVERY_CONTROLLER_REF=refs/heads/main"
		"RECOVERY_CONTROLLER_COMMIT=${fixed_controller_commit}"
		"RECOVERY_CHECKED_OUT_CONTROLLER_COMMIT=${fixed_controller_commit}"
		"RECOVERY_TAG_OBJECT_TYPE=tag"
		"RECOVERY_TAG_OBJECT_SHA=${fixed_tag_object}"
		"RECOVERY_TAG_OBJECT_NAME=v1.0.2"
		"RECOVERY_TAG_TARGET_TYPE=commit"
		"RECOVERY_TAG_TARGET_COMMIT=${fixed_target_commit}"
		"RECOVERY_TAG_SIGNATURE_VERIFIED=true"
		"RECOVERY_CHECKED_OUT_SOURCE_COMMIT=${fixed_target_commit}"
		"RECOVERY_LOCAL_TAG_OBJECT_SHA=${fixed_tag_object}"
		"RECOVERY_LOCAL_TAG_TARGET_COMMIT=${fixed_target_commit}"
		"RECOVERY_REQUIRED_RELEASE_STATE=preflight"
		"RECOVERY_RELEASE_STATE=absent"
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

run_case absent "${exit_success}" 'validated immutable release recovery'
run_case existing_draft "${exit_success}" 'validated immutable release recovery' \
	RECOVERY_RELEASE_STATE=draft RECOVERY_RELEASE_ID=123
run_case publish_draft "${exit_success}" 'validated immutable release recovery' \
	RECOVERY_REQUIRED_RELEASE_STATE=draft RECOVERY_RELEASE_STATE=draft RECOVERY_RELEASE_ID=123
run_case publish_wrong_draft "${exit_failure}" 'does not match the bound release ID' \
	RECOVERY_REQUIRED_RELEASE_STATE=draft RECOVERY_RELEASE_STATE=draft \
	RECOVERY_RELEASE_ID=123 RECOVERY_EXPECTED_RELEASE_ID=456
run_case draft_missing_id "${exit_failure}" 'draft state requires the exact draft release ID' \
	RECOVERY_RELEASE_STATE=draft
run_case wrong_confirmation "${exit_failure}" 'recovery confirmation must be v1.0.2' \
	RECOVERY_TAG=v1.0.3
run_case wrong_controller_ref "${exit_failure}" 'must run from protected refs/heads/main' \
	RECOVERY_CONTROLLER_REF=refs/heads/release
run_case missing_controller_commit "${exit_failure}" 'controller commit is required' \
	RECOVERY_CONTROLLER_COMMIT=
run_case wrong_controller_checkout "${exit_failure}" 'controller does not match the dispatch commit' \
	RECOVERY_CHECKED_OUT_CONTROLLER_COMMIT=2222222222222222222222222222222222222222
run_case lightweight "${exit_failure}" 'must point to an annotated tag object' \
	RECOVERY_TAG_OBJECT_TYPE=commit
run_case changed_object "${exit_failure}" 'does not match the fixed annotated tag object' \
	RECOVERY_TAG_OBJECT_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
run_case changed_name "${exit_failure}" 'name does not match the fixed release tag' \
	RECOVERY_TAG_OBJECT_NAME=v1.0.3
run_case changed_target_type "${exit_failure}" 'must point directly to a commit' \
	RECOVERY_TAG_TARGET_TYPE=tree
run_case changed_target "${exit_failure}" 'does not match the fixed source commit' \
	RECOVERY_TAG_TARGET_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
run_case unverified "${exit_failure}" 'signature must be verified by GitHub' \
	RECOVERY_TAG_SIGNATURE_VERIFIED=false
run_case wrong_source_checkout "${exit_failure}" 'source does not match the fixed source commit' \
	RECOVERY_CHECKED_OUT_SOURCE_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
run_case wrong_local_object "${exit_failure}" 'does not contain the fixed tag object' \
	RECOVERY_LOCAL_TAG_OBJECT_SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
run_case wrong_local_target "${exit_failure}" 'does not peel to the fixed source commit' \
	RECOVERY_LOCAL_TAG_TARGET_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
run_case public_replay "${exit_failure}" 'recovery replay is forbidden' \
	RECOVERY_RELEASE_STATE=public
run_case multiple_drafts "${exit_failure}" 'requires no release or one exact-tag draft' \
	RECOVERY_RELEASE_STATE=multiple
run_case publish_absent "${exit_failure}" 'publication requires exactly one exact-tag draft' \
	RECOVERY_REQUIRED_RELEASE_STATE=draft RECOVERY_RELEASE_STATE=absent
run_case invalid_state_requirement "${exit_failure}" 'must be preflight or draft' \
	RECOVERY_REQUIRED_RELEASE_STATE=unknown

output_path="${tmp}/github-output"
GITHUB_OUTPUT=${output_path} env \
	"RECOVERY_TAG=v1.0.2" \
	"RECOVERY_CONTROLLER_REF=refs/heads/main" \
	"RECOVERY_CONTROLLER_COMMIT=${fixed_controller_commit}" \
	"RECOVERY_CHECKED_OUT_CONTROLLER_COMMIT=${fixed_controller_commit}" \
	"RECOVERY_TAG_OBJECT_TYPE=tag" \
	"RECOVERY_TAG_OBJECT_SHA=${fixed_tag_object}" \
	"RECOVERY_TAG_OBJECT_NAME=v1.0.2" \
	"RECOVERY_TAG_TARGET_TYPE=commit" \
	"RECOVERY_TAG_TARGET_COMMIT=${fixed_target_commit}" \
	"RECOVERY_TAG_SIGNATURE_VERIFIED=true" \
	"RECOVERY_CHECKED_OUT_SOURCE_COMMIT=${fixed_target_commit}" \
	"RECOVERY_LOCAL_TAG_OBJECT_SHA=${fixed_tag_object}" \
	"RECOVERY_LOCAL_TAG_TARGET_COMMIT=${fixed_target_commit}" \
	"RECOVERY_REQUIRED_RELEASE_STATE=preflight" \
	"RECOVERY_RELEASE_STATE=absent" \
	"$(validator_path)" >/dev/null

grep -Fx "release-tag=v1.0.2" "${output_path}" >/dev/null
grep -Fx "tag-object-sha=${fixed_tag_object}" "${output_path}" >/dev/null
grep -Fx "source-commit=${fixed_target_commit}" "${output_path}" >/dev/null
