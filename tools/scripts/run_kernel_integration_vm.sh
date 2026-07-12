#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir
repo_root=$(cd -- "${script_dir}/../.." && pwd)
readonly repo_root

readonly image_release='20260706-2531'
readonly image_name="debian-13-generic-amd64-${image_release}.qcow2"
readonly default_image_url="https://cloud.debian.org/images/cloud/trixie/${image_release}/${image_name}"
readonly default_image_sha512='aca6eefc7b87faddad617b197fb621c44cc2c440f7097d78ac06e113f78177f6b7a1a39a581fbb24c2513354ab6938e63e78730259ce204b53452e8186f53a37'
readonly image_url="${KERNEL_VM_IMAGE_URL:-${default_image_url}}"
readonly image_sha512="${KERNEL_VM_IMAGE_SHA512:-${default_image_sha512}}"
readonly cache_root="${KERNEL_VM_CACHE_ROOT:-${repo_root}/.local/kernel-vm}"
readonly image_dir="${cache_root}/images"
readonly image_path="${image_dir}/${image_name}"
readonly ssh_port="${KERNEL_VM_SSH_PORT:-2222}"
readonly ssh_attempts=300
readonly ssh_retry_seconds=1
readonly vm_cpu_count=4
readonly vm_memory_mb=6144
readonly vm_disk_size='10G'

fail() {
	printf 'kernel integration VM failed: %s\n' "$*" >&2
	exit 1
}

require_cmd() {
	if ! command -v "$1" >/dev/null 2>&1; then
		fail "missing required command: $1"
	fi
}

verify_image() {
	local actual
	actual=$(sha512sum "${image_path}")
	actual=${actual%% *}
	[[ "${actual}" == "${image_sha512}" ]]
}

download_image() {
	require_cmd curl
	require_cmd sha512sum
	mkdir -p "${image_dir}"

	if [[ -f "${image_path}" ]] && verify_image; then
		printf 'using cached kernel integration image: %s\n' "${image_path}"
		return
	fi

	rm -f "${image_path}"
	printf 'downloading checksum-pinned kernel integration image: %s\n' "${image_url}"
	curl --fail --location --retry 3 --show-error \
		--output "${image_path}" "${image_url}"
	if ! verify_image; then
		rm -f "${image_path}"
		fail "SHA-512 mismatch for ${image_url}"
	fi
}

download_image
if [[ "${KERNEL_VM_VERIFY_ONLY:-}" == '1' ]]; then
	exit 0
fi

for command in cloud-localds git mktemp qemu-img qemu-system-x86_64 ssh ssh-keygen; do
	require_cmd "${command}"
done

mkdir -p "${cache_root}"
run_root=$(mktemp -d "${cache_root}/run.XXXXXX")
readonly run_root

readonly overlay_path="${run_root}/root.qcow2"
readonly seed_path="${run_root}/seed.img"
readonly ssh_key="${run_root}/id_ed25519"
readonly serial_log="${run_root}/serial.log"
readonly pid_file="${run_root}/qemu.pid"

ssh-keygen -q -t ed25519 -N '' -f "${ssh_key}"
ssh_public_key=$(<"${ssh_key}.pub")
readonly ssh_public_key

cat >"${run_root}/user-data" <<EOF
#cloud-config
users:
  - default
  - name: runner
    groups: [sudo]
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - ${ssh_public_key}
package_update: true
packages:
  - ca-certificates
  - curl
  - make
EOF

cat >"${run_root}/meta-data" <<'EOF'
instance-id: usbip-go-kernel-integration
local-hostname: usbip-go-kernel
EOF

cloud-localds "${seed_path}" "${run_root}/user-data" "${run_root}/meta-data"
qemu-img create -q -f qcow2 -F qcow2 -b "${image_path}" "${overlay_path}"
qemu-img resize -q "${overlay_path}" "${vm_disk_size}"

acceleration='tcg,thread=multi'
cpu_model='max'
if [[ -c /dev/kvm && -r /dev/kvm && -w /dev/kvm ]]; then
	acceleration='kvm'
	cpu_model='host'
fi
readonly acceleration cpu_model
printf 'starting kernel integration VM: acceleration=%s\n' "${acceleration}"

qemu-system-x86_64 \
	-accel "${acceleration}" \
	-cpu "${cpu_model}" \
	-smp "${vm_cpu_count}" \
	-m "${vm_memory_mb}" \
	-drive "if=virtio,format=qcow2,file=${overlay_path}" \
	-drive "if=virtio,format=raw,readonly=on,file=${seed_path}" \
	-netdev "user,id=net0,hostfwd=tcp:127.0.0.1:${ssh_port}-:22" \
	-device virtio-net-pci,netdev=net0 \
	-display none \
	-serial "file:${serial_log}" \
	-daemonize \
	-pidfile "${pid_file}"

cleanup() {
	if [[ -f "${pid_file}" ]]; then
		kill "$(<"${pid_file}")" >/dev/null 2>&1 || true
	fi
	rm -rf "${run_root}"
}
trap cleanup EXIT

readonly -a ssh_options=(
	-i "${ssh_key}"
	-p "${ssh_port}"
	-o BatchMode=yes
	-o ConnectTimeout=2
	-o StrictHostKeyChecking=no
	-o UserKnownHostsFile=/dev/null
)

ssh_ready=false
for ((attempt = 1; attempt <= ssh_attempts; attempt++)); do
	if ssh "${ssh_options[@]}" runner@127.0.0.1 true >/dev/null 2>&1; then
		ssh_ready=true
		break
	fi
	sleep "${ssh_retry_seconds}"
done
if [[ "${ssh_ready}" != 'true' ]]; then
	cat "${serial_log}" >&2
	fail "SSH did not become ready after ${ssh_attempts} attempts"
fi

ssh "${ssh_options[@]}" runner@127.0.0.1 'cloud-init status --wait'

git -C "${repo_root}" archive --format=tar HEAD |
	ssh "${ssh_options[@]}" runner@127.0.0.1 \
		'mkdir -p /home/runner/usbip-go && tar -C /home/runner/usbip-go -xf -'

ssh "${ssh_options[@]}" runner@127.0.0.1 \
	'sudo /home/runner/usbip-go/tools/scripts/prepare_kernel_integration_host.sh && sudo env NO_COLOR=1 make -C /home/runner/usbip-go test-integration'

ssh "${ssh_options[@]}" runner@127.0.0.1 'sudo poweroff' >/dev/null 2>&1 || true
printf 'kernel integration VM completed successfully\n'
