#!/usr/bin/env bash

set -euo pipefail

readonly canonical_stable_tag_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'

fail() {
	printf '::error::%s\n' "$1" >&2
	exit 1
}

if [[ ${RELEASE_EVENT_CREATED:-} != 'true' ]]; then
	fail 'release tags must be fresh creations (github.event.created must be true)'
fi

if [[ ${RELEASE_EVENT_FORCED:-} != 'false' ]]; then
	fail 'forced release tag pushes are forbidden (github.event.forced must be false)'
fi

if [[ ${RELEASE_EVENT_DELETED:-} != 'false' ]]; then
	fail 'deleted release tag events are forbidden (github.event.deleted must be false)'
fi

if [[ ! ${RELEASE_TAG:-} =~ ${canonical_stable_tag_pattern} ]]; then
	fail "tag '${RELEASE_TAG:-}' is not a canonical stable release (vMAJOR.MINOR.PATCH)"
fi

if [[ ${RELEASE_TAG_OBJECT_TYPE:-} != 'tag' ]]; then
	fail 'release ref must point to an annotated tag object'
fi

if [[ ${RELEASE_TAG_OBJECT_NAME:-} != "${RELEASE_TAG}" ]]; then
	fail 'annotated tag object name must match the pushed release tag'
fi

if [[ ${RELEASE_TAG_TARGET_TYPE:-} != 'commit' ]]; then
	fail 'annotated release tag must point directly to a commit'
fi

if [[ ${RELEASE_TAG_SIGNATURE_VERIFIED:-} != 'true' ]]; then
	fail 'annotated release tag signature must be verified by GitHub'
fi

if [[ -z ${RELEASE_EVENT_AFTER:-} ]]; then
	fail 'release event target commit is required'
fi

if [[ -z ${RELEASE_TAG_TARGET_COMMIT:-} ]]; then
	fail 'annotated release tag target commit is required'
fi

if [[ -z ${RELEASE_DEFAULT_BRANCH_COMMIT:-} ]]; then
	fail 'default-branch head commit is required'
fi

if [[ ${RELEASE_TAG_TARGET_COMMIT} != "${RELEASE_EVENT_AFTER}" ]]; then
	fail 'live annotated tag target does not match the release push event target'
fi

if [[ ${RELEASE_TAG_TARGET_COMMIT} != "${RELEASE_DEFAULT_BRANCH_COMMIT}" ]]; then
	fail 'release tag target must equal the current default-branch head'
fi

printf 'validated fresh stable release tag %s\n' "${RELEASE_TAG}"
