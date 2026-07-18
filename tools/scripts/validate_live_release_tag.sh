#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf '::error::%s\n' "$1" >&2
	exit 1
}

validator_path() {
	local path

	path="$(dirname "${BASH_SOURCE[0]}")/validate_release_tag.sh"
	if [[ -x ${path} ]]; then
		printf '%s\n' "${path}"
		return
	fi

	if [[ -n ${RUNFILES_DIR:-} ]]; then
		path="${RUNFILES_DIR}/_main/tools/scripts/validate_release_tag.sh"
		if [[ -x ${path} ]]; then
			printf '%s\n' "${path}"
			return
		fi
	fi

	path="${BASH_SOURCE[0]}.runfiles/_main/tools/scripts/validate_release_tag.sh"
	if [[ -x ${path} ]]; then
		printf '%s\n' "${path}"
		return
	fi

	fail 'pure release tag validator is unavailable'
}

if [[ -z ${RELEASE_REPOSITORY:-} ]]; then
	fail 'release repository is required'
fi

if [[ -z ${RELEASE_TAG:-} ]]; then
	fail 'release tag is required'
fi

readonly tag_ref_endpoint="repos/${RELEASE_REPOSITORY}/git/ref/tags/${RELEASE_TAG}"
IFS=$'\t' read -r release_tag_object_type release_tag_object_sha < <(
	gh api "${tag_ref_endpoint}" --jq '.object | [.type, .sha] | @tsv'
)

release_tag_object_name=''
release_tag_target_type=''
release_tag_target_commit=''
release_tag_signature_verified='false'
if [[ ${release_tag_object_type} == 'tag' ]]; then
	readonly tag_object_endpoint="repos/${RELEASE_REPOSITORY}/git/tags/${release_tag_object_sha}"
	IFS=$'\t' read -r \
		release_tag_object_name \
		release_tag_target_type \
		release_tag_target_commit \
		release_tag_signature_verified < <(
			gh api "${tag_object_endpoint}" \
				--jq '[.tag, .object.type, .object.sha, (.verification.verified | tostring)] | @tsv'
		)
fi

if [[ -z ${RELEASE_DEFAULT_BRANCH_COMMIT:-} ]]; then
	RELEASE_DEFAULT_BRANCH_COMMIT=$(git rev-parse HEAD)
fi

export RELEASE_DEFAULT_BRANCH_COMMIT
export RELEASE_TAG_OBJECT_NAME=${release_tag_object_name}
export RELEASE_TAG_OBJECT_TYPE=${release_tag_object_type}
export RELEASE_TAG_SIGNATURE_VERIFIED=${release_tag_signature_verified}
export RELEASE_TAG_TARGET_COMMIT=${release_tag_target_commit}
export RELEASE_TAG_TARGET_TYPE=${release_tag_target_type}

"$(validator_path)"
