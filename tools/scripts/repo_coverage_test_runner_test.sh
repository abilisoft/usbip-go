#!/usr/bin/env bash

set -euo pipefail

readonly exit_failure=1
readonly git_author_email='repo-coverage@example.invalid'
readonly git_author_name='Repository Coverage Test'

script_dir=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir

runner_path() {
	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/repo_coverage_test_runner.sh"
		return
	fi

	printf '%s\n' "${script_dir}/repo_coverage_test_runner.sh"
}

tmp=${TEST_TMPDIR:-$(mktemp -d)}
checkout="${tmp}/checkout"
execroot="${tmp}/execroot"

mkdir -p "${checkout}" "${execroot}"
git -C "${checkout}" init -q
git -C "${checkout}" config user.name "${git_author_name}"
git -C "${checkout}" config user.email "${git_author_email}"
git -C "${checkout}" config commit.gpgsign false
printf '# Covered\n' >"${checkout}/README.md"
git -C "${checkout}" add README.md
git -C "${checkout}" commit -qm 'test: establish covered file'

ln -s "${checkout}/.git" "${execroot}/.git"
ln -s . "${execroot}/bazel-out"

if ! "$(runner_path)" \
	--workspace="${execroot}" \
	--covered=README.md \
	--include-suffix=.md; then
	printf 'repository coverage runner did not resolve the Bazel execroot back to the checkout\n' >&2
	exit "${exit_failure}"
fi
