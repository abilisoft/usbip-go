#!/usr/bin/env bash

set -euo pipefail

split_words() {
	local value=${1:-}
	if [[ -z "${value}" ]]; then
		return 0
	fi

	# Repository Make variables pass simple Bazel flags without shell quoting.
	# shellcheck disable=SC2206
	local words=(${value})
	printf '%s\0' "${words[@]}"
}

read_words() {
	local -n out=$1
	local value=${2:-}
	out=()

	while IFS= read -r -d '' word; do
		out+=("${word}")
	done < <(split_words "${value}")
}

workspace=${BUILD_WORKSPACE_DIRECTORY:-${PWD}}

declare -a bazel_cmd=()
declare -a build_flags=()
declare -a test_flags=()
declare -a unit_test_flags=()
declare -a build_targets=()
declare -a test_targets=()
declare -a conformance_targets=()
declare -a coverage_targets=()

bazel_default="${workspace}/.local/tools/bin/bazelisk --output_user_root=${workspace}/.local/bazel"
read_words bazel_cmd "${BAZEL:-${bazel_default}}"
read_words build_flags "${BAZEL_BUILD_FLAGS:-}"
read_words test_flags "${BAZEL_TEST_FLAGS:-}"
read_words unit_test_flags "${BAZEL_UNIT_TEST_FLAGS:---test_tag_filters=-integration,-conformance,-mutation,-lint,-manual,-external}"
read_words build_targets "${BAZEL_BUILD_TARGETS:-//...}"
read_words test_targets "${BAZEL_TEST_TARGETS:-//:test}"
read_words conformance_targets "${BAZEL_CONFORMANCE_TEST_TARGETS:-//:conformance}"
read_words coverage_targets "${BAZEL_COVERAGE_TARGETS:-//:test}"

if ((${#bazel_cmd[@]} == 0)); then
	printf 'ci-local-runner: BAZEL command is empty\n' >&2
	exit 1
fi

run_step() {
	local name=$1
	shift

	printf '\n==> %s\n' "${name}"
	printf '+ '
	printf '%q ' "${bazel_cmd[@]}" "$@"
	printf '\n'
	"${bazel_cmd[@]}" "$@"
}

copy_coverage_report() {
	local output_path=$1
	local source="${output_path}/_coverage/_coverage_report.dat"
	local destination="${workspace}/build/coverage/coverage.lcov"

	mkdir -p "$(dirname "${destination}")"
	rm -f "${destination}"
	cp "${source}" "${destination}"
}

# Runs the repository-owned GitHub PR/push gates locally. GitHub-only services
# such as CodeQL, Trivy, Scorecard, Codecov upload, and SARIF upload remain in
# Actions; this runner covers the Make/Bazel commands those workflows own.
run_step 'Build' build "${build_flags[@]}" "${build_targets[@]}"
run_step 'Unit tests' test "${unit_test_flags[@]}" "${test_flags[@]}" "${test_targets[@]}"
run_step 'Race tests' test --config=race "${unit_test_flags[@]}" "${test_flags[@]}" "${test_targets[@]}"
run_step 'Conformance tests' test --config=conformance "${test_flags[@]}" "${conformance_targets[@]}"
run_step 'Strict lint suite' test "${test_flags[@]}" //:lint
run_step 'Vulnerability scan' test "${test_flags[@]}" //:govulncheck
run_step 'Coverage' coverage --combined_report=lcov "${unit_test_flags[@]}" "${test_flags[@]}" "${coverage_targets[@]}"
copy_coverage_report "$("${bazel_cmd[@]}" info output_path)"
run_step 'Coverage thresholds' run "${build_flags[@]}" //tools/scripts:coverage_check -- build/coverage/coverage.lcov .testcoverage.yaml
run_step 'Release stamping' test --config=release --workspace_status_command=tools/scripts/release_workspace_status_fixture.sh "${test_flags[@]}" //test/release:release_stamping_test
run_step 'GoReleaser config' run "${build_flags[@]}" //:release-check
