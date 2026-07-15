#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 AbiliSoft
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

readonly gadget_root=/sys/kernel/config/usb_gadget
readonly gadget_name=usbip_go_release_real_busid
readonly gadget_dir=${gadget_root}/${gadget_name}
readonly vendor_id=1d6b
readonly product_id=1040

fail() {
	printf 'dummy USB gadget provisioning failed: %s\n' "$*" >&2
	exit 1
}

cleanup() {
	local cleanup_status=0
	local path

	[[ -d ${gadget_dir} ]] || return 0
	if ! printf '\n' >"${gadget_dir}/UDC"; then
		printf 'failed to unbind gadget UDC: %s\n' "${gadget_dir}/UDC" >&2
		cleanup_status=1
	fi
	if [[ -L ${gadget_dir}/configs/c.1/acm.usb0 ]] &&
		! rm "${gadget_dir}/configs/c.1/acm.usb0"; then
		printf 'failed to remove gadget function link\n' >&2
		cleanup_status=1
	fi
	for path in \
		"${gadget_dir}/functions/acm.usb0" \
		"${gadget_dir}/configs/c.1/strings/0x409" \
		"${gadget_dir}/configs/c.1" \
		"${gadget_dir}/strings/0x409" \
		"${gadget_dir}"; do
		[[ -d ${path} ]] || continue
		if ! rmdir "${path}"; then
			printf 'failed to remove gadget directory: %s\n' "${path}" >&2
			cleanup_status=1
		fi
	done
	[[ ! -e ${gadget_dir} ]] || cleanup_status=1

	return "${cleanup_status}"
}

find_busid() {
	local device
	local -a matches=()

	for device in /sys/bus/usb/devices/*-*; do
		[[ -f ${device}/idVendor && -f ${device}/idProduct ]] || continue
		[[ $(<"${device}/idVendor") == "${vendor_id}" ]] || continue
		[[ $(<"${device}/idProduct") == "${product_id}" ]] || continue
		matches+=("${device##*/}")
	done

	case ${#matches[@]} in
	0)
		return 1
		;;
	1)
		printf '%s\n' "${matches[0]}"
		return 0
		;;
	*)
		return 2
		;;
	esac
}

[[ $(id -u) -eq 0 ]] || fail 'must run as root'

case "${1:-create}" in
cleanup)
	cleanup || fail 'gadget teardown did not complete'
	exit 0
	;;
create)
	;;
*)
	fail "unknown action: ${1}"
	;;
esac

[[ -d /sys/class/udc/dummy_udc.1 ]] || fail 'dummy_udc.1 is unavailable'

cleanup || fail 'could not remove stale gadget state before creation'
mkdir -p "${gadget_dir}/strings/0x409"
printf '0x%s' "${vendor_id}" >"${gadget_dir}/idVendor"
printf '0x%s' "${product_id}" >"${gadget_dir}/idProduct"
printf 'usbip-go-release' >"${gadget_dir}/strings/0x409/serialnumber"
printf 'AbiliSoft' >"${gadget_dir}/strings/0x409/manufacturer"
printf 'USB-IP integration device' >"${gadget_dir}/strings/0x409/product"

mkdir -p "${gadget_dir}/configs/c.1/strings/0x409"
printf 'integration' >"${gadget_dir}/configs/c.1/strings/0x409/configuration"
mkdir -p "${gadget_dir}/functions/acm.usb0"
ln -s "${gadget_dir}/functions/acm.usb0" "${gadget_dir}/configs/c.1/acm.usb0"
printf 'dummy_udc.1' >"${gadget_dir}/UDC"

for _ in $(seq 1 100); do
	if busid=$(find_busid); then
		printf '%s\n' "${busid}"
		exit 0
	else
		find_status=$?
		if [[ ${find_status} -eq 2 ]]; then
			cleanup || fail 'ambiguous matching USB devices and gadget teardown failed'
			fail 'multiple USB devices match the gadget identity'
		fi
	fi
	sleep 0.05
done

cleanup || fail 'enumeration failed and gadget teardown did not complete'
fail 'dummy_hcd-backed device did not enumerate'
