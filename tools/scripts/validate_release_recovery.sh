#!/usr/bin/env bash

set -euo pipefail

readonly fixed_controller_ref='refs/heads/main'
readonly fixed_release_tag='v1.0.2'
readonly fixed_tag_object_sha='f0c7083fdee40e1e31ebc170992fa5f43efe8d60'
readonly fixed_target_commit='72aa5a6b585d1f5b6230c8362254ea2a6296ec75'

fail() {
	printf '::error::%s\n' "$1" >&2
	exit 1
}

if [[ ${RECOVERY_TAG:-} != "${fixed_release_tag}" ]]; then
	fail "recovery confirmation must be ${fixed_release_tag}"
fi

if [[ ${RECOVERY_CONTROLLER_REF:-} != "${fixed_controller_ref}" ]]; then
	fail "release recovery must run from protected ${fixed_controller_ref}"
fi

if [[ -z ${RECOVERY_CONTROLLER_COMMIT:-} ]]; then
	fail 'release recovery controller commit is required'
fi

if [[ ${RECOVERY_CHECKED_OUT_CONTROLLER_COMMIT:-} != "${RECOVERY_CONTROLLER_COMMIT}" ]]; then
	fail 'checked-out recovery controller does not match the dispatch commit'
fi

if [[ ${RECOVERY_TAG_OBJECT_TYPE:-} != 'tag' ]]; then
	fail 'recovery release ref must point to an annotated tag object'
fi

if [[ ${RECOVERY_TAG_OBJECT_SHA:-} != "${fixed_tag_object_sha}" ]]; then
	fail 'live recovery tag object does not match the fixed annotated tag object'
fi

if [[ ${RECOVERY_TAG_OBJECT_NAME:-} != "${fixed_release_tag}" ]]; then
	fail 'recovery annotated tag object name does not match the fixed release tag'
fi

if [[ ${RECOVERY_TAG_TARGET_TYPE:-} != 'commit' ]]; then
	fail 'recovery annotated tag must point directly to a commit'
fi

if [[ ${RECOVERY_TAG_TARGET_COMMIT:-} != "${fixed_target_commit}" ]]; then
	fail 'live recovery tag target does not match the fixed source commit'
fi

if [[ ${RECOVERY_TAG_SIGNATURE_VERIFIED:-} != 'true' ]]; then
	fail 'recovery annotated tag signature must be verified by GitHub'
fi

if [[ ${RECOVERY_CHECKED_OUT_SOURCE_COMMIT:-} != "${fixed_target_commit}" ]]; then
	fail 'checked-out recovery source does not match the fixed source commit'
fi

if [[ ${RECOVERY_LOCAL_TAG_OBJECT_SHA:-} != "${fixed_tag_object_sha}" ]]; then
	fail 'checked-out recovery source does not contain the fixed tag object'
fi

if [[ ${RECOVERY_LOCAL_TAG_TARGET_COMMIT:-} != "${fixed_target_commit}" ]]; then
	fail 'checked-out recovery tag does not peel to the fixed source commit'
fi

if [[ ${RECOVERY_RELEASE_STATE:-} == 'draft' && ! ${RECOVERY_RELEASE_ID:-} =~ ^[1-9][0-9]*$ ]]; then
	fail 'recovery draft state requires the exact draft release ID'
fi

case "${RECOVERY_REQUIRED_RELEASE_STATE:-}" in
preflight)
	if [[ ${RECOVERY_RELEASE_STATE:-} == 'public' ]]; then
		fail "public release ${fixed_release_tag} already exists; recovery replay is forbidden"
	fi
	if [[ ${RECOVERY_RELEASE_STATE:-} != 'absent' && ${RECOVERY_RELEASE_STATE:-} != 'draft' ]]; then
		fail 'recovery preflight requires no release or one exact-tag draft'
	fi
	;;
draft)
	if [[ ${RECOVERY_RELEASE_STATE:-} != 'draft' ]]; then
		fail 'recovery publication requires exactly one exact-tag draft'
	fi
	if [[ -n ${RECOVERY_EXPECTED_RELEASE_ID:-} && ${RECOVERY_RELEASE_ID} != "${RECOVERY_EXPECTED_RELEASE_ID}" ]]; then
		fail 'live recovery draft does not match the bound release ID'
	fi
	;;
*)
	fail 'recovery release-state requirement must be preflight or draft'
	;;
esac

if [[ -n ${GITHUB_OUTPUT:-} ]]; then
	{
		printf 'release-tag=%s\n' "${fixed_release_tag}"
		printf 'tag-object-sha=%s\n' "${fixed_tag_object_sha}"
		printf 'source-commit=%s\n' "${fixed_target_commit}"
		if [[ ${RECOVERY_RELEASE_STATE:-} == 'draft' ]]; then
			printf 'release-id=%s\n' "${RECOVERY_RELEASE_ID}"
		fi
	} >>"${GITHUB_OUTPUT}"
fi

printf 'validated immutable release recovery for %s at %s\n' \
	"${fixed_release_tag}" "${fixed_target_commit}"
