#!/usr/bin/env bash

set -euo pipefail

output=${1:-}
shift || true
if [[ -z "${output}" ]]; then
	printf 'usage: govulncheck_sarif.sh OUTPUT [GOVULNCHECK_ARGS...]\n' >&2
	exit 2
fi

if [[ -n "${BUILD_WORKSPACE_DIRECTORY:-}" ]]; then
	case "${output}" in
	/*) ;;
	*) output="${BUILD_WORKSPACE_DIRECTORY}/${output}" ;;
	esac
fi

mkdir -p "$(dirname "${output}")"
tmp=$(mktemp)
trap 'rm -f "${tmp}"' EXIT

set +e
govulncheck -format=sarif "$@" >"${tmp}" 2>&1
status=$?
set -e

cat "${tmp}" >"${output}"
cat "${tmp}"

if ((status == 0 || status == 3)); then
	exit "${status}"
fi

if grep -Eiq 'vuln\.go\.dev|GOVULNDB|vulnerability database|fetch.*vulnerab|network is unreachable|no such host|temporary failure|connection refused|connection reset|i/o timeout|TLS handshake timeout|proxyconnect' "${tmp}"; then
	cat >&2 <<'EOFMSG'

govulncheck: unable to reach the Go vulnerability database.
This lint fails closed so it never passes with stale vulnerability data.
Check network/proxy access to https://vuln.go.dev, or set GOVULNDB / -db explicitly.
EOFMSG
fi

exit "${status}"
