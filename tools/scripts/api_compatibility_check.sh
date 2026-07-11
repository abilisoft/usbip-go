#!/usr/bin/env bash

set -euo pipefail

if [[ -n "${GITHUB_BASE_REF:-}" ]]; then
	git fetch --no-tags --depth=200 origin "${GITHUB_BASE_REF}" || true
	range="origin/${GITHUB_BASE_REF}..HEAD"
else
	range="HEAD~1..HEAD"
fi
subjects=$(git log --format=%s "${range}" 2>/dev/null || git log -1 --format=%s 2>/dev/null || true)

for pkg in github.com/abilisoft/usbip-go/pkg/usbip github.com/abilisoft/usbip-go/pkg/domain; do
	short=${pkg##*/}
	baseline="api/pkg_${short}.json"
	if [[ ! -f "${baseline}" ]]; then
		printf 'missing API baseline for %s at %s — refusing to skip\n' "${pkg}" "${baseline}" >&2
		exit 1
	fi

	diff_file=$(mktemp)
	if ! apidiff "${baseline}" "${pkg}" >"${diff_file}"; then
		cat "${diff_file}"
		rm -f "${diff_file}"
		exit 1
	fi
	cat "${diff_file}"
	if grep -qE '^(Incompatible|Removed)' "${diff_file}"; then
		if ! printf '%s\n' "${subjects}" | grep -q '^BREAKING:'; then
			printf 'API break in %s without BREAKING: commit subject in range %s\n' "${pkg}" "${range}" >&2
			rm -f "${diff_file}"
			exit 1
		fi
	fi
	rm -f "${diff_file}"
done
