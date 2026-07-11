#!/usr/bin/env bash

set -euo pipefail

script_path() {
	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/govulncheck_sarif.sh"
		return
	fi

	printf '%s\n' "$(dirname "${BASH_SOURCE[0]}")/govulncheck_sarif.sh"
}

readonly exit_scan_failed=1
readonly exit_success=0
readonly exit_vulnerabilities_found=3
readonly expected_driver_name='"name": "govulncheck"'
readonly expected_driver_name_count=1

tmp=${TEST_TMPDIR:-$(mktemp -d)}
fake_bin="${tmp}/bin"
mkdir -p "${fake_bin}"

cat >"${fake_bin}/govulncheck" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
cat "${GOVULNCHECK_FIXTURE}"
exit "${GOVULNCHECK_STATUS}"
FAKE
chmod +x "${fake_bin}/govulncheck"

cat >"${tmp}/missing-name.sarif" <<'SARIF'
{
  "version": "2.1.0",
  "runs": [{
    "tool": {
      "driver": {
        "rules": []
      }
    },
    "results": []
  }]
}
SARIF

output="${tmp}/normalized.sarif"
GOVULNCHECK_FIXTURE="${tmp}/missing-name.sarif" \
	GOVULNCHECK_STATUS="${exit_success}" \
	PATH="${fake_bin}:${PATH}" \
	"$(script_path)" "${output}" ./... >/dev/null

if [[ $(grep -Fc "${expected_driver_name}" "${output}") -ne ${expected_driver_name_count} ]]; then
	printf 'expected exactly one normalized SARIF driver name\n' >&2
	exit "${exit_scan_failed}"
fi

status=${exit_success}
GOVULNCHECK_FIXTURE="${output}" \
	GOVULNCHECK_STATUS="${exit_vulnerabilities_found}" \
	PATH="${fake_bin}:${PATH}" \
	"$(script_path)" "${tmp}/finding.sarif" ./... >/dev/null || status=$?
if [[ ${status} -ne ${exit_vulnerabilities_found} ]]; then
	printf 'expected vulnerability status %d, got %d\n' \
		"${exit_vulnerabilities_found}" "${status}" >&2
	exit "${exit_scan_failed}"
fi
if [[ $(grep -Fc "${expected_driver_name}" "${tmp}/finding.sarif") -ne ${expected_driver_name_count} ]]; then
	printf 'normalization duplicated the existing SARIF driver name\n' >&2
	exit "${exit_scan_failed}"
fi

printf 'network is unreachable\n' >"${tmp}/scan-error.txt"
: >"${tmp}/stale.sarif"
status=${exit_success}
GOVULNCHECK_FIXTURE="${tmp}/scan-error.txt" \
	GOVULNCHECK_STATUS="${exit_scan_failed}" \
	PATH="${fake_bin}:${PATH}" \
	"$(script_path)" "${tmp}/stale.sarif" ./... >/dev/null 2>&1 || status=$?
if [[ ${status} -ne ${exit_scan_failed} ]]; then
	printf 'expected scan failure status %d, got %d\n' \
		"${exit_scan_failed}" "${status}" >&2
	exit "${exit_scan_failed}"
fi
if [[ -e "${tmp}/stale.sarif" ]]; then
	printf 'failed scans must not leave an uploadable SARIF file\n' >&2
	exit "${exit_scan_failed}"
fi
