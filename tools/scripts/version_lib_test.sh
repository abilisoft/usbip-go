#!/usr/bin/env bash

set -euo pipefail

readonly git_author_name="USBIP Go Test"
readonly git_author_email="usbip-go-test@example.invalid"
readonly first_commit_date="2026-07-01T12:34:56Z"
readonly second_commit_date="2026-07-02T01:02:03Z"

git_bin=$(command -v git) || {
	printf '%s\n' 'version_lib_test: host Git is required by the local requires-git test target' >&2
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

assert_equal() {
	local want=$1
	local got=$2
	local context=$3

	if [[ "${got}" != "${want}" ]]; then
		printf '%s: want %q, got %q\n' "${context}" "${want}" "${got}" >&2
		exit 1
	fi
}

init_repo() {
	local repo=$1

	"${git_bin}" -C "${repo}" init -q
	"${git_bin}" -C "${repo}" config user.name "${git_author_name}"
	"${git_bin}" -C "${repo}" config user.email "${git_author_email}"
	"${git_bin}" -C "${repo}" config commit.gpgsign false
}

commit_file() {
	local repo=$1
	local file=$2
	local contents=$3
	local date=$4

	printf '%s\n' "${contents}" >"${repo}/${file}"
	"${git_bin}" -C "${repo}" add "${file}"
	GIT_AUTHOR_DATE="${date}" GIT_COMMITTER_DATE="${date}" \
		"${git_bin}" -C "${repo}" commit -qm "test: add ${file}"
}

version_in() {
	local repo=$1

	(
		cd "${repo}"
		export HARNESS_GIT="${git_bin}"
		# shellcheck source=tools/scripts/version_lib.sh
		source "$(script_path version_lib.sh)"
		harness_pep440_version
	)
}

tmp=${TEST_TMPDIR:-$(mktemp -d)}
tagged_repo="${tmp}/tagged"
fallback_repo="${tmp}/fallback"
mkdir -p "${tagged_repo}" "${fallback_repo}"

init_repo "${tagged_repo}"
commit_file "${tagged_repo}" first "first" "${first_commit_date}"
"${git_bin}" -C "${tagged_repo}" tag v1.2.3

assert_equal "1.2.3" "$(version_in "${tagged_repo}")" "exact canonical tag"

commit_file "${tagged_repo}" second "second" "${second_commit_date}"
tagged_sha=$("${git_bin}" -C "${tagged_repo}" rev-parse --short=7 HEAD)
"${git_bin}" -C "${tagged_repo}" tag v9.9.9-rc1
assert_equal \
	"1.2.3.dev1+g${tagged_sha}" \
	"$(version_in "${tagged_repo}")" \
	"development commit after canonical tag with a nearer prerelease tag"

printf '%s\n' "dirty" >"${tagged_repo}/untracked"
assert_equal \
	"1.2.3.dev1+g${tagged_sha}.dirty" \
	"$(version_in "${tagged_repo}")" \
	"dirty development tree"

init_repo "${fallback_repo}"
commit_file "${fallback_repo}" first "fallback" "${first_commit_date}"
"${git_bin}" -C "${fallback_repo}" tag v01.2.3
fallback_sha=$("${git_bin}" -C "${fallback_repo}" rev-parse --short=7 HEAD)
assert_equal \
	"0.0.0.dev1+g${fallback_sha}" \
	"$(version_in "${fallback_repo}")" \
	"repository without a canonical tag ignores a leading-zero tag"
