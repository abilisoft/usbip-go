#!/usr/bin/env bash

set -euo pipefail

readonly git_author_name="USBIP Go Test"
readonly git_author_email="usbip-go-test@example.invalid"
readonly commit_date="2026-07-03T04:05:06Z"
readonly expected_iso_date="2026-07-03T04:05:06Z"

git_bin=$(command -v git) || {
	printf '%s\n' 'workspace_status_test: host Git is required by the local requires-git test target' >&2
	exit 1
}
readonly git_bin

script_dir=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir

script_path() {
	local name=$1

	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/${name}"
		return
	fi

	printf '%s\n' "${script_dir}/${name}"
}

tmp=${TEST_TMPDIR:-$(mktemp -d)}
repo="${tmp}/repo"
mkdir -p "${repo}"

"${git_bin}" -C "${repo}" init -q
"${git_bin}" -C "${repo}" config user.name "${git_author_name}"
"${git_bin}" -C "${repo}" config user.email "${git_author_email}"
"${git_bin}" -C "${repo}" config commit.gpgsign false
printf '%s\n' "fixture" >"${repo}/fixture"
"${git_bin}" -C "${repo}" add fixture
GIT_AUTHOR_DATE="${commit_date}" GIT_COMMITTER_DATE="${commit_date}" \
	"${git_bin}" -C "${repo}" commit -qm 'test: create workspace status fixture'
"${git_bin}" -C "${repo}" tag v2.3.4

full_sha=$("${git_bin}" -C "${repo}" rev-parse HEAD)
short_sha=$("${git_bin}" -C "${repo}" rev-parse --short=7 HEAD)

cat >"${tmp}/want" <<EOF
STABLE_VERSION 2.3.4
STABLE_GIT_COMMIT ${full_sha}
STABLE_GIT_SHA_SHORT ${short_sha}
STABLE_GIT_DIRTY false
STABLE_BUILD_DATE ${expected_iso_date}
EOF

(
	cd "${repo}"
	HARNESS_GIT="${git_bin}" "$(script_path workspace_status.sh)"
) >"${tmp}/first"
(
	cd "${repo}"
	HARNESS_GIT="${git_bin}" "$(script_path workspace_status.sh)"
) >"${tmp}/second"

diff -u "${tmp}/want" "${tmp}/first"
diff -u "${tmp}/first" "${tmp}/second"

if grep -Eq '(^|[[:space:]])BUILD_DATE([[:space:]]|$)' "${tmp}/first"; then
	printf '%s\n' 'workspace status must not emit volatile BUILD_DATE' >&2
	exit 1
fi
