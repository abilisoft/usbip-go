#!/usr/bin/env bash

set -euo pipefail

readonly sys_module_root="${SYS_MODULE_ROOT:-/sys/module}"
readonly configfs_root="${CONFIGFS_ROOT:-/sys/kernel/config}"
readonly gadget_root="${GADGET_ROOT:-${configfs_root}/usb_gadget}"
readonly udc_root="${UDC_ROOT:-/sys/class/udc}"

readonly -a required_modules=(
	dummy_hcd
	libcomposite
	usb_f_acm
	usb_f_mass_storage
	usbip_core
	usbip_host
	usbip_vudc
	vhci_hcd
)

fail() {
	printf 'kernel integration host preparation failed: %s\n' "$*" >&2
	exit 1
}

if [[ "$(id -u)" -ne 0 ]]; then
	fail 'must run as root'
fi

for module in "${required_modules[@]}"; do
	modprobe "${module}"
done

mkdir -p "${configfs_root}"
if ! mountpoint --quiet "${configfs_root}"; then
	mount -t configfs configfs "${configfs_root}"
fi

for module in "${required_modules[@]}"; do
	if [[ ! -d "${sys_module_root}/${module}" ]]; then
		fail "required kernel module is not loaded: ${module}"
	fi
done

if [[ ! -d "${gadget_root}" ]]; then
	fail "configfs gadget root is absent: ${gadget_root}"
fi
if [[ ! -w "${gadget_root}" ]]; then
	fail "configfs gadget root is not writable: ${gadget_root}"
fi

if ! compgen -G "${udc_root}/dummy_udc.*" >/dev/null; then
	fail "dummy_hcd UDC is absent under ${udc_root}"
fi
if ! compgen -G "${udc_root}/usbip-vudc.*" >/dev/null; then
	fail "usbip_vudc UDC is absent under ${udc_root}"
fi

printf 'kernel integration host ready: kernel=%s\n' "$(uname -r)"
