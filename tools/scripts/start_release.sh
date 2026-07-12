#!/usr/bin/env bash

set -euo pipefail

readonly stable_tag_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'
readonly release_workflow='release.yml'

die() {
	printf 'start-release: %s\n' "$*" >&2
	exit 1
}

require_env() {
	local name=$1
	[[ -n "${!name:-}" ]] || die "${name} is required"
}

require_env DEFAULT_BRANCH
require_env GH_TOKEN
require_env GITHUB_REF_NAME
require_env GITHUB_REPOSITORY
require_env GITHUB_SHA
require_env RELEASE_TAG

[[ ${RELEASE_TAG} =~ ${stable_tag_pattern} ]] ||
	die "tag '${RELEASE_TAG}' is not canonical stable SemVer (vMAJOR.MINOR.PATCH)"
[[ ${GITHUB_REF_NAME} == "${DEFAULT_BRANCH}" ]] ||
	die "manual releases must start from default branch '${DEFAULT_BRANCH}', not '${GITHUB_REF_NAME}'"

current_sha=$(gh api \
	"repos/${GITHUB_REPOSITORY}/git/ref/heads/${DEFAULT_BRANCH}" \
	--jq '.object.sha') || die "could not read the current '${DEFAULT_BRANCH}' head"
[[ -n ${current_sha} ]] || die "GitHub returned an empty '${DEFAULT_BRANCH}' head"
[[ ${GITHUB_SHA} == "${current_sha}" ]] ||
	die "dispatch commit ${GITHUB_SHA} is stale; current '${DEFAULT_BRANCH}' head is ${current_sha}"

gh api --method POST "repos/${GITHUB_REPOSITORY}/git/refs" \
	-f "ref=refs/tags/${RELEASE_TAG}" \
	-f "sha=${GITHUB_SHA}" >/dev/null ||
	die "could not create tag '${RELEASE_TAG}' at ${GITHUB_SHA}"

if gh workflow run "${release_workflow}" \
	--repo "${GITHUB_REPOSITORY}" \
	--ref "${RELEASE_TAG}" \
	-f "tag=${RELEASE_TAG}"; then
	printf 'start-release: created %s and dispatched the tag-context release workflow\n' \
		"${RELEASE_TAG}"
	exit 0
fi

printf 'start-release: dispatch failed; deleting newly created tag %s\n' \
	"${RELEASE_TAG}" >&2
if ! gh api --method DELETE \
	"repos/${GITHUB_REPOSITORY}/git/refs/tags/${RELEASE_TAG}" >/dev/null; then
	printf 'start-release: rollback failed; delete tag %s manually before retrying\n' \
		"${RELEASE_TAG}" >&2
fi

die "could not dispatch the tag-context release workflow"
