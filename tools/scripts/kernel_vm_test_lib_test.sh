#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 AbiliSoft
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

fail() {
	printf 'kernel VM helper test failed: %s\n' "$*" >&2
	exit 1
}

resolve_helper() {
	local workspace=${TEST_WORKSPACE:-_main}
	local candidate

	for candidate in \
		"${TEST_SRCDIR:-}/${workspace}/tools/scripts/kernel_vm_test_lib.sh" \
		"${RUNFILES_DIR:-}/${workspace}/tools/scripts/kernel_vm_test_lib.sh" \
		"$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)/kernel_vm_test_lib.sh"; do
		if [[ -f ${candidate} ]]; then
			printf '%s\n' "${candidate}"
			return 0
		fi
	done

	fail 'kernel_vm_test_lib.sh runfile is missing'
}

expect_failure() {
	if "$@" >/dev/null 2>&1; then
		fail "expected failure: $*"
	fi
}

helper=$(resolve_helper)
# shellcheck source=tools/scripts/kernel_vm_test_lib.sh
source "${helper}"

kernel_vm_validate_config 3 25 1 1024
expect_failure kernel_vm_validate_config 2 25 1 1024
expect_failure kernel_vm_validate_config 3 0 1 1024
expect_failure kernel_vm_validate_config 3 50 1 1024
expect_failure kernel_vm_validate_config 3 25 2 1024
expect_failure kernel_vm_validate_config 3 25 1 0
expect_failure kernel_vm_validate_config 3 25 1 512
expect_failure kernel_vm_validate_config 3 25 1 1025
kernel_vm_validate_cache_root \
	/home/example/repository/.local/kernel-vm /home/example/repository
expect_failure kernel_vm_validate_cache_root '' /home/example/repository
expect_failure kernel_vm_validate_cache_root relative/cache /home/example/repository
expect_failure kernel_vm_validate_cache_root /tmp/kernel-vm /home/example/repository
expect_failure kernel_vm_validate_cache_root /dev/shm/kernel-vm /home/example/repository
expect_failure kernel_vm_validate_cache_root /home/example/repository/.local/other /home/example/repository
kernel_vm_validate_ports 2222 2223 33240
expect_failure kernel_vm_validate_ports 22 2223 33240
expect_failure kernel_vm_validate_ports 2222 2222 33240
expect_failure kernel_vm_validate_ports 2222 2223 70000

[[ $(kernel_vm_cycle_sequence 3) == $'1\n2\n3' ]] ||
	fail 'cycle sequence is not exactly 1, 2, 3'
expect_failure kernel_vm_cycle_sequence 2
kernel_vm_validate_completed_cycles 3 3
expect_failure kernel_vm_validate_completed_cycles 3 2
expect_failure kernel_vm_validate_completed_cycles 4 4
[[ $(kernel_vm_cleanup_sequence) == $'detach\nreaders\nserver\nunbind\ngadget' ]] ||
	fail 'cleanup order does not drain before unbinding the exporter gadget'
cleanup_calls=()
cleanup_fail_step=server
fake_cleanup_handler() {
	local step=$1

	cleanup_calls+=("${step}")
	[[ ${step} != "${cleanup_fail_step}" ]]
}
expect_failure kernel_vm_run_cleanup_plan fake_cleanup_handler
[[ "${cleanup_calls[*]}" == 'detach readers server unbind gadget' ]] ||
	fail 'cleanup plan stopped after an intermediate command failure'
cleanup_fail_step=''
kernel_vm_run_cleanup_plan fake_cleanup_handler

# The runner exposes these names as readonly globals. Helper-local names must
# never collide with them because Bash dynamically scopes sourced functions.
(
	readonly cycle_count=3
	readonly network_delay_ms=25
	readonly vm_cpu_count=1
	readonly vm_memory_mb=1024
	readonly cache_root=/home/example/repository/.local/kernel-vm
	readonly workspace_root=/home/example/repository
	kernel_vm_validate_config \
		"${cycle_count}" "${network_delay_ms}" "${vm_cpu_count}" "${vm_memory_mb}"
	kernel_vm_validate_cache_root "${cache_root}" "${workspace_root}"
	[[ $(kernel_vm_cycle_sequence "${cycle_count}") == $'1\n2\n3' ]]
) || fail 'helpers collide with the runner readonly configuration'

work=$(mktemp -d)
trap 'rm -rf "${work}"' EXIT

run_root_fixture=${work}/fixture-two-vm
mkdir -p "${run_root_fixture}/exporter"
truncate -s 8M "${run_root_fixture}/exporter/root.qcow2"
expect_failure kernel_vm_remove_run_root_if_roles_stopped \
	"${run_root_fixture}" "${work}" false
[[ -e ${run_root_fixture}/exporter/root.qcow2 ]] ||
	fail 'cleanup gate removed an overlay before every guest stopped'
kernel_vm_remove_run_root_if_roles_stopped \
	"${run_root_fixture}" "${work}" true
[[ ! -e ${run_root_fixture} ]] || fail 'run-root cleanup retained an overlay'
expect_failure kernel_vm_remove_run_root / "${work}"
expect_failure kernel_vm_remove_run_root "${work}/not-a-run-root" "${work}"
mkdir -p "${work}/nested/foreign-two-vm"
expect_failure kernel_vm_remove_run_root "${work}/nested/foreign-two-vm" "${work}"

printf 'kernel evidence\n' >"${work}/kernel-evidence.log"
printf 'journal evidence\n' >"${work}/journal-evidence.log"
printf '{"schema":"v1"}\n' >"${work}/state-evidence.json"
kernel_vm_require_nonempty_evidence test-role \
	"${work}/kernel-evidence.log" \
	"${work}/journal-evidence.log" \
	"${work}/state-evidence.json"
expect_failure kernel_vm_require_nonempty_evidence test-role
expect_failure kernel_vm_require_nonempty_evidence test-role "${work}/missing-evidence.log"
: >"${work}/empty-evidence.log"
expect_failure kernel_vm_require_nonempty_evidence test-role "${work}/empty-evidence.log"

printf '0123456789' >"${work}/large.log"
kernel_vm_bound_log_file "${work}/large.log" 5
[[ $(<"${work}/large.log") == 56789 ]] || fail 'bounded log did not retain the final bytes'
expect_failure kernel_vm_bound_log_file "${work}/large.log" 0

printf 'checksum fixture' >"${work}/image"
checksum=$(sha512sum "${work}/image")
checksum=${checksum%% *}
kernel_vm_sha512_matches "${work}/image" "${checksum}"
expect_failure kernel_vm_sha512_matches "${work}/image" invalid
expect_failure kernel_vm_sha512_matches "${work}/missing" "${checksum}"

cat >"${work}/netem-before" <<'EOF'
qdisc netem 8001: dev ens3 root refcnt 2 limit 1000 delay 25ms
 Sent 100 bytes 2 pkt (dropped 0, overlimits 0 requeues 0)
EOF
cat >"${work}/netem-after" <<'EOF'
qdisc netem 8001: dev ens3 root refcnt 2 limit 1000 delay 25ms
 Sent 900 bytes 12 pkt (dropped 0, overlimits 0 requeues 0)
EOF
cat >"${work}/netem-stale" <<'EOF'
qdisc netem 8001: dev ens3 root refcnt 2 limit 1000 delay 25ms
 Sent 900 bytes 2 pkt (dropped 0, overlimits 0 requeues 0)
EOF
cat >"${work}/netem-byte-stale" <<'EOF'
qdisc netem 8001: dev ens3 root refcnt 2 limit 1000 delay 25ms
 Sent 100 bytes 12 pkt (dropped 0, overlimits 0 requeues 0)
EOF
kernel_vm_require_netem_advanced "${work}/netem-before" "${work}/netem-after"
kernel_vm_require_netem_delay "${work}/netem-before" 25
expect_failure kernel_vm_require_netem_delay "${work}/netem-before" 50
expect_failure kernel_vm_require_netem_advanced "${work}/netem-before" "${work}/netem-stale"
expect_failure kernel_vm_require_netem_advanced \
	"${work}/netem-before" "${work}/netem-byte-stale"

printf 'qemu-system-x86_64\0-drive\0file=/persistent/run/exporter/root.qcow2\0' \
	>"${work}/qemu.cmdline"
kernel_vm_pid_cmdline_matches_overlay \
	"${work}/qemu.cmdline" /persistent/run/exporter/root.qcow2
expect_failure kernel_vm_pid_cmdline_matches_overlay \
	"${work}/qemu.cmdline" /persistent/run/importer/root.qcow2

mkdir "${work}/good-logs" "${work}/bad-logs"
printf 'three cycles completed cleanly\n' >"${work}/good-logs/guest.log"
expect_failure kernel_vm_logs_contain_failure "${work}/good-logs"
failure_markers=(
	'--- SKIP: TestLiveKernel'
	'integration harness: required kernel modules not loaded'
	'activity probe failed'
	'exporter shutdown failed'
	'drain operation failed'
	'vhci_hcd: cannot find a urb of seqnum 42'
	'kernel BUG: synthetic'
	'kernel Oops: synthetic'
	'kernel panic synthetic'
)
for marker in "${failure_markers[@]}"; do
	printf '%s\n' "${marker}" >"${work}/bad-logs/guest.log"
	kernel_vm_logs_contain_failure "${work}/bad-logs" ||
		fail "failure marker was not classified: ${marker}"
done
set +e
kernel_vm_logs_contain_failure "${work}/missing-logs" >/dev/null 2>&1
scan_status=$?
set -e
[[ ${scan_status} -eq 2 ]] || fail 'unreadable logs did not return scan status 2'

printf 'kernel VM helper tests passed\n'
