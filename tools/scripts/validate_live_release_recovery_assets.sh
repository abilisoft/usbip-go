#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf '::error::%s\n' "$1" >&2
	exit 1
}

validator_path() {
	local path

	path="$(dirname "${BASH_SOURCE[0]}")/validate_release_recovery_assets.sh"
	if [[ -x ${path} ]]; then
		printf '%s\n' "${path}"
		return
	fi

	if [[ -n ${RUNFILES_DIR:-} ]]; then
		path="${RUNFILES_DIR}/_main/tools/scripts/validate_release_recovery_assets.sh"
		if [[ -x ${path} ]]; then
			printf '%s\n' "${path}"
			return
		fi
	fi

	path="${BASH_SOURCE[0]}.runfiles/_main/tools/scripts/validate_release_recovery_assets.sh"
	if [[ -x ${path} ]]; then
		printf '%s\n' "${path}"
		return
	fi

	fail 'pure recovered release asset validator is unavailable'
}

if [[ -z ${RECOVERY_REPOSITORY:-} ]]; then
	fail 'release recovery repository is required for asset validation'
fi

if [[ ! ${RECOVERY_RELEASE_ID:-} =~ ^[1-9][0-9]*$ ]]; then
	fail 'release recovery asset validation requires the bound draft release ID'
fi

release_row=$(
	gh api "repos/${RECOVERY_REPOSITORY}/releases/${RECOVERY_RELEASE_ID}" \
		--jq '[.id, .tag_name, (.draft | tostring), (.prerelease | tostring)] | @tsv'
) || fail 'unable to read the bound recovered draft release'
IFS=$'\t' read -r live_release_id release_tag release_draft release_prerelease <<<"${release_row}"

if [[ ${live_release_id} != "${RECOVERY_RELEASE_ID}" ]]; then
	fail 'GitHub returned a different recovered draft release ID'
fi

asset_rows=$(
	gh api "repos/${RECOVERY_REPOSITORY}/releases/${RECOVERY_RELEASE_ID}/assets?per_page=100" \
		--paginate --slurp \
		--jq 'add | .[] | [.id, .name, .size, (.digest // ""), .state] | @tsv'
) || fail 'unable to read the bound recovered draft assets'

export RECOVERY_ASSET_ROWS=${asset_rows}
export RECOVERY_RELEASE_DRAFT=${release_draft}
export RECOVERY_RELEASE_PRERELEASE=${release_prerelease}
export RECOVERY_RELEASE_TAG=${release_tag}

"$(validator_path)"
