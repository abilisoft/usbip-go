#!/usr/bin/env bash

set -euo pipefail

readonly expected_capability_line='CapabilityBoundingSet=CAP_SYS_ADMIN CAP_DAC_OVERRIDE CAP_CHOWN'
readonly exit_failure=1

service_path() {
	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/contrib/systemd/usbip-go.service"
		return
	fi

	local script_dir
	script_dir=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
	printf '%s\n' "${script_dir}/../../contrib/systemd/usbip-go.service"
}

if ! grep -Fxq "${expected_capability_line}" "$(service_path)"; then
	printf 'systemd service must retain CAP_CHOWN for --status-socket-group\n' >&2
	exit "${exit_failure}"
fi
