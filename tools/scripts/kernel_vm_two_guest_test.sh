#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 AbiliSoft
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

readonly image_release=20260706-2531
readonly image_name=debian-13-generic-amd64-${image_release}.qcow2
readonly image_url=https://cloud.debian.org/images/cloud/trixie/${image_release}/${image_name}
readonly image_sha512=aca6eefc7b87faddad617b197fb621c44cc2c440f7097d78ac06e113f78177f6b7a1a39a581fbb24c2513354ab6938e63e78730259ce204b53452e8186f53a37
readonly cache_root=${KERNEL_VM_CACHE_ROOT:-}
readonly workspace_root=${KERNEL_VM_WORKSPACE_ROOT:-}
readonly image_dir=${cache_root}/images
readonly image_path=${image_dir}/${image_name}
readonly run_parent=${cache_root}/runs
readonly log_parent=${TEST_UNDECLARED_OUTPUTS_DIR:-${cache_root}/logs}
readonly exporter_ssh_port=${KERNEL_VM_EXPORTER_SSH_PORT:-2222}
readonly importer_ssh_port=${KERNEL_VM_IMPORTER_SSH_PORT:-2223}
readonly interguest_stream_port=${KERNEL_VM_INTERGUEST_STREAM_PORT:-33240}
readonly exporter_interguest_address=192.0.2.1
readonly importer_interguest_address=192.0.2.2
readonly interguest_prefix_length=30
readonly exporter_interguest_mac=52:54:00:12:34:01
readonly importer_interguest_mac=52:54:00:12:34:02
readonly guest_remote=${exporter_interguest_address}:3240
readonly cycle_count=${KERNEL_VM_CYCLE_COUNT:-3}
readonly network_delay_ms=${KERNEL_VM_NETWORK_DELAY_MS:-25}
readonly vm_cpu_count=${KERNEL_VM_CPU_COUNT:-1}
readonly vm_memory_mb=${KERNEL_VM_MEMORY_MB:-1024}
readonly vm_disk_size=6G
readonly status_socket=/run/usbip-go/status.sock
readonly ssh_command_timeout=120
readonly cloud_init_timeout=600
readonly image_download_timeout=600
readonly retained_log_bytes=4194304
readonly diagnostic_line_limit=3000

fail() {
	printf 'two-VM USB/IP validation failed: %s\n' "$*" >&2
	exit 1
}

resolve_runfile() {
	local logical_path=$1
	local candidate
	local workspace=${TEST_WORKSPACE:-_main}

	for candidate in \
		"${RUNFILES_DIR:-}/${workspace}/${logical_path}" \
		"${TEST_SRCDIR:-}/${workspace}/${logical_path}" \
		"${RUNFILES_DIR:-}/_main/${logical_path}" \
		"${TEST_SRCDIR:-}/_main/${logical_path}"; do
		if [[ ${candidate} != /*/* && ${candidate} == /* ]]; then
			continue
		fi
		if [[ -e ${candidate} ]]; then
			printf '%s\n' "${candidate}"
			return 0
		fi
	done

	if [[ -n ${RUNFILES_MANIFEST_FILE:-} && -f ${RUNFILES_MANIFEST_FILE} ]]; then
		candidate=$(awk -v key="${workspace}/${logical_path}" '$1 == key { print substr($0, length($1) + 2); exit }' "${RUNFILES_MANIFEST_FILE}")
		if [[ -n ${candidate} && -e ${candidate} ]]; then
			printf '%s\n' "${candidate}"
			return 0
		fi
	fi

	fail "Bazel runfile is missing: ${logical_path}"
}

test_lib=$(resolve_runfile tools/scripts/kernel_vm_test_lib.sh)
readonly test_lib
# shellcheck source=tools/scripts/kernel_vm_test_lib.sh
source "${test_lib}"

for command in awk cloud-localds cmp curl date find grep qemu-img qemu-system-x86_64 \
	realpath scp seq sha512sum ssh ssh-keygen tail timeout wc; do
	command -v "${command}" >/dev/null 2>&1 || fail "missing required command: ${command}"
done

kernel_vm_validate_config \
	"${cycle_count}" "${network_delay_ms}" "${vm_cpu_count}" "${vm_memory_mb}" ||
	fail 'invalid kernel VM runner configuration'
kernel_vm_validate_cache_root "${cache_root}" "${workspace_root}" ||
	fail 'invalid kernel VM cache configuration'
kernel_vm_validate_ports \
	"${exporter_ssh_port}" "${importer_ssh_port}" "${interguest_stream_port}" ||
	fail 'invalid kernel VM port configuration'
[[ -c /dev/kvm && -r /dev/kvm && -w /dev/kvm ]] || fail '/dev/kvm is not usable'

usbip_binary=$(resolve_runfile cmd/usbip-go/usbip-go_/usbip-go)
readonly usbip_binary
provision_gadget_script=$(resolve_runfile tools/scripts/kernel_vm_provision_gadget.sh)
readonly provision_gadget_script
[[ -x ${usbip_binary} ]] || fail "Bazel usbip-go binary is not executable: ${usbip_binary}"
[[ -f ${provision_gadget_script} ]] || fail "gadget provisioner is missing: ${provision_gadget_script}"

mkdir -p "${image_dir}" "${run_parent}" "${log_parent}"

verify_image() {
	kernel_vm_sha512_matches "${image_path}" "${image_sha512}"
}

if [[ ! -f ${image_path} ]] || ! verify_image; then
	rm -f "${image_path}"
	printf 'downloading checksum-pinned Debian image: %s\n' "${image_url}"
	curl --connect-timeout 15 --fail --location --max-time "${image_download_timeout}" \
		--retry 3 --show-error --output "${image_path}" "${image_url}"
	verify_image || {
		rm -f "${image_path}"
		fail 'Debian image SHA-512 mismatch'
	}
fi

run_id=$(date -u +%Y%m%dT%H%M%SZ)-$$-two-vm
readonly run_id
run_root=${run_parent}/${run_id}
log_root=${log_parent}/${run_id}
readonly run_root log_root
mkdir "${run_root}" "${log_root}"
mkdir "${run_root}/exporter" "${run_root}/importer"

readonly ssh_key=${run_root}/id_ed25519
status=1
busid=''
port_id=''
gadget_created=false
device_bound=false
serve_started=false
port_attached=false
completed_cycles=0

role_port() {
	case "$1" in
	exporter)
		printf '%s\n' "${exporter_ssh_port}"
		;;
	importer)
		printf '%s\n' "${importer_ssh_port}"
		;;
	*)
		fail "unknown VM role: $1"
		;;
	esac
}

role_pid_file() {
	printf '%s/%s/qemu.pid\n' "${run_root}" "$1"
}

role_running() {
	local pid pid_file role_root

	pid_file=$(role_pid_file "$1")
	role_root=${run_root}/$1
	[[ -f ${pid_file} ]] || return 1
	pid=$(<"${pid_file}")
	[[ ${pid} =~ ^[0-9]+$ ]] || return 1
	kill -0 "${pid}" >/dev/null 2>&1 || return 1
	kernel_vm_pid_cmdline_matches_overlay \
		"/proc/${pid}/cmdline" "${role_root}/root.qcow2"
}

ssh_role_command() {
	local role=$1
	local timeout_seconds=$2
	local stdin_policy=$3
	local port
	local -a stdin_options=()

	shift 3
	case "${stdin_policy}" in
	discard)
		stdin_options=(-n)
		;;
	forward) ;;
	*)
		fail "unknown SSH stdin policy: ${stdin_policy}"
		;;
	esac
	port=$(role_port "${role}")
	timeout --signal=TERM --kill-after=5 "${timeout_seconds}" ssh \
		"${stdin_options[@]}" \
		-i "${ssh_key}" \
		-p "${port}" \
		-o BatchMode=yes \
		-o ConnectTimeout=2 \
		-o LogLevel=ERROR \
		-o StrictHostKeyChecking=no \
		-o UserKnownHostsFile=/dev/null \
		runner@127.0.0.1 "$@"
}

ssh_role_with_timeout() {
	local role=$1
	local timeout_seconds=$2

	shift 2
	ssh_role_command "${role}" "${timeout_seconds}" discard "$@"
}

ssh_role() {
	local role=$1

	shift
	ssh_role_with_timeout "${role}" "${ssh_command_timeout}" "$@"
}

ssh_role_with_stdin() {
	local role=$1

	shift
	ssh_role_command "${role}" "${ssh_command_timeout}" forward "$@"
}

scp_to_role() {
	local role=$1
	local source=$2
	local destination=$3
	local port

	port=$(role_port "${role}")
	timeout --signal=TERM --kill-after=5 "${ssh_command_timeout}" scp \
		-i "${ssh_key}" \
		-P "${port}" \
		-o BatchMode=yes \
		-o ConnectTimeout=2 \
		-o LogLevel=ERROR \
		-o StrictHostKeyChecking=no \
		-o UserKnownHostsFile=/dev/null \
		"${source}" "runner@127.0.0.1:${destination}"
}

capture_role_diagnostics_best_effort() {
	local role=$1
	local destination=${log_root}/${role}-diagnostics.log

	role_running "${role}" || return 0
	{
		printf '%s\n' '== kernel =='
		ssh_role "${role}" \
			"uname -a; sudo dmesg --color=never | tail -n ${diagnostic_line_limit}" || true
		printf '%s\n' '== journal =='
		ssh_role "${role}" \
			"sudo journalctl -b --no-pager -n ${diagnostic_line_limit}" || true
		printf '%s\n' '== modules and processes =='
		ssh_role "${role}" \
			'sudo sh -c '\''lsmod; ps auxww; find /sys/module -maxdepth 1 -type l -printf "%f\n" | sort'\''' || true
		if [[ ${role} == importer ]]; then
			printf '%s\n' '== usbip-go port =='
			ssh_role importer 'sudo /usr/local/bin/usbip-go --output=json port' || true
		else
			printf '%s\n' '== usbip-go local list =='
			ssh_role exporter 'sudo /usr/local/bin/usbip-go --output=json list' || true
		fi
		printf '%s\n' '== USB devices =='
		ssh_role "${role}" \
			"sudo sh -c 'for p in /sys/bus/usb/devices/*-*; do [ -f \"\${p}/idVendor\" ] || continue; printf \"%s %s:%s driver=%s\\n\" \"\${p##*/}\" \"\$(cat \"\${p}/idVendor\")\" \"\$(cat \"\${p}/idProduct\")\" \"\$(basename \"\$(readlink -f \"\${p}/driver\" 2>/dev/null)\" 2>/dev/null || true)\"; done'" || true
		if [[ ${role} == exporter ]]; then
			printf '%s\n' '== exporter server =='
			ssh_role exporter \
				"sudo sh -c 'cat /run/usbip-go-serve.pid 2>/dev/null || true; tail -n ${diagnostic_line_limit} /var/log/usbip-go-serve.log 2>/dev/null || true'" || true
		else
			printf '%s\n' '== importer reader =='
			ssh_role importer \
				'sudo sh -c '\''ls -l /dev/ttyACM* /run/usbip-go-reader.* /run/usbip-go-payload-* 2>/dev/null || true; cat /run/usbip-go-reader.err /run/usbip-go-reader-supervisor.log /run/usbip-go-remote-list.err /run/usbip-go-attach.err /run/usbip-go-port.err 2>/dev/null || true'\''' || true
		fi
	} >"${destination}" 2>&1
}

capture_role_diagnostics_strict() {
	local role=$1
	local destination=${log_root}/${role}-diagnostics.log
	local kernel_version_path=${log_root}/${role}-kernel-version-evidence.log
	local kernel_log_path=${log_root}/${role}-kernel-log-evidence.log
	local journal_path=${log_root}/${role}-journal-evidence.log
	local system_state_path=${log_root}/${role}-system-state-evidence.log
	local role_state_path=${log_root}/${role}-role-state-evidence.json

	role_running "${role}" || {
		printf 'required diagnostics unavailable because %s VM is not running\n' \
			"${role}" >&2
		return 1
	}

	ssh_role "${role}" 'uname -a' >"${kernel_version_path}" 2>&1 || {
		printf 'failed to capture required %s kernel version evidence\n' "${role}" >&2
		return 1
	}
	ssh_role "${role}" \
		"sudo bash -o pipefail -c 'dmesg --color=never | tail -n ${diagnostic_line_limit}'" \
		>"${kernel_log_path}" 2>&1 || {
		printf 'failed to capture required %s kernel log evidence\n' "${role}" >&2
		return 1
	}
	ssh_role "${role}" \
		"sudo journalctl -b --no-pager -n ${diagnostic_line_limit}" \
		>"${journal_path}" 2>&1 || {
		printf 'failed to capture required %s journal evidence\n' "${role}" >&2
		return 1
	}
	ssh_role "${role}" \
		'sudo bash -euo pipefail -c '\''lsmod; ps auxww; find /sys/module -maxdepth 1 -type l -printf "%f\n" | sort'\''' \
		>"${system_state_path}" 2>&1 || {
		printf 'failed to capture required %s system state evidence\n' "${role}" >&2
		return 1
	}

	case "${role}" in
	importer)
		ssh_role importer 'sudo /usr/local/bin/usbip-go --output=json port' \
			>"${role_state_path}" 2>&1 || {
			printf 'failed to capture required importer Port state evidence\n' >&2
			return 1
		}
		;;
	exporter)
		ssh_role exporter 'sudo /usr/local/bin/usbip-go --output=json list' \
			>"${role_state_path}" 2>&1 || {
			printf 'failed to capture required exporter device state evidence\n' >&2
			return 1
		}
		;;
	*)
		printf 'unknown VM role for required diagnostics: %s\n' "${role}" >&2
		return 1
		;;
	esac

	kernel_vm_require_nonempty_evidence "${role}" \
		"${kernel_version_path}" \
		"${kernel_log_path}" \
		"${journal_path}" \
		"${system_state_path}" \
		"${role_state_path}" || return 1

	{
		printf '%s\n' '== kernel version =='
		cat "${kernel_version_path}"
		printf '%s\n' '== kernel log =='
		cat "${kernel_log_path}"
		printf '%s\n' '== journal =='
		cat "${journal_path}"
		printf '%s\n' '== modules and processes =='
		cat "${system_state_path}"
		printf '%s\n' '== role state =='
		cat "${role_state_path}"
		printf '%s\n' '== USB devices =='
		ssh_role "${role}" \
			"sudo sh -c 'for p in /sys/bus/usb/devices/*-*; do [ -f \"\${p}/idVendor\" ] || continue; printf \"%s %s:%s driver=%s\\n\" \"\${p##*/}\" \"\$(cat \"\${p}/idVendor\")\" \"\$(cat \"\${p}/idProduct\")\" \"\$(basename \"\$(readlink -f \"\${p}/driver\" 2>/dev/null)\" 2>/dev/null || true)\"; done'" || true
		if [[ ${role} == exporter ]]; then
			printf '%s\n' '== exporter server =='
			ssh_role exporter \
				"sudo sh -c 'cat /run/usbip-go-serve.pid 2>/dev/null || true; tail -n ${diagnostic_line_limit} /var/log/usbip-go-serve.log 2>/dev/null || true'" || true
		else
			printf '%s\n' '== importer reader =='
			ssh_role importer \
				'sudo sh -c '\''ls -l /dev/ttyACM* /run/usbip-go-reader.* /run/usbip-go-payload-* 2>/dev/null || true; cat /run/usbip-go-reader.err /run/usbip-go-reader-supervisor.log /run/usbip-go-remote-list.err /run/usbip-go-attach.err /run/usbip-go-port.err 2>/dev/null || true'\''' || true
		fi
	} >"${destination}" 2>&1

	kernel_vm_require_nonempty_evidence "${role} combined diagnostics" "${destination}"
}

bound_retained_logs() {
	local bound_status=0
	local log_path
	local -a log_files=()

	[[ -d ${log_root} ]] || return 1
	mapfile -d '' -t log_files < <(find "${log_root}" -type f -print0)
	for log_path in "${log_files[@]}"; do
		kernel_vm_bound_log_file "${log_path}" "${retained_log_bytes}" ||
			bound_status=1
	done

	return "${bound_status}"
}

cleanup_guest_step() {
	local step=$1
	local role
	local step_status=0

	case "${step}" in
	detach)
		[[ ${port_attached} == true ]] || return 0
		[[ -n ${port_id} ]] || {
			printf 'attached importer state has no exact Port ID\n' \
				>>"${log_root}/cleanup.log"
			return 1
		}
		role_running importer || {
			printf 'cannot detach Port %s because importer VM is not running\n' \
				"${port_id}" >>"${log_root}/cleanup.log"
			return 1
		}
		if ssh_role importer \
			"sudo timeout 15 /usr/local/bin/usbip-go --output=json detach '${port_id}'" \
			>>"${log_root}/cleanup.log" 2>&1; then
			port_attached=false
			port_id=''
			return 0
		fi
		return 1
		;;
	readers)
		for role in importer exporter; do
			role_running "${role}" || continue
			ssh_role "${role}" \
				'sudo sh -c '\''if [ -x /usr/local/sbin/stop-usbip-go-reader.sh ]; then /usr/local/sbin/stop-usbip-go-reader.sh; fi'\''' \
				>>"${log_root}/cleanup.log" 2>&1 || step_status=1
		done
		return "${step_status}"
		;;
	server)
		[[ ${serve_started} == true ]] || return 0
		role_running exporter || {
			printf 'cannot stop exporter server because exporter VM is not running\n' \
				>>"${log_root}/cleanup.log"
			return 1
		}
		if ssh_role exporter 'sudo /usr/local/sbin/stop-usbip-go-server.sh' \
			>>"${log_root}/cleanup.log" 2>&1; then
			serve_started=false
			return 0
		fi
		return 1
		;;
	unbind)
		[[ ${device_bound} == true ]] || return 0
		[[ ${serve_started} == false ]] || {
			printf 'refusing to unbind while exporter server ownership remains active\n' \
				>>"${log_root}/cleanup.log"
			return 1
		}
		[[ -n ${busid} ]] || {
			printf 'bound exporter state has no exact bus ID\n' \
				>>"${log_root}/cleanup.log"
			return 1
		}
		role_running exporter || {
			printf 'cannot unbind %s because exporter VM is not running\n' \
				"${busid}" >>"${log_root}/cleanup.log"
			return 1
		}
		if ssh_role exporter \
			"sudo timeout 15 /usr/local/bin/usbip-go --output=json unbind '${busid}'" \
			>>"${log_root}/cleanup.log" 2>&1; then
			device_bound=false
			return 0
		fi
		return 1
		;;
	gadget)
		[[ ${gadget_created} == true ]] || return 0
		[[ ${serve_started} == false && ${device_bound} == false ]] || {
			printf 'refusing gadget removal while server or bind ownership remains active\n' \
				>>"${log_root}/cleanup.log"
			return 1
		}
		role_running exporter || {
			printf 'cannot remove gadget because exporter VM is not running\n' \
				>>"${log_root}/cleanup.log"
			return 1
		}
		if ssh_role exporter 'sudo /usr/local/sbin/provision-real-busid.sh cleanup' \
			>>"${log_root}/cleanup.log" 2>&1; then
			gadget_created=false
			return 0
		fi
		return 1
		;;
	*)
		printf 'unknown cleanup step: %s\n' "${step}" >>"${log_root}/cleanup.log"
		return 1
		;;
	esac
}

cleanup_guest_state() {
	kernel_vm_run_cleanup_plan cleanup_guest_step
}

stop_role() {
	local role=$1
	local pid_file pid

	pid_file=$(role_pid_file "${role}")
	[[ -f ${pid_file} ]] || return 0
	if ! role_running "${role}"; then
		rm -f "${pid_file}"
		return 0
	fi
	pid=$(<"${pid_file}")
	kill "${pid}" >/dev/null 2>&1 || return 1
	for _ in $(seq 1 50); do
		if ! role_running "${role}"; then
			rm -f "${pid_file}"
			return 0
		fi
		sleep 0.1
	done
	kill -KILL "${pid}" >/dev/null 2>&1 || return 1
	for _ in $(seq 1 50); do
		if ! role_running "${role}"; then
			rm -f "${pid_file}"
			return 0
		fi
		sleep 0.1
	done
	return 1
}

cleanup() {
	local cleanup_status=0
	local role
	local all_roles_stopped=true

	set +e
	if [[ ${status} -ne 0 ]]; then
		capture_role_diagnostics_best_effort exporter || cleanup_status=1
		capture_role_diagnostics_best_effort importer || cleanup_status=1
	fi
	cleanup_guest_state || cleanup_status=1
	for role in importer exporter; do
		if ! stop_role "${role}"; then
			cleanup_status=1
			all_roles_stopped=false
			continue
		fi
		if role_running "${role}"; then
			printf '%s VM still owns its overlay after shutdown\n' "${role}" \
				>>"${log_root}/cleanup.log"
			cleanup_status=1
			all_roles_stopped=false
		fi
	done
	kernel_vm_remove_run_root_if_roles_stopped \
		"${run_root}" "${run_parent}" "${all_roles_stopped}" || cleanup_status=1
	bound_retained_logs || cleanup_status=1
	if [[ ${status} -ne 0 || ${cleanup_status} -ne 0 ]]; then
		printf 'failure logs: %s\n' "${log_root}" >&2
	fi
	if [[ ${status} -eq 0 && ${cleanup_status} -ne 0 ]]; then
		exit 1
	fi
}
trap cleanup EXIT

ssh-keygen -q -t ed25519 -N '' -f "${ssh_key}"
ssh_public_key=$(<"${ssh_key}.pub")
readonly ssh_public_key

write_cloud_init() {
	local role=$1
	local role_root=${run_root}/${role}

	cat >"${role_root}/user-data" <<EOF
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
  - iproute2
  - jq
  - kmod
EOF

	cat >"${role_root}/meta-data" <<EOF
instance-id: usbip-go-two-vm-${role}
local-hostname: usbip-go-${role}
EOF
}

write_role_scripts() {
	cat >"${run_root}/prepare-exporter.sh" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf 'exporter preparation failed: %s\n' "$*" >&2
	exit 1
}

[[ $(id -u) -eq 0 ]] || fail 'must run as root'
modprobe dummy_hcd num=2
for module in cdc_acm libcomposite usb_f_acm usbip_core usbip_host; do
	modprobe "${module}"
done
mkdir -p /sys/kernel/config
if ! mountpoint --quiet /sys/kernel/config; then
	mount -t configfs configfs /sys/kernel/config
fi
for module in dummy_hcd cdc_acm libcomposite usb_f_acm usbip_core usbip_host; do
	[[ -d /sys/module/${module} ]] || fail "required module is not loaded: ${module}"
done
[[ -d /sys/kernel/config/usb_gadget ]] || fail 'configfs gadget root is absent'
[[ -w /sys/kernel/config/usb_gadget ]] || fail 'configfs gadget root is not writable'
[[ -d /sys/class/udc/dummy_udc.1 ]] || fail 'dummy_udc.1 is unavailable'
[[ -w /sys/bus/usb/drivers/usbip-host/bind ]] || fail 'usbip-host bind is not writable'
[[ -w /sys/bus/usb/drivers/usbip-host/unbind ]] || fail 'usbip-host unbind is not writable'
printf 'exporter ready: kernel=%s\n' "$(uname -r)"
EOF

	cat >"${run_root}/prepare-importer.sh" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

fail() {
	printf 'importer preparation failed: %s\n' "$*" >&2
	exit 1
}

[[ $(id -u) -eq 0 ]] || fail 'must run as root'
for module in cdc_acm usbip_core vhci_hcd; do
	modprobe "${module}"
done
for module in cdc_acm usbip_core vhci_hcd; do
	[[ -d /sys/module/${module} ]] || fail "required module is not loaded: ${module}"
done
[[ -w /sys/devices/platform/vhci_hcd.0/attach ]] || fail 'vhci_hcd attach is not writable'
[[ -w /sys/devices/platform/vhci_hcd.0/detach ]] || fail 'vhci_hcd detach is not writable'
printf 'importer ready: kernel=%s\n' "$(uname -r)"
EOF

	cat >"${run_root}/reader-supervisor.sh" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

tty=$1
count=$2
output=$3
readonly tty count output

rm -f "${output}" /run/usbip-go-reader.pid /run/usbip-go-reader.status /run/usbip-go-reader.err
printf '%s\n' "$$" >/run/usbip-go-reader-supervisor.pid
reader_pid=''
cleanup_reader() {
	if [[ ${reader_pid} =~ ^[0-9]+$ ]] && kill -0 "${reader_pid}" >/dev/null 2>&1; then
		kill -TERM "${reader_pid}" >/dev/null 2>&1 || true
		wait "${reader_pid}" 2>/dev/null || true
	fi
	rm -f /run/usbip-go-reader-supervisor.pid /run/usbip-go-reader.pid
}
trap cleanup_reader EXIT
trap 'exit 143' INT TERM
dd if="${tty}" of="${output}" bs=1 count="${count}" iflag=fullblock status=none \
	</dev/null >/dev/null 2>/run/usbip-go-reader.err &
reader_pid=$!
printf '%s\n' "${reader_pid}" >/run/usbip-go-reader.pid
set +e
wait "${reader_pid}"
reader_status=$?
reader_pid=''
set -e
printf '%s\n' "${reader_status}" >/run/usbip-go-reader.status
exit "${reader_status}"
EOF

	cat >"${run_root}/stop-usbip-go-reader.sh" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

stop_owned_process() {
	local pid_file=$1
	local marker=$2
	local pid

	[[ -f ${pid_file} ]] || return 0
	pid=$(<"${pid_file}")
	if [[ ! ${pid} =~ ^[0-9]+$ ]] || ! kill -0 "${pid}" >/dev/null 2>&1; then
		rm -f "${pid_file}"
		return 0
	fi
	if ! grep -a -E -q -- "${marker}" "/proc/${pid}/cmdline"; then
		printf 'refusing to signal unowned reader pid %s from %s\n' \
			"${pid}" "${pid_file}" >&2
		return 1
	fi
	kill -TERM "${pid}"
	for _ in $(seq 1 100); do
		if ! kill -0 "${pid}" >/dev/null 2>&1; then
			rm -f "${pid_file}"
			return 0
		fi
		sleep 0.1
	done
	kill -KILL "${pid}"
	for _ in $(seq 1 50); do
		if ! kill -0 "${pid}" >/dev/null 2>&1; then
			rm -f "${pid_file}"
			return 0
		fi
		sleep 0.1
	done
	printf 'reader pid did not terminate: %s\n' "${pid}" >&2
	return 1
}

stop_owned_process /run/usbip-go-reader-supervisor.pid 'reader-supervisor[.]sh'
stop_owned_process /run/usbip-go-reader.pid 'dd.*if=/dev/tty(GS|ACM)'
rm -f /run/usbip-go-reader-supervisor.pid /run/usbip-go-reader.pid
EOF

	cat >"${run_root}/stop-usbip-go-server.sh" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

readonly pid_file=/run/usbip-go-serve.pid
readonly status_socket=/run/usbip-go/status.sock

finish_shutdown() {
	rm -f "${pid_file}"
	for _ in $(seq 1 50); do
		[[ -e ${status_socket} ]] || return 0
		sleep 0.1
	done
	printf 'usbip-go status socket remained after server shutdown: %s\n' \
		"${status_socket}" >&2
	return 1
}

if [[ ! -f ${pid_file} ]]; then
	[[ ! -e ${status_socket} ]] ||
		printf 'usbip-go status socket exists without a server pid file\n' >&2
	[[ ! -e ${status_socket} ]]
	exit
fi
pid=$(<"${pid_file}")
[[ ${pid} =~ ^[0-9]+$ ]] || {
	printf 'invalid usbip-go server pid: %s\n' "${pid}" >&2
	exit 1
}
if ! kill -0 "${pid}" >/dev/null 2>&1; then
	finish_shutdown
	exit
fi
if ! grep -a -F -q -- '/usr/local/bin/usbip-go' "/proc/${pid}/cmdline"; then
	printf 'refusing to signal unowned server pid: %s\n' "${pid}" >&2
	exit 1
fi
kill -TERM "${pid}"
for _ in $(seq 1 350); do
	if ! kill -0 "${pid}" >/dev/null 2>&1; then
		finish_shutdown
		exit
	fi
	sleep 0.1
done
printf 'usbip-go server did not stop after SIGTERM: pid=%s\n' "${pid}" >&2
exit 1
EOF

	chmod 0755 \
		"${run_root}/prepare-exporter.sh" \
		"${run_root}/prepare-importer.sh" \
		"${run_root}/reader-supervisor.sh" \
		"${run_root}/stop-usbip-go-reader.sh" \
		"${run_root}/stop-usbip-go-server.sh"
}

write_role_scripts
for role in exporter importer; do
	write_cloud_init "${role}"
	cloud-localds \
		"${run_root}/${role}/seed.img" \
		"${run_root}/${role}/user-data" \
		"${run_root}/${role}/meta-data"
	qemu-img create -q -f qcow2 -F qcow2 -b "${image_path}" \
		"${run_root}/${role}/root.qcow2"
	qemu-img resize -q "${run_root}/${role}/root.qcow2" "${vm_disk_size}"
done

start_role() {
	local role=$1
	local phase=${2:-validation}
	local role_root=${run_root}/${role}
	local serial_log=${log_root}/${role}-serial.log
	local primary_netdev interguest_netdev interguest_mac
	local -a network_args

	if [[ ${role} == exporter ]]; then
		primary_netdev="user,id=net0,hostfwd=tcp:127.0.0.1:${exporter_ssh_port}-:22"
	else
		primary_netdev="user,id=net0,hostfwd=tcp:127.0.0.1:${importer_ssh_port}-:22"
	fi
	network_args=(
		-netdev "${primary_netdev}"
		-device "virtio-net-pci,netdev=net0"
	)

	if [[ ${phase} == validation ]]; then
		if [[ ${role} == exporter ]]; then
			interguest_netdev="stream,id=interguest,server=on,addr.type=inet,addr.host=127.0.0.1,addr.port=${interguest_stream_port}"
			interguest_mac=${exporter_interguest_mac}
		else
			interguest_netdev="stream,id=interguest,server=off,addr.type=inet,addr.host=127.0.0.1,addr.port=${interguest_stream_port},reconnect-ms=5000"
			interguest_mac=${importer_interguest_mac}
		fi
		network_args+=(
			-netdev "${interguest_netdev}"
			-device "virtio-net-pci,netdev=interguest,mac=${interguest_mac}"
		)
	fi

	rm -f "${role_root}/qemu.pid"
	printf 'starting %s VM: kvm, cpus=%s, memory=%s MiB\n' \
		"${role}" "${vm_cpu_count}" "${vm_memory_mb}"
	qemu-system-x86_64 \
		-accel kvm \
		-cpu host \
		-smp "${vm_cpu_count}" \
		-m "${vm_memory_mb}" \
		-drive "if=virtio,format=qcow2,file=${role_root}/root.qcow2" \
		-drive "if=virtio,format=raw,readonly=on,file=${role_root}/seed.img" \
		"${network_args[@]}" \
		-display none \
		-serial "file:${serial_log}" \
		-daemonize \
		-pidfile "${role_root}/qemu.pid"
	role_running "${role}" || fail "${role} VM did not stay running"
}

wait_for_ssh() {
	local role=$1

	for _ in $(seq 1 300); do
		if ssh_role "${role}" true >/dev/null 2>&1; then
			return 0
		fi
		role_running "${role}" || fail "${role} VM exited before SSH became ready"
		sleep 1
	done
	cat "${log_root}/${role}-serial.log" >&2
	fail "${role} guest SSH did not become ready"
}

wait_for_role_exit() {
	local role=$1
	local pid_file

	pid_file=$(role_pid_file "${role}")
	for _ in $(seq 1 120); do
		if ! role_running "${role}"; then
			rm -f "${pid_file}"
			return 0
		fi
		sleep 0.5
	done
	fail "${role} VM did not power off"
}

install_role_payload() {
	local role=$1

	scp_to_role "${role}" "${usbip_binary}" /home/runner/usbip-go
	scp_to_role "${role}" "${run_root}/prepare-${role}.sh" /home/runner/prepare-role.sh
	scp_to_role "${role}" "${run_root}/reader-supervisor.sh" /home/runner/reader-supervisor.sh
	scp_to_role "${role}" "${run_root}/stop-usbip-go-reader.sh" /home/runner/stop-usbip-go-reader.sh
	ssh_role "${role}" \
		'sudo install -m 0755 /home/runner/usbip-go /usr/local/bin/usbip-go && sudo install -m 0755 /home/runner/prepare-role.sh /usr/local/sbin/prepare-role.sh && sudo install -m 0755 /home/runner/reader-supervisor.sh /usr/local/sbin/reader-supervisor.sh && sudo install -m 0755 /home/runner/stop-usbip-go-reader.sh /usr/local/sbin/stop-usbip-go-reader.sh'

	if [[ ${role} == exporter ]]; then
		scp_to_role "${role}" "${provision_gadget_script}" \
			/home/runner/provision-real-busid.sh
		scp_to_role "${role}" "${run_root}/stop-usbip-go-server.sh" \
			/home/runner/stop-usbip-go-server.sh
		ssh_role exporter \
			'sudo install -m 0755 /home/runner/provision-real-busid.sh /usr/local/sbin/provision-real-busid.sh && sudo install -m 0755 /home/runner/stop-usbip-go-server.sh /usr/local/sbin/stop-usbip-go-server.sh'
	fi

	ssh_role "${role}" 'sudo /usr/local/sbin/prepare-role.sh'
}

role_interguest_mac() {
	case "$1" in
	exporter)
		printf '%s\n' "${exporter_interguest_mac}"
		;;
	importer)
		printf '%s\n' "${importer_interguest_mac}"
		;;
	*)
		fail "unknown VM role for inter-guest MAC: $1"
		;;
	esac
}

role_interguest_address() {
	case "$1" in
	exporter)
		printf '%s\n' "${exporter_interguest_address}"
		;;
	importer)
		printf '%s\n' "${importer_interguest_address}"
		;;
	*)
		fail "unknown VM role for inter-guest address: $1"
		;;
	esac
}

role_interguest_peer() {
	case "$1" in
	exporter)
		printf '%s\n' "${importer_interguest_address}"
		;;
	importer)
		printf '%s\n' "${exporter_interguest_address}"
		;;
	*)
		fail "unknown VM role for inter-guest peer: $1"
		;;
	esac
}

configure_interguest_network() {
	local role=$1
	local address iface mac

	address=$(role_interguest_address "${role}")
	mac=$(role_interguest_mac "${role}")
	iface=$(ssh_role "${role}" \
		"ip -j link show | jq -er --arg mac '${mac}' '.[] | select(.address == \$mac) | .ifname'") ||
		fail "${role} failed to identify its dedicated inter-guest interface"
	[[ ${iface} =~ ^[[:alnum:]_.:-]+$ && ${iface} != lo ]] ||
		fail "${role} returned an invalid inter-guest interface: ${iface}"
	ssh_role "${role}" \
		"sudo ip link set dev '${iface}' up && sudo ip address replace '${address}/${interguest_prefix_length}' dev '${iface}'" ||
		fail "${role} failed to configure its dedicated inter-guest link"
	printf '%s\n' "${iface}" >"${log_root}/${role}-interguest-interface.txt"
}

apply_network_delay() {
	local role=$1
	local iface peer route_path

	peer=$(role_interguest_peer "${role}")
	route_path=${log_root}/${role}-peer-route.json
	ssh_role "${role}" "ip -j route get '${peer}'" >"${route_path}" ||
		fail "${role} failed to resolve the route to peer ${peer}"
	iface=$(ssh_role "${role}" \
		"ip -j route get '${peer}' | jq -er '.[0].dev | select(. != \"lo\" and length > 0)'") ||
		fail "${role} failed to identify a non-loopback egress route to peer ${peer}"
	[[ ${iface} =~ ^[[:alnum:]_.:-]+$ && ${iface} != lo ]] ||
		fail "${role} returned an invalid egress interface: ${iface}"
	[[ ${iface} == "$(<"${log_root}/${role}-interguest-interface.txt")" ]] ||
		fail "${role} route to ${peer} did not use the dedicated inter-guest interface"
	ssh_role "${role}" \
		"sudo tc qdisc replace dev '${iface}' root netem delay ${network_delay_ms}ms" ||
		fail "${role} failed to apply ${network_delay_ms} ms netem delay"
	printf '%s\n' "${iface}" >"${log_root}/${role}-egress-interface.txt"
	ssh_role "${role}" 'ip -j addr show' >"${log_root}/${role}-ip-addresses.json"
	ssh_role "${role}" 'ip -j route show table all' >"${log_root}/${role}-ip-routes.json"
	ssh_role "${role}" "sudo tc -s qdisc show dev '${iface}'" \
		>"${log_root}/${role}-netem-before.log"
	kernel_vm_require_netem_delay \
		"${log_root}/${role}-netem-before.log" "${network_delay_ms}" ||
		fail "${role}: effective netem delay does not match ${network_delay_ms} ms"
}

wait_for_exporter_idle() {
	local label=$1
	local status_ready=false
	local guest_status=/run/usbip-go-exporter-status.json
	local host_status=${log_root}/${label}-exporter-status.json

	for _ in $(seq 1 200); do
		if ssh_role exporter \
			"sudo sh -c 'curl --fail --silent --show-error --unix-socket \"${status_socket}\" http://localhost/ > \"${guest_status}.tmp\" && mv \"${guest_status}.tmp\" \"${guest_status}\"' && sudo jq -e --arg busid '${busid}' '.schema == \"v1\" and .listening.accepting == true and (.sessions | type == \"array\" and length == 0) and (.bound_devices | type == \"array\") and ([.bound_devices[] | select(.busid == \$busid and .vid == \"0x1d6b\" and .pid == \"0x1040\")] | length == 1) and (.kernel_modules.usbip_core == \"loaded\") and (.kernel_modules.usbip_host == \"loaded\") and (has(\"bound_devices_error\") | not)' '${guest_status}'" \
			>/dev/null 2>&1; then
			status_ready=true
			break
		fi
		ssh_role exporter "sudo bash -c 'kill -0 \"\$(</run/usbip-go-serve.pid)\"'" ||
			fail "${label}: exporter server exited while waiting for idle status"
		sleep 0.1
	done
	if [[ ${status_ready} == true ]]; then
		ssh_role exporter "sudo cat '${guest_status}'" >"${host_status}" ||
			fail "${label}: exporter status was validated but its evidence could not be retained"
	else
		ssh_role exporter "sudo cat '${guest_status}'" >"${host_status}" 2>/dev/null || true
		fail "${label}: exporter status did not prove an empty session registry and ready bound device"
	fi
}

wait_for_remote_device() {
	local cycle=$1
	local remote_list_ready=false

	for _ in $(seq 1 100); do
		if ssh_role importer \
			"sudo bash -c '/usr/local/bin/usbip-go --output=json list ${guest_remote} > /run/usbip-go-remote-list.json 2> /run/usbip-go-remote-list.err' && jq -e --arg busid '${busid}' '.schema == \"v1\" and ([.devices[] | select(.busid == \$busid and .vendor_id == \"1d6b\" and .product_id == \"1040\")] | length == 1)' /run/usbip-go-remote-list.json" \
			>/dev/null 2>&1; then
			remote_list_ready=true
			break
		fi
		ssh_role exporter "sudo bash -c 'kill -0 \"\$(</run/usbip-go-serve.pid)\"'" ||
			fail "cycle ${cycle}: exporter server exited during remote-list convergence"
		sleep 0.1
	done
	if [[ ${remote_list_ready} == true ]]; then
		ssh_role importer 'sudo cat /run/usbip-go-remote-list.json' \
			>"${log_root}/cycle-${cycle}-remote-list.json" ||
			fail "cycle ${cycle}: validated remote-list evidence could not be retained"
	else
		ssh_role importer 'sudo cat /run/usbip-go-remote-list.json' \
			>"${log_root}/cycle-${cycle}-remote-list.json" 2>/dev/null || true
	fi
	ssh_role importer 'sudo cat /run/usbip-go-remote-list.err' \
		>"${log_root}/cycle-${cycle}-remote-list.err" 2>/dev/null || true
	[[ ${remote_list_ready} == true ]] ||
		fail "cycle ${cycle}: importer remote list did not converge on the exact gadget"
}

attach_cycle() {
	local cycle=$1
	local port_ready=false

	if ! port_id=$(ssh_role importer \
		"sudo bash -c '/usr/local/bin/usbip-go --output=json attach ${guest_remote} ${busid} > /run/usbip-go-attach.json 2> /run/usbip-go-attach.err' && jq -e --arg busid '${busid}' '.schema == \"v1\" and .op == \"attach\" and .ok == true and .port.busid == \$busid and (.port.id | type == \"number\")' /run/usbip-go-attach.json >/dev/null && jq -er '.port.id | select(type == \"number\" and . >= 0 and floor == .)' /run/usbip-go-attach.json"); then
		fail "cycle ${cycle}: attach command failed"
	fi
	[[ ${port_id} =~ ^[0-9]+$ ]] || fail "cycle ${cycle}: attach returned invalid port id: ${port_id}"
	port_attached=true
	ssh_role importer 'sudo cat /run/usbip-go-attach.json' \
		>"${log_root}/cycle-${cycle}-attach.json"
	ssh_role importer 'sudo cat /run/usbip-go-attach.err' \
		>"${log_root}/cycle-${cycle}-attach.err" 2>/dev/null || true

	for _ in $(seq 1 100); do
		if ssh_role importer \
			"sudo bash -c '/usr/local/bin/usbip-go --output=json port --id ${port_id} > /run/usbip-go-port.json 2> /run/usbip-go-port.err' && jq -e --argjson port '${port_id}' '.schema == \"v1\" and (.ports | length == 1) and .ports[0].id == \$port and .ports[0].status == \"used\"' /run/usbip-go-port.json" \
			>/dev/null 2>&1; then
			port_ready=true
			break
		fi
		sleep 0.1
	done
	if [[ ${port_ready} == true ]]; then
		ssh_role importer 'sudo cat /run/usbip-go-port.json' \
			>"${log_root}/cycle-${cycle}-port-used.json" ||
			fail "cycle ${cycle}: validated used-Port evidence could not be retained"
	else
		ssh_role importer 'sudo cat /run/usbip-go-port.json' \
			>"${log_root}/cycle-${cycle}-port-used.json" 2>/dev/null || true
	fi
	ssh_role importer 'sudo cat /run/usbip-go-port.err' \
		>"${log_root}/cycle-${cycle}-port-used.err" 2>/dev/null || true
	[[ ${port_ready} == true ]] || fail "cycle ${cycle}: attached port ${port_id} did not reach used status"
}

wait_for_importer_tty() {
	local cycle=$1

	importer_tty=''
	for _ in $(seq 1 200); do
		if importer_tty=$(ssh_role importer \
			"sudo bash -c 'shopt -s nullglob; devices=(/dev/ttyACM*); ((\${#devices[@]} == 1)) || exit 1; printf \"%s\\n\" \"\${devices[0]}\"'" \
			2>/dev/null); then
			break
		fi
		sleep 0.1
	done
	[[ ${importer_tty} =~ ^/dev/ttyACM[0-9]+$ ]] ||
		fail "cycle ${cycle}: importer ACM device did not appear uniquely"
	ssh_role importer \
		"sudo bash -c 'path=\$(readlink -f /sys/class/tty/${importer_tty##*/}/device); found=false; while [[ \${path} != / ]]; do if [[ -r \${path}/idVendor && -r \${path}/idProduct ]]; then [[ \$(<\${path}/idVendor) == 1d6b && \$(<\${path}/idProduct) == 1040 ]] || exit 1; found=true; break; fi; path=\${path%/*}; [[ -n \${path} ]] || path=/; done; [[ \${found} == true ]]'" ||
		fail "cycle ${cycle}: ${importer_tty} does not descend from the expected gadget"
}

transfer_payload() {
	local cycle=$1
	local direction=$2
	local source_role=$3
	local source_tty=$4
	local destination_role=$5
	local destination_tty=$6
	local payload=$7
	local payload_count=${#payload}
	local remote_output=/run/usbip-go-payload-${direction}
	local expected_file=${log_root}/cycle-${cycle}-${direction}.expected
	local received_file=${log_root}/cycle-${cycle}-${direction}.received
	local reader_ready=false
	local reader_complete=false

	printf '%s' "${payload}" >"${expected_file}"
	ssh_role "${source_role}" "sudo stty -F '${source_tty}' raw -echo"
	ssh_role "${destination_role}" "sudo stty -F '${destination_tty}' raw -echo"
	ssh_role "${destination_role}" \
		"sudo bash -c 'rm -f /run/usbip-go-reader.pid /run/usbip-go-reader.status /run/usbip-go-reader.err; nohup /usr/local/sbin/reader-supervisor.sh ${destination_tty} ${payload_count} ${remote_output} > /run/usbip-go-reader-supervisor.log 2>&1 < /dev/null &'"

	for _ in $(seq 1 100); do
		if ssh_role "${destination_role}" \
			"sudo bash -c '[[ -s /run/usbip-go-reader.pid ]]; pid=\$(</run/usbip-go-reader.pid); kill -0 \"\${pid}\"; for fd in /proc/\${pid}/fd/*; do [[ \$(readlink -f \"\${fd}\") == ${destination_tty} ]] && exit 0; done; exit 1'" \
			>/dev/null 2>&1; then
			reader_ready=true
			break
		fi
		sleep 0.1
	done
	[[ ${reader_ready} == true ]] ||
		fail "cycle ${cycle} ${direction}: destination reader did not open ${destination_tty}"

	printf '%s' "${payload}" | ssh_role_with_stdin "${source_role}" \
		"sudo timeout 10 dd of='${source_tty}' bs=1 count=${payload_count} status=none"

	for _ in $(seq 1 200); do
		if ssh_role "${destination_role}" 'sudo test -s /run/usbip-go-reader.status' \
			>/dev/null 2>&1; then
			reader_complete=true
			break
		fi
		sleep 0.1
	done
	[[ ${reader_complete} == true ]] ||
		fail "cycle ${cycle} ${direction}: destination reader did not complete"
	[[ $(ssh_role "${destination_role}" 'sudo cat /run/usbip-go-reader.status') == 0 ]] ||
		fail "cycle ${cycle} ${direction}: destination reader exited non-zero"
	[[ $(ssh_role "${destination_role}" "sudo stat -c %s '${remote_output}'") == "${payload_count}" ]] ||
		fail "cycle ${cycle} ${direction}: received byte count is not exact"
	ssh_role "${destination_role}" "sudo cat '${remote_output}'" >"${received_file}"
	cmp --silent "${expected_file}" "${received_file}" ||
		fail "cycle ${cycle} ${direction}: payload does not match byte-for-byte"
}

detach_cycle() {
	local cycle=$1
	local port_absent=false
	local tty_absent=false
	local exporter_available=false
	local exporter_status_path=/run/usbip-go-exporter-device-status

	ssh_role importer \
		"sudo /usr/local/bin/usbip-go --output=json detach '${port_id}'" \
		>"${log_root}/cycle-${cycle}-detach.json"
	port_attached=false
	for _ in $(seq 1 100); do
		if ssh_role importer \
			"sudo bash -c 'set +e; /usr/local/bin/usbip-go --output=json port --id ${port_id} > /run/usbip-go-port-after-detach.out 2> /run/usbip-go-port-after-detach.err; rc=\$?; set -e; [[ \${rc} -eq 5 ]] && grep -q \"device not found\" /run/usbip-go-port-after-detach.err'" \
			>/dev/null 2>&1; then
			port_absent=true
			break
		fi
		sleep 0.1
	done
	if [[ ${port_absent} == true ]]; then
		ssh_role importer 'sudo cat /run/usbip-go-port-after-detach.out' \
			>"${log_root}/cycle-${cycle}-port-after-detach.out" ||
			fail "cycle ${cycle}: post-detach stdout evidence could not be retained"
		ssh_role importer 'sudo cat /run/usbip-go-port-after-detach.err' \
			>"${log_root}/cycle-${cycle}-port-after-detach.err" ||
			fail "cycle ${cycle}: post-detach stderr evidence could not be retained"
	else
		ssh_role importer 'sudo cat /run/usbip-go-port-after-detach.out' \
			>"${log_root}/cycle-${cycle}-port-after-detach.out" 2>/dev/null || true
		ssh_role importer 'sudo cat /run/usbip-go-port-after-detach.err' \
			>"${log_root}/cycle-${cycle}-port-after-detach.err" 2>/dev/null || true
		fail "cycle ${cycle}: detached port ${port_id} is still reported"
	fi

	for _ in $(seq 1 200); do
		if ssh_role importer \
			"sudo bash -c 'shopt -s nullglob; devices=(/dev/ttyACM*); ((\${#devices[@]} == 0))'" \
			>/dev/null 2>&1; then
			tty_absent=true
			break
		fi
		sleep 0.1
	done
	[[ ${tty_absent} == true ]] || fail "cycle ${cycle}: imported ACM device did not disappear"

	for _ in $(seq 1 200); do
		if ssh_role exporter \
			"sudo bash -c '[[ -r /sys/bus/usb/devices/${busid}/usbip_status ]] && cat /sys/bus/usb/devices/${busid}/usbip_status > ${exporter_status_path} && [[ \$(<${exporter_status_path}) == 1 ]]'" \
			>/dev/null 2>&1; then
			exporter_available=true
			break
		fi
		sleep 0.1
	done
	if [[ ${exporter_available} == true ]]; then
		ssh_role exporter "sudo cat '${exporter_status_path}'" \
			>"${log_root}/cycle-${cycle}-exporter-device-status.txt" ||
			fail "cycle ${cycle}: exporter availability evidence could not be retained"
	else
		ssh_role exporter "sudo cat '${exporter_status_path}'" \
			>"${log_root}/cycle-${cycle}-exporter-device-status.txt" 2>/dev/null || true
		fail "cycle ${cycle}: exporter device did not return to available state"
	fi
	wait_for_exporter_idle "cycle-${cycle}"
	port_id=''
}

for role in exporter importer; do
	printf 'provisioning %s VM sequentially\n' "${role}"
	start_role "${role}" provision
	wait_for_ssh "${role}"
	ssh_role_with_timeout "${role}" "$((cloud_init_timeout + 30))" \
		"sudo timeout ${cloud_init_timeout} cloud-init status --wait"
	install_role_payload "${role}"
	ssh_role "${role}" 'sudo poweroff' >/dev/null 2>&1 || true
	wait_for_role_exit "${role}"
done

printf 'starting final two-VM USB/IP validation\n'
start_role exporter
wait_for_ssh exporter
ssh_role exporter 'sudo /usr/local/sbin/prepare-role.sh'

busid=$(ssh_role exporter 'sudo /usr/local/sbin/provision-real-busid.sh create')
readonly busid
gadget_created=true
[[ ${busid} =~ ^[0-9]+-[0-9]+([.][0-9]+)*$ ]] || fail "invalid provisioned busid: ${busid}"
ssh_role exporter \
	"sudo bash -c '[[ \$(<\"/sys/bus/usb/devices/${busid}/idVendor\") == 1d6b && \$(<\"/sys/bus/usb/devices/${busid}/idProduct\") == 1040 && -c /dev/ttyGS0 ]]'" ||
	fail 'provisioned exporter gadget identity or /dev/ttyGS0 is invalid'

ssh_role exporter \
	"sudo bash -c '/usr/local/bin/usbip-go --output=json list > /run/usbip-go-local-list.json' && jq -e --arg busid '${busid}' '.schema == \"v1\" and ([.devices[] | select(.busid == \$busid and .vendor_id == \"1d6b\" and .product_id == \"1040\")] | length == 1)' /run/usbip-go-local-list.json" ||
	fail 'exporter local list did not contain the exact provisioned gadget'
ssh_role exporter 'sudo cat /run/usbip-go-local-list.json' >"${log_root}/exporter-local-list.json"

ssh_role exporter \
	"sudo /usr/local/bin/usbip-go --output=json bind '${busid}'" \
	>"${log_root}/exporter-bind.json"
device_bound=true

ssh_role exporter \
	"sudo install -d -m 0755 /run/usbip-go && sudo bash -c 'rm -f /run/usbip-go-serve.pid /var/log/usbip-go-serve.log ${status_socket}; nohup env NO_COLOR=1 /usr/local/bin/usbip-go --log-level info --log-format json serve --listen 0.0.0.0:3240 --status-socket=${status_socket} --status-socket-group=root > /var/log/usbip-go-serve.log 2>&1 < /dev/null & echo \$! > /run/usbip-go-serve.pid'"
serve_started=true
wait_for_exporter_idle startup

start_role importer
wait_for_ssh importer
ssh_role importer 'sudo /usr/local/sbin/prepare-role.sh'

exporter_boot_id=$(ssh_role exporter 'cat /proc/sys/kernel/random/boot_id')
importer_boot_id=$(ssh_role importer 'cat /proc/sys/kernel/random/boot_id')
[[ ${exporter_boot_id} =~ ^[0-9a-f-]{36}$ &&
	${importer_boot_id} =~ ^[0-9a-f-]{36}$ &&
	${exporter_boot_id} != "${importer_boot_id}" ]] ||
	fail 'exporter and importer did not prove distinct guest kernel instances'
printf '%s\n' "${exporter_boot_id}" >"${log_root}/exporter-kernel-boot-id.txt"
printf '%s\n' "${importer_boot_id}" >"${log_root}/importer-kernel-boot-id.txt"
ssh_role exporter 'uname -a' >"${log_root}/exporter-kernel-version.txt"
ssh_role importer 'uname -a' >"${log_root}/importer-kernel-version.txt"

configure_interguest_network exporter
configure_interguest_network importer
apply_network_delay exporter
apply_network_delay importer

mapfile -t cycles < <(kernel_vm_cycle_sequence "${cycle_count}")
for cycle in "${cycles[@]}"; do
	printf 'running two-guest resilience cycle %s/%s with %s ms delay per guest egress\n' \
		"${cycle}" "${cycle_count}" "${network_delay_ms}"
	wait_for_remote_device "${cycle}"
	attach_cycle "${cycle}"
	wait_for_importer_tty "${cycle}"

	exporter_payload="usbip-go-${run_id}-cycle-${cycle}-exporter-to-importer"
	transfer_payload "${cycle}" exporter-to-importer \
		exporter /dev/ttyGS0 importer "${importer_tty}" "${exporter_payload}"

	importer_payload="usbip-go-${run_id}-cycle-${cycle}-importer-to-exporter"
	transfer_payload "${cycle}" importer-to-exporter \
		importer "${importer_tty}" exporter /dev/ttyGS0 "${importer_payload}"

	detach_cycle "${cycle}"
	((completed_cycles += 1))
done
kernel_vm_validate_completed_cycles "${cycle_count}" "${completed_cycles}" ||
	fail 'the two-guest test did not complete exactly three cycles'

for role in exporter importer; do
	iface=$(<"${log_root}/${role}-egress-interface.txt")
	ssh_role "${role}" "sudo tc -s qdisc show dev '${iface}'" \
		>"${log_root}/${role}-netem-after.log"
	kernel_vm_require_netem_delay \
		"${log_root}/${role}-netem-after.log" "${network_delay_ms}" ||
		fail "${role}: netem delay changed during the payload cycles"
	kernel_vm_require_netem_advanced \
		"${log_root}/${role}-netem-before.log" \
		"${log_root}/${role}-netem-after.log" ||
		fail "${role}: netem qdisc did not remain active with advancing traffic"
done

cleanup_guest_state || fail 'post-validation guest cleanup failed'

capture_role_diagnostics_strict exporter ||
	fail 'required exporter diagnostic evidence is incomplete'
capture_role_diagnostics_strict importer ||
	fail 'required importer diagnostic evidence is incomplete'
if kernel_vm_logs_contain_failure "${log_root}"; then
	fail 'two-VM logs contain a skip, prerequisite failure, or kernel fault'
else
	log_scan_status=$?
fi
[[ ${log_scan_status} -eq 1 ]] || fail 'two-VM logs could not be scanned completely'

ssh_role importer 'sudo poweroff' >/dev/null 2>&1 || true
wait_for_role_exit importer
ssh_role exporter 'sudo poweroff' >/dev/null 2>&1 || true
wait_for_role_exit exporter

bound_retained_logs || fail 'retained two-VM logs could not be bounded'

status=0
printf 'two-VM USB/IP validation completed successfully; logs: %s\n' "${log_root}"
