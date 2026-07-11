#!/usr/bin/env bash

set -euo pipefail

output="$(mktemp)"
trap 'rm -f "${output}"' EXIT

set +e
govulncheck "$@" >"${output}" 2>&1
status=$?
set -e

cat "${output}"

if ((status == 0 || status == 3)); then
	exit "${status}"
fi

if grep -Eiq 'vuln\.go\.dev|GOVULNDB|vulnerability database|fetch.*vulnerab|network is unreachable|no such host|temporary failure|connection refused|connection reset|i/o timeout|TLS handshake timeout|proxyconnect' "${output}"; then
	cat >&2 <<'EOFMSG'

govulncheck: unable to reach the Go vulnerability database.
This lint fails closed so it never passes with stale vulnerability data.
Check network/proxy access to https://vuln.go.dev, or set GOVULNDB / -db explicitly.
EOFMSG
fi

exit "${status}"
