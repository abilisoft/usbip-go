#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 AbiliSoft
# SPDX-License-Identifier: Apache-2.0

kernel_vm_validate_config() {
	local configured_cycle_count=$1
	local configured_network_delay_ms=$2
	local configured_vm_cpu_count=$3
	local configured_vm_memory_mb=$4

	[[ ${configured_cycle_count} =~ ^[0-9]+$ && ${configured_cycle_count} -eq 3 ]] || {
		printf 'KERNEL_VM_CYCLE_COUNT must be exactly 3\n' >&2
		return 1
	}
	[[ ${configured_network_delay_ms} =~ ^[0-9]+$ && ${configured_network_delay_ms} -eq 25 ]] || {
		printf 'KERNEL_VM_NETWORK_DELAY_MS must be exactly 25\n' >&2
		return 1
	}
	[[ ${configured_vm_cpu_count} =~ ^[0-9]+$ && ${configured_vm_cpu_count} -eq 1 ]] || {
		printf 'KERNEL_VM_CPU_COUNT must be exactly 1\n' >&2
		return 1
	}
	[[ ${configured_vm_memory_mb} =~ ^[0-9]+$ && ${configured_vm_memory_mb} -eq 1024 ]] || {
		printf 'KERNEL_VM_MEMORY_MB must be exactly 1024\n' >&2
		return 1
	}
}

kernel_vm_validate_cache_root() {
	local configured_cache_root=$1
	local configured_workspace_root=$2
	local canonical_cache_root canonical_workspace_root

	[[ -n ${configured_cache_root} && ${configured_cache_root} == /* &&
		-n ${configured_workspace_root} && ${configured_workspace_root} == /* ]] || {
		printf 'KERNEL_VM_CACHE_ROOT and KERNEL_VM_WORKSPACE_ROOT must be non-empty absolute paths\n' >&2
		return 1
	}
	canonical_cache_root=$(realpath -m -- "${configured_cache_root}") || return 1
	canonical_workspace_root=$(realpath -m -- "${configured_workspace_root}") || return 1
	[[ ${canonical_workspace_root} != / &&
		${canonical_cache_root} == "${canonical_workspace_root}/.local/kernel-vm" ]] || {
		printf 'KERNEL_VM_CACHE_ROOT must resolve to <repository>/.local/kernel-vm: %s\n' \
			"${canonical_cache_root}" >&2
		return 1
	}
}

kernel_vm_validate_ports() {
	local exporter_port=$1
	local importer_port=$2
	local forward_port=$3
	local port

	for port in "${exporter_port}" "${importer_port}" "${forward_port}"; do
		[[ ${port} =~ ^[0-9]+$ && ${port} -ge 1024 && ${port} -le 65535 ]] || {
			printf 'kernel VM forwarded ports must be distinct integers from 1024 through 65535\n' >&2
			return 1
		}
	done
	[[ ${exporter_port} -ne ${importer_port} &&
		${exporter_port} -ne ${forward_port} &&
		${importer_port} -ne ${forward_port} ]] || {
		printf 'kernel VM forwarded ports must be distinct\n' >&2
		return 1
	}
}

kernel_vm_cycle_sequence() {
	local configured_cycle_count=$1

	[[ ${configured_cycle_count} =~ ^[0-9]+$ && ${configured_cycle_count} -eq 3 ]] || return 1
	printf '1\n2\n3\n'
}

kernel_vm_validate_completed_cycles() {
	local expected=$1
	local completed=$2

	[[ ${expected} =~ ^[0-9]+$ && ${completed} =~ ^[0-9]+$ &&
		${expected} -eq 3 && ${completed} -eq ${expected} ]] || {
		printf 'exactly three complete resilience cycles are required: expected=%s completed=%s\n' \
			"${expected}" "${completed}" >&2
		return 1
	}
}

kernel_vm_cleanup_sequence() {
	printf 'detach\nreaders\nserver\nunbind\ngadget\n'
}

kernel_vm_run_cleanup_plan() {
	local handler=$1
	local cleanup_status=0
	local cleanup_step

	while IFS= read -r cleanup_step; do
		"${handler}" "${cleanup_step}" || cleanup_status=1
	done < <(kernel_vm_cleanup_sequence)

	return "${cleanup_status}"
}

kernel_vm_remove_run_root() {
	local candidate_run_root=$1
	local configured_run_parent=$2
	local canonical_candidate canonical_parent

	[[ -n ${candidate_run_root} && ${candidate_run_root} == /* &&
		-n ${configured_run_parent} && ${configured_run_parent} == /* ]] || {
		printf 'refusing to remove invalid kernel VM run root: %s\n' \
			"${candidate_run_root}" >&2
		return 1
	}
	canonical_candidate=$(realpath -m -- "${candidate_run_root}") || return 1
	canonical_parent=$(realpath -m -- "${configured_run_parent}") || return 1
	[[ ${canonical_parent} != / &&
		${canonical_candidate%/*} == "${canonical_parent}" &&
		${canonical_candidate##*/} == *-two-vm ]] || {
		printf 'refusing to remove kernel VM run root outside its owner directory: %s\n' \
			"${canonical_candidate}" >&2
		return 1
	}
	rm -rf -- "${canonical_candidate}"
	[[ ! -e ${canonical_candidate} ]]
}

kernel_vm_remove_run_root_if_roles_stopped() {
	local candidate_run_root=$1
	local configured_run_parent=$2
	local all_roles_stopped=$3

	[[ ${all_roles_stopped} == true ]] || {
		printf 'preserving kernel VM run root because not every guest was confirmed stopped: %s\n' \
			"${candidate_run_root}" >&2
		return 1
	}

	kernel_vm_remove_run_root "${candidate_run_root}" "${configured_run_parent}"
}

kernel_vm_require_nonempty_evidence() {
	local label=$1
	local evidence_path

	shift
	[[ -n ${label} && $# -gt 0 ]] || {
		printf 'required diagnostic evidence set is empty: %s\n' "${label}" >&2
		return 1
	}

	for evidence_path in "$@"; do
		[[ -f ${evidence_path} && -s ${evidence_path} ]] || {
			printf 'required diagnostic evidence is missing or empty for %s: %s\n' \
				"${label}" "${evidence_path}" >&2
			return 1
		}
	done
}

kernel_vm_bound_log_file() {
	local log_path=$1
	local retained_bytes=$2
	local temporary_path

	[[ -f ${log_path} ]] || return 0
	[[ ${retained_bytes} =~ ^[0-9]+$ && ${retained_bytes} -gt 0 ]] || return 1
	[[ $(wc -c <"${log_path}") -le ${retained_bytes} ]] && return 0

	temporary_path=${log_path}.bounded
	tail -c "${retained_bytes}" "${log_path}" >"${temporary_path}" || {
		rm -f -- "${temporary_path}"
		return 1
	}
	mv -f -- "${temporary_path}" "${log_path}"
}

kernel_vm_sha512_matches() {
	local path=$1
	local expected=$2
	local actual

	[[ -f ${path} ]] || return 1
	actual=$(sha512sum "${path}")
	actual=${actual%% *}

	[[ ${actual} == "${expected}" ]]
}

kernel_vm_netem_packet_count() {
	local path=$1

	awk '
		/^qdisc / { in_netem = ($2 == "netem") }
		in_netem && /Sent / {
			for (i = 1; i < NF; i++) {
				if ($(i + 1) == "pkt" && $i ~ /^[0-9]+$/) {
					print $i
					exit
				}
			}
		}
	' "${path}"
}

kernel_vm_netem_byte_count() {
	local path=$1

	awk '
		/^qdisc / { in_netem = ($2 == "netem") }
		in_netem && /Sent / {
			for (i = 1; i < NF; i++) {
				if ($i == "Sent" && $(i + 1) ~ /^[0-9]+$/) {
					print $(i + 1)
					exit
				}
			}
		}
	' "${path}"
}

kernel_vm_require_netem_delay() {
	local path=$1
	local expected_delay_ms=$2

	[[ ${expected_delay_ms} =~ ^[0-9]+$ && ${expected_delay_ms} -gt 0 ]] || return 1
	awk -v expected="${expected_delay_ms}" '
		$1 == "qdisc" && $2 == "netem" {
			for (i = 1; i < NF; i++) {
				if ($i != "delay") {
					continue
				}
				value = $(i + 1)
				if (value !~ /^[0-9]+([.][0-9]+)?ms$/) {
					exit 1
				}
				sub(/ms$/, "", value)
				found = (value + 0 == expected + 0)
				exit !found
			}
		}
		END { if (!found) exit 1 }
	' "${path}" || {
		printf 'netem qdisc does not report the required %s ms delay\n' \
			"${expected_delay_ms}" >&2
		return 1
	}
}

kernel_vm_require_netem_advanced() {
	local before_path=$1
	local after_path=$2
	local before_bytes after_bytes before_packets after_packets

	before_bytes=$(kernel_vm_netem_byte_count "${before_path}")
	after_bytes=$(kernel_vm_netem_byte_count "${after_path}")
	before_packets=$(kernel_vm_netem_packet_count "${before_path}")
	after_packets=$(kernel_vm_netem_packet_count "${after_path}")
	[[ ${before_bytes} =~ ^[0-9]+$ && ${after_bytes} =~ ^[0-9]+$ &&
		${before_packets} =~ ^[0-9]+$ && ${after_packets} =~ ^[0-9]+$ ]] || {
		printf 'netem qdisc byte or packet counters are missing\n' >&2
		return 1
	}
	[[ ${after_bytes} -gt ${before_bytes} && ${after_packets} -gt ${before_packets} ]] || {
		printf 'netem qdisc counters did not advance: bytes=%s->%s packets=%s->%s\n' \
			"${before_bytes}" "${after_bytes}" "${before_packets}" "${after_packets}" >&2
		return 1
	}
}

kernel_vm_pid_cmdline_matches_overlay() {
	local cmdline_path=$1
	local overlay_path=$2

	[[ -r ${cmdline_path} && -n ${overlay_path} ]] || return 1
	grep -a -F -q -- "${overlay_path}" "${cmdline_path}"
}

kernel_vm_logs_contain_failure() {
	local root=$1

	[[ -d ${root} && -r ${root} ]] || return 2
	grep -R -E -i -q -- \
		'--- SKIP:|integration harness:|dummy_hcd harness:|required kernel modules not loaded|configfs .*not available|kernel module .*not loaded|activity probe failed|exporter shutdown failed|drain .*failed|cannot find a urb of seqnum|(^|[^[:alpha:]])BUG:|(^|[^[:alpha:]])Oops:|kernel panic' \
		"${root}"
}
