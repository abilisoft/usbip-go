#!/usr/bin/env bash

set -euo pipefail

script_path() {
	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/ci_local_runner.sh"
		return
	fi

	printf '%s\n' "$(dirname "${BASH_SOURCE[0]}")/ci_local_runner.sh"
}

tmp=${TEST_TMPDIR:-$(mktemp -d)}
fake_bazel="${tmp}/bazel"
log="${tmp}/bazel.log"

cat >"${fake_bazel}" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == "info output_path" ]]; then
	printf '%s\n' "${CI_LOCAL_TEST_OUTPUT_PATH}"
	exit 0
fi
printf '%s\n' "$*" >>"${CI_LOCAL_TEST_LOG}"
FAKE
chmod +x "${fake_bazel}"
: >"${log}"
mkdir -p "${tmp}/bazel-out/_coverage"
printf 'TN:\nSF:pkg/domain/busid.go\nLF:1\nLH:1\nend_of_record\n' >"${tmp}/bazel-out/_coverage/_coverage_report.dat"

CI_LOCAL_TEST_LOG="${log}" \
	CI_LOCAL_TEST_OUTPUT_PATH="${tmp}/bazel-out" \
	BUILD_WORKSPACE_DIRECTORY="${PWD}" \
	BAZEL="${fake_bazel}" \
	BAZEL_BUILD_FLAGS="--config=ci" \
	BAZEL_TEST_FLAGS="--test_output=errors" \
	BAZEL_UNIT_TEST_FLAGS="--test_tag_filters=-integration" \
	BAZEL_BUILD_TARGETS="//cmd/usbip-go:usbip-go" \
	BAZEL_CONFORMANCE_TEST_TARGETS="//test/conformance:conformance_test" \
	BAZEL_COVERAGE_TARGETS="//:test" \
	BAZEL_TEST_TARGETS="//cmd/usbip-go:usbip-go_test" \
	"$(script_path)" >/dev/null

cat >"${tmp}/want" <<'WANT'
build --config=ci //cmd/usbip-go:usbip-go
test --test_tag_filters=-integration --test_output=errors //cmd/usbip-go:usbip-go_test
test --config=race --test_tag_filters=-integration --test_output=errors //cmd/usbip-go:usbip-go_test
test --config=conformance --test_output=errors //test/conformance:conformance_test
test --test_output=errors //:lint
test --test_output=errors //:govulncheck
coverage --combined_report=lcov --test_tag_filters=-integration --test_output=errors //:test
run --config=ci //tools/scripts:coverage_check -- build/coverage/coverage.lcov .testcoverage.yaml
test --config=release --workspace_status_command=tools/scripts/release_workspace_status_fixture.sh --test_output=errors //test/release:release_stamping_test
run --config=ci //:release-check
WANT

if ! diff -u "${tmp}/want" "${log}"; then
	printf 'ci_local_runner did not execute the expected GitHub CI step commands\n' >&2
	exit 1
fi
