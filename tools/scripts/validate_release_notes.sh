#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf '::error::%s\n' "$1" >&2
	exit 1
}

if [[ -z ${RELEASE_TAG:-} ]]; then
	fail 'release tag is required for release-note validation'
fi

if [[ -z ${RELEASE_NOTES_PATH:-} ]]; then
	fail 'release-notes path is required'
fi

if [[ ! -s ${RELEASE_NOTES_PATH} ]]; then
	fail 'release notes are empty'
fi

actual_heading=''
IFS= read -r actual_heading <"${RELEASE_NOTES_PATH}" || true
readonly expected_heading="## [${RELEASE_TAG#v}] — "
if [[ ${actual_heading} != "${expected_heading}"* ]]; then
	fail "release notes heading does not match ${RELEASE_TAG}"
fi

printf 'validated release notes for %s\n' "${RELEASE_TAG}"
