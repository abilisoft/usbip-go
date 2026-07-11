#!/usr/bin/env bash

set -euo pipefail

readonly exit_failure=1
readonly exit_success=0
readonly exit_usage=2
readonly exit_vulnerabilities_found=3
readonly sarif_driver_marker='"driver": {'
readonly sarif_driver_name='"name": "govulncheck"'

output=${1:-}
shift || true
if [[ -z "${output}" ]]; then
	printf 'usage: govulncheck_sarif.sh OUTPUT [GOVULNCHECK_ARGS...]\n' >&2
	exit "${exit_usage}"
fi

if [[ -n "${BUILD_WORKSPACE_DIRECTORY:-}" ]]; then
	case "${output}" in
	/*) ;;
	*) output="${BUILD_WORKSPACE_DIRECTORY}/${output}" ;;
	esac
fi

mkdir -p "$(dirname "${output}")"
rm -f "${output}"
raw_sarif=$(mktemp)
normalized_sarif=$(mktemp)
trap 'rm -f "${raw_sarif}" "${normalized_sarif}"' EXIT

set +e
govulncheck -format=sarif "$@" >"${raw_sarif}" 2>&1
status=$?
set -e

if ((status == exit_success || status == exit_vulnerabilities_found)); then
	if grep -Fq "${sarif_driver_name}" "${raw_sarif}"; then
		cat "${raw_sarif}" >"${normalized_sarif}"
	elif grep -Fq "${sarif_driver_marker}" "${raw_sarif}"; then
		awk \
			-v failure="${exit_failure}" \
			-v marker="${sarif_driver_marker}" \
			-v property="${sarif_driver_name}" \
			'!inserted && index($0, marker) {
				print
				match($0, /^[[:space:]]*/)
				printf "%s  %s,\n", substr($0, 1, RLENGTH), property
				inserted = 1
				next
			}
			{ print }
			END { if (!inserted) exit failure }' \
			"${raw_sarif}" >"${normalized_sarif}"
	else
		printf 'govulncheck: SARIF output is missing tool.driver\n' >&2
		cat "${raw_sarif}" >&2
		exit "${exit_failure}"
	fi

	cat "${normalized_sarif}" >"${output}"
	cat "${normalized_sarif}"
	exit "${status}"
fi

cat "${raw_sarif}"

if grep -Eiq 'vuln\.go\.dev|GOVULNDB|vulnerability database|fetch.*vulnerab|network is unreachable|no such host|temporary failure|connection refused|connection reset|i/o timeout|TLS handshake timeout|proxyconnect' "${raw_sarif}"; then
	cat >&2 <<'EOFMSG'

govulncheck: unable to reach the Go vulnerability database.
This lint fails closed so it never passes with stale vulnerability data.
Check network/proxy access to https://vuln.go.dev, or set GOVULNDB / -db explicitly.
EOFMSG
fi

exit "${status}"
