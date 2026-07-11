#!/usr/bin/env bash

set -euo pipefail

readonly breaking_subject_pattern='^[a-z][a-z0-9-]*(\([a-z0-9][a-z0-9._/-]*\))?!: .+'
readonly breaking_footer_pattern='^BREAKING[ -]CHANGE: .+'
readonly fetch_depth=200
readonly exit_failure=1

has_breaking_marker() {
	local messages="$1"

	printf '%s\n' "${messages}" |
		grep -Eq "${breaking_subject_pattern}|${breaking_footer_pattern}"
}

main() {
	local range
	if [[ -n "${GITHUB_BASE_REF:-}" ]]; then
		git fetch --no-tags --depth="${fetch_depth}" origin "${GITHUB_BASE_REF}" || true
		range="origin/${GITHUB_BASE_REF}..HEAD"
	else
		range="HEAD~1..HEAD"
	fi

	local messages
	messages=$(git log --format='%s%n%b' "${range}" 2>/dev/null ||
		git log -1 --format='%s%n%b' 2>/dev/null || true)

	local pkg
	for pkg in github.com/abilisoft/usbip-go/pkg/usbip github.com/abilisoft/usbip-go/pkg/domain; do
		local short=${pkg##*/}
		local baseline="api/pkg_${short}.json"
		if [[ ! -f "${baseline}" ]]; then
			printf 'missing API baseline for %s at %s — refusing to skip\n' "${pkg}" "${baseline}" >&2
			return "${exit_failure}"
		fi

		local diff_file
		diff_file=$(mktemp)
		if ! apidiff "${baseline}" "${pkg}" >"${diff_file}"; then
			cat "${diff_file}"
			rm -f "${diff_file}"
			return "${exit_failure}"
		fi
		cat "${diff_file}"
		if grep -qE '^(Incompatible|Removed)' "${diff_file}" &&
			! has_breaking_marker "${messages}"; then
			printf 'API break in %s without a Conventional Commit breaking marker in range %s\n' \
				"${pkg}" "${range}" >&2
			rm -f "${diff_file}"
			return "${exit_failure}"
		fi
		rm -f "${diff_file}"
	done
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	main "$@"
fi
