#!/usr/bin/env bash

set -euo pipefail

readonly exit_failure=1

script_dir=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir

script_path() {
	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/coverage_check.sh"
		return
	fi

	printf '%s\n' "${script_dir}/coverage_check.sh"
}

tmp=${TEST_TMPDIR:-$(mktemp -d)}
config="${tmp}/coverage.yaml"

cat >"${config}" <<'YAML'
threshold:
  package: 80
  total: 90
exclude:
  paths:
    - /excluded/
YAML

expect_failure() {
	local name=$1
	local report=$2

	if "$(script_path)" "${report}" "${config}" >"${tmp}/${name}.out" 2>"${tmp}/${name}.err"; then
		printf '%s: expected coverage checker failure\n' "${name}" >&2
		exit "${exit_failure}"
	fi

	grep -Fq 'no executable lines' "${tmp}/${name}.err"
	if grep -Fq '100.00% (0/0)' "${tmp}/${name}.out"; then
		printf '%s: missing coverage must not be reported as 100 percent\n' "${name}" >&2
		exit "${exit_failure}"
	fi
}

empty_report="${tmp}/empty.lcov"
: >"${empty_report}"
expect_failure empty "${empty_report}"

no_lines_report="${tmp}/no-lines.lcov"
cat >"${no_lines_report}" <<'LCOV'
TN:
SF:pkg/example/example.go
end_of_record
LCOV
expect_failure no-lines "${no_lines_report}"

zero_lines_report="${tmp}/zero-lines.lcov"
cat >"${zero_lines_report}" <<'LCOV'
TN:
SF:pkg/example/example.go
LF:0
LH:0
end_of_record
LCOV
expect_failure zero-lines "${zero_lines_report}"

excluded_report="${tmp}/excluded.lcov"
cat >"${excluded_report}" <<'LCOV'
TN:
SF:internal/excluded/generated.go
LF:10
LH:10
end_of_record
LCOV
expect_failure excluded-only "${excluded_report}"

valid_report="${tmp}/valid.lcov"
cat >"${valid_report}" <<'LCOV'
TN:
SF:pkg/example/example.go
LF:10
LH:9
end_of_record
LCOV

"$(script_path)" "${valid_report}" "${config}" >"${tmp}/valid.out"
grep -Fq 'coverage: total 90.00% (9/10), threshold 90.00%' "${tmp}/valid.out"

mixed_report="${tmp}/mixed.lcov"
cat >"${mixed_report}" <<'LCOV'
TN:
SF:pkg/empty/example.go
LF:0
LH:0
end_of_record
TN:
SF:pkg/measured/example.go
LF:10
LH:9
end_of_record
LCOV

"$(script_path)" "${mixed_report}" "${config}" >"${tmp}/mixed.out"
grep -Fq 'coverage: total 90.00% (9/10), threshold 90.00%' "${tmp}/mixed.out"
grep -Fq 'coverage: pkg/measured 90.00% (9/10), threshold 80.00%' "${tmp}/mixed.out"
grep -Fq 'coverage: pkg/empty not coverable (0 executable lines)' "${tmp}/mixed.out"
if grep -Fq '100.00% (0/0)' "${tmp}/mixed.out"; then
	printf '%s\n' 'mixed: zero-line packages must not invent a coverage percentage' >&2
	exit "${exit_failure}"
fi
