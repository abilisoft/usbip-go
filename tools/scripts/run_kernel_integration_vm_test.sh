#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir

script_path() {
	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/run_kernel_integration_vm.sh"
		return
	fi

	printf '%s\n' "${script_dir}/run_kernel_integration_vm.sh"
}

readonly tmp=${TEST_TMPDIR:-$(mktemp -d)}
readonly source_image="${tmp}/source.qcow2"
readonly cache_root="${tmp}/cache"

if ! grep --fixed-strings --line-regexp '  - build-essential' "$(script_path)" >/dev/null; then
	printf 'guest build toolchain package is absent\n' >&2
	exit 1
fi
if ! grep --fixed-strings --line-regexp "cpu_model='qemu64'" "$(script_path)" >/dev/null; then
	printf 'stable TCG CPU model is absent\n' >&2
	exit 1
fi

printf 'pinned image fixture\n' >"${source_image}"
checksum=$(sha512sum "${source_image}")
readonly checksum=${checksum%% *}

run_verify() {
	KERNEL_VM_IMAGE_URL="file://${source_image}" \
		KERNEL_VM_IMAGE_SHA512="${checksum}" \
		KERNEL_VM_CACHE_ROOT="${cache_root}" \
		KERNEL_VM_VERIFY_ONLY=1 \
		"$(script_path)"
}

run_verify
cached_image=$(find "${cache_root}/images" -type f -name '*.qcow2' -print -quit)
[[ -n "${cached_image}" ]]
first_mtime=$(stat -c %Y "${cached_image}")
sleep 1
run_verify
second_mtime=$(stat -c %Y "${cached_image}")
[[ "${first_mtime}" == "${second_mtime}" ]]

printf 'corrupt cache\n' >"${cached_image}"
run_verify
sha512sum --check <(printf '%s  %s\n' "${checksum}" "${cached_image}")

status=0
KERNEL_VM_IMAGE_URL="file://${source_image}" \
	KERNEL_VM_IMAGE_SHA512='invalid-checksum' \
	KERNEL_VM_CACHE_ROOT="${tmp}/bad-cache" \
	KERNEL_VM_VERIFY_ONLY=1 \
	"$(script_path)" >/dev/null 2>&1 || status=$?
if [[ ${status} -eq 0 ]]; then
	printf 'checksum mismatch unexpectedly succeeded\n' >&2
	exit 1
fi
