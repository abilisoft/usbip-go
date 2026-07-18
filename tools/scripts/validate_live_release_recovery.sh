#!/usr/bin/env bash

set -euo pipefail

readonly fixed_release_tag='v1.0.2'

fail() {
	printf '::error::%s\n' "$1" >&2
	exit 1
}

validator_path() {
	local path

	path="$(dirname "${BASH_SOURCE[0]}")/validate_release_recovery.sh"
	if [[ -x ${path} ]]; then
		printf '%s\n' "${path}"
		return
	fi

	if [[ -n ${RUNFILES_DIR:-} ]]; then
		path="${RUNFILES_DIR}/_main/tools/scripts/validate_release_recovery.sh"
		if [[ -x ${path} ]]; then
			printf '%s\n' "${path}"
			return
		fi
	fi

	path="${BASH_SOURCE[0]}.runfiles/_main/tools/scripts/validate_release_recovery.sh"
	if [[ -x ${path} ]]; then
		printf '%s\n' "${path}"
		return
	fi

	fail 'pure release recovery validator is unavailable'
}

if [[ -z ${RECOVERY_REPOSITORY:-} ]]; then
	fail 'release recovery repository is required'
fi

if [[ ${RECOVERY_TAG:-} != "${fixed_release_tag}" ]]; then
	fail "release recovery supports only ${fixed_release_tag}"
fi

if [[ -z ${RECOVERY_CONTROLLER_PATH:-} ]]; then
	fail 'release recovery controller checkout path is required'
fi

if [[ -z ${RECOVERY_SOURCE_PATH:-} ]]; then
	fail 'release recovery source checkout path is required'
fi

readonly tag_ref_endpoint="repos/${RECOVERY_REPOSITORY}/git/ref/tags/${fixed_release_tag}"
tag_ref_output=$(gh api "${tag_ref_endpoint}" --jq '.object | [.type, .sha] | @tsv') ||
	fail 'unable to read the live recovery tag ref'
IFS=$'\t' read -r recovery_tag_object_type recovery_tag_object_sha <<<"${tag_ref_output}"

recovery_tag_object_name=''
recovery_tag_target_type=''
recovery_tag_target_commit=''
recovery_tag_signature_verified='false'
if [[ ${recovery_tag_object_type} == 'tag' ]]; then
	readonly tag_object_endpoint="repos/${RECOVERY_REPOSITORY}/git/tags/${recovery_tag_object_sha}"
	tag_object_output=$(
		gh api "${tag_object_endpoint}" \
			--jq '[.tag, .object.type, .object.sha, (.verification.verified | tostring)] | @tsv'
	) || fail 'unable to read the live recovery annotated tag object'
	IFS=$'\t' read -r \
		recovery_tag_object_name \
		recovery_tag_target_type \
		recovery_tag_target_commit \
		recovery_tag_signature_verified <<<"${tag_object_output}"
fi

readonly releases_endpoint="repos/${RECOVERY_REPOSITORY}/releases?per_page=100"
release_rows=$(
	gh api "${releases_endpoint}" --paginate \
		--jq ".[] | select(.tag_name == \"${fixed_release_tag}\") | [(.draft | tostring), (.id | tostring)] | @tsv"
) || fail 'unable to inspect existing recovery releases'

recovery_draft_count=0
recovery_public_count=0
recovery_release_id=''
while IFS=$'\t' read -r release_is_draft live_release_id; do
	[[ -n ${release_is_draft} ]] || continue
	case "${release_is_draft}" in
	true)
		((recovery_draft_count += 1))
		if ((recovery_draft_count == 1)); then
			recovery_release_id=${live_release_id}
		fi
		;;
	false) ((recovery_public_count += 1)) ;;
	*) fail 'GitHub returned an invalid recovery release state' ;;
	esac
done <<<"${release_rows}"

case "${recovery_draft_count}:${recovery_public_count}" in
0:0) recovery_release_state='absent' ;;
1:0) recovery_release_state='draft' ;;
*:0) recovery_release_state='multiple' ;;
*) recovery_release_state='public' ;;
esac

recovery_checked_out_controller_commit=$(
	git -C "${RECOVERY_CONTROLLER_PATH}" rev-parse HEAD
) || fail 'unable to resolve the checked-out recovery controller commit'
recovery_checked_out_source_commit=$(
	git -C "${RECOVERY_SOURCE_PATH}" rev-parse HEAD
) || fail 'unable to resolve the checked-out recovery source commit'
recovery_local_tag_object_sha=$(
	git -C "${RECOVERY_SOURCE_PATH}" rev-parse "refs/tags/${fixed_release_tag}"
) || fail 'checked-out recovery source does not contain the fixed tag ref'
recovery_local_tag_target_commit=$(
	git -C "${RECOVERY_SOURCE_PATH}" rev-parse "refs/tags/${fixed_release_tag}^{commit}"
) || fail 'unable to peel the checked-out recovery tag to a commit'

export RECOVERY_CHECKED_OUT_CONTROLLER_COMMIT=${recovery_checked_out_controller_commit}
export RECOVERY_CHECKED_OUT_SOURCE_COMMIT=${recovery_checked_out_source_commit}
export RECOVERY_LOCAL_TAG_OBJECT_SHA=${recovery_local_tag_object_sha}
export RECOVERY_LOCAL_TAG_TARGET_COMMIT=${recovery_local_tag_target_commit}
export RECOVERY_RELEASE_ID=${recovery_release_id}
export RECOVERY_RELEASE_STATE=${recovery_release_state}
export RECOVERY_TAG_OBJECT_NAME=${recovery_tag_object_name}
export RECOVERY_TAG_OBJECT_SHA=${recovery_tag_object_sha}
export RECOVERY_TAG_OBJECT_TYPE=${recovery_tag_object_type}
export RECOVERY_TAG_SIGNATURE_VERIFIED=${recovery_tag_signature_verified}
export RECOVERY_TAG_TARGET_COMMIT=${recovery_tag_target_commit}
export RECOVERY_TAG_TARGET_TYPE=${recovery_tag_target_type}

"$(validator_path)"
