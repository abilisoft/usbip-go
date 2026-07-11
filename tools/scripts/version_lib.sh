#!/usr/bin/env bash

# Shared git-derived version helpers for build stamping and package metadata.

set -euo pipefail

readonly HARNESS_VERSION_FALLBACK_BASE="0.0.0"
readonly HARNESS_VERSION_FALLBACK_SHA="0000000"
readonly HARNESS_VERSION_TAG_GLOB="[0-9]*.[0-9]*.[0-9]*"
readonly HARNESS_VERSION_TAG_REGEX='^[0-9]+\.[0-9]+\.[0-9]+$'

harness_git_short_sha() {
	git rev-parse --short=7 HEAD 2>/dev/null || printf '%s\n' "${HARNESS_VERSION_FALLBACK_SHA}"
}

harness_git_full_sha() {
	git rev-parse HEAD 2>/dev/null || printf '%s\n' "unknown"
}

harness_git_dirty_flag() {
	local status

	if status=$(git status --porcelain --untracked-files=all 2>/dev/null) && [[ -n "${status}" ]]; then
		printf '%s\n' "true"
		return
	fi

	printf '%s\n' "false"
}

harness_latest_version_tag() {
	local tag

	tag=$(git describe --tags --abbrev=0 --match "${HARNESS_VERSION_TAG_GLOB}" 2>/dev/null) || return 1
	if [[ ! "${tag}" =~ ${HARNESS_VERSION_TAG_REGEX} ]]; then
		return 1
	fi

	printf '%s\n' "${tag}"
}

harness_commit_distance() {
	local tag=${1:-}

	if [[ -n "${tag}" ]]; then
		git rev-list "${tag}..HEAD" --count 2>/dev/null || printf '%s\n' "0"
		return
	fi

	git rev-list HEAD --count 2>/dev/null || printf '%s\n' "0"
}

harness_pep440_version() {
	local sha
	local dirty_suffix=""
	local version
	local distance
	local tag=""

	sha=$(harness_git_short_sha)
	if [[ "$(harness_git_dirty_flag)" == "true" ]]; then
		dirty_suffix=".dirty"
	fi

	if tag=$(harness_latest_version_tag); then
		version="${tag}"
		distance=$(harness_commit_distance "${tag}")
	else
		version="${HARNESS_VERSION_FALLBACK_BASE}"
		distance=$(harness_commit_distance)
	fi

	if [[ "${distance}" == "0" && -z "${dirty_suffix}" ]]; then
		printf '%s\n' "${version}"
		return
	fi

	printf '%s.dev%s+g%s%s\n' "${version}" "${distance}" "${sha}" "${dirty_suffix}"
}
