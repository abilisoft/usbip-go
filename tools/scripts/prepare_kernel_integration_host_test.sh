#!/usr/bin/env bash

set -euo pipefail

readonly exit_failure=1
readonly exit_success=0

script_dir=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir

script_path() {
	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/prepare_kernel_integration_host.sh"
		return
	fi

	printf '%s\n' "${script_dir}/prepare_kernel_integration_host.sh"
}

readonly tmp=${TEST_TMPDIR:-$(mktemp -d)}
readonly fake_bin="${tmp}/bin"
readonly log_file="${tmp}/commands.log"
mkdir -p "${fake_bin}"

cat >"${fake_bin}/id" <<'FAKE'
#!/usr/bin/env bash
printf '%s\n' "${FAKE_UID:-0}"
FAKE

cat >"${fake_bin}/modprobe" <<'FAKE'
#!/usr/bin/env bash
printf 'modprobe %s\n' "$1" >>"${FAKE_LOG}"
if [[ "${FAKE_MODPROBE_FAIL:-}" == "$1" ]]; then
	exit 1
fi
if [[ "${FAKE_MODULE_DIR_MISSING:-}" != "$1" ]]; then
	mkdir -p "${SYS_MODULE_ROOT}/$1"
fi
FAKE

cat >"${fake_bin}/mountpoint" <<'FAKE'
#!/usr/bin/env bash
if [[ "${FAKE_CONFIGFS_MOUNTED:-}" == 'yes' ]]; then
	exit 0
fi
exit 1
FAKE

cat >"${fake_bin}/mount" <<'FAKE'
#!/usr/bin/env bash
printf 'mount %s\n' "$*" >>"${FAKE_LOG}"
mkdir -p "${GADGET_ROOT}"
FAKE

chmod +x "${fake_bin}"/*

new_case_root() {
	local name=$1
	local root="${tmp}/${name}"

	rm -rf "${root}"
	mkdir -p "${root}/sys/module" "${root}/sys/kernel/config/usb_gadget" \
		"${root}/sys/class/udc/dummy_udc.0" \
		"${root}/sys/class/udc/usbip-vudc.0"
	printf '%s\n' "${root}"
}

run_case() {
	local name=$1
	local expected_status=$2
	shift 2
	local root
	root=$(new_case_root "${name}")
	case "${CASE_LAYOUT:-complete}" in
	complete) ;;
	missing-gadget) rmdir "${root}/sys/kernel/config/usb_gadget" ;;
	missing-dummy-udc) rmdir "${root}/sys/class/udc/dummy_udc.0" ;;
	missing-vudc) rmdir "${root}/sys/class/udc/usbip-vudc.0" ;;
	*)
		printf 'unknown case layout: %s\n' "${CASE_LAYOUT}" >&2
		exit "${exit_failure}"
		;;
	esac
	: >"${log_file}"

	local status=${exit_success}
	env \
		PATH="${fake_bin}:${PATH}" \
		FAKE_LOG="${log_file}" \
		SYS_MODULE_ROOT="${root}/sys/module" \
		CONFIGFS_ROOT="${root}/sys/kernel/config" \
		GADGET_ROOT="${root}/sys/kernel/config/usb_gadget" \
		UDC_ROOT="${root}/sys/class/udc" \
		"$@" \
		"$(script_path)" >"${root}/stdout" 2>"${root}/stderr" || status=$?

	if [[ ${status} -ne ${expected_status} ]]; then
		printf '%s: expected status %d, got %d\n' \
			"${name}" "${expected_status}" "${status}" >&2
		cat "${root}/stderr" >&2
		exit "${exit_failure}"
	fi

	printf '%s\n' "${root}"
}

root=$(run_case loads_modules_and_mounts_configfs "${exit_success}" \
	FAKE_CONFIGFS_MOUNTED='no')
grep -Fxq 'mount -t configfs configfs '"${root}"'/sys/kernel/config' "${log_file}"
for module in cdc_acm dummy_hcd libcomposite sd_mod usb_f_acm \
	usb_f_mass_storage usb_storage usbip_core usbip_host usbip_vudc vhci_hcd; do
	grep -Fxq "modprobe ${module}" "${log_file}"
done

run_case uses_mounted_configfs "${exit_success}" \
	FAKE_CONFIGFS_MOUNTED='yes' >/dev/null
if grep -q '^mount ' "${log_file}"; then
	printf 'mounted case unexpectedly mounted configfs again\n' >&2
	exit "${exit_failure}"
fi

run_case rejects_non_root "${exit_failure}" FAKE_UID='1000' >/dev/null
run_case rejects_modprobe_failure "${exit_failure}" \
	FAKE_MODPROBE_FAIL='usbip_host' >/dev/null
run_case rejects_unloaded_module "${exit_failure}" \
	FAKE_MODULE_DIR_MISSING='vhci_hcd' >/dev/null

CASE_LAYOUT=missing-gadget run_case missing_gadget_root "${exit_failure}" \
	FAKE_CONFIGFS_MOUNTED='yes' >/dev/null

CASE_LAYOUT=missing-dummy-udc run_case missing_dummy_udc "${exit_failure}" \
	FAKE_CONFIGFS_MOUNTED='yes' >/dev/null

CASE_LAYOUT=missing-vudc run_case missing_vudc "${exit_failure}" \
	FAKE_CONFIGFS_MOUNTED='yes' >/dev/null
