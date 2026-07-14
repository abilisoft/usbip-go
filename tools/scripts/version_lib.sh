#!/usr/bin/env bash

# Shared git-derived version helpers for build stamping and package metadata.

set -euo pipefail

readonly HARNESS_VERSION_FALLBACK_BASE="0.0.0"
readonly HARNESS_VERSION_FALLBACK_SHA="0000000"
readonly HARNESS_BUILD_DATE_FALLBACK="unknown"
readonly HARNESS_VERSION_TAG_GLOB="v[0-9]*.[0-9]*.[0-9]*"
readonly HARNESS_VERSION_TAG_REGEX='^v[0-9]+\.[0-9]+\.[0-9]+$'
readonly HARNESS_GIT="${HARNESS_GIT:-git}"

harness_git_short_sha() {
	"${HARNESS_GIT}" rev-parse --short=7 HEAD 2>/dev/null || printf '%s\n' "${HARNESS_VERSION_FALLBACK_SHA}"
}

harness_git_full_sha() {
	"${HARNESS_GIT}" rev-parse HEAD 2>/dev/null || printf '%s\n' "unknown"
}

harness_git_commit_date() {
	"${HARNESS_GIT}" show -s --format=%cI HEAD 2>/dev/null || printf '%s\n' "${HARNESS_BUILD_DATE_FALLBACK}"
}

harness_git_dirty_flag() {
	local status

	if status=$("${HARNESS_GIT}" status --porcelain --untracked-files=all 2>/dev/null) && [[ -n "${status}" ]]; then
		printf '%s\n' "true"
		return
	fi

	printf '%s\n' "false"
}

harness_latest_version_tag() {
	local -a describe_args=()
	local tag

	while IFS= read -r tag; do
		if [[ "${tag}" =~ ${HARNESS_VERSION_TAG_REGEX} ]]; then
			describe_args+=(--match "${tag}")
		fi
	done < <("${HARNESS_GIT}" tag --merged HEAD --list "${HARNESS_VERSION_TAG_GLOB}" 2>/dev/null)

	((${#describe_args[@]} > 0)) || return 1
	tag=$("${HARNESS_GIT}" describe --tags --abbrev=0 "${describe_args[@]}" 2>/dev/null) || return 1

	printf '%s\n' "${tag}"
}

harness_commit_distance() {
	local tag=${1:-}

	if [[ -n "${tag}" ]]; then
		"${HARNESS_GIT}" rev-list "${tag}..HEAD" --count 2>/dev/null || printf '%s\n' "0"
		return
	fi

	"${HARNESS_GIT}" rev-list HEAD --count 2>/dev/null || printf '%s\n' "0"
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
		version="${tag#v}"
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
