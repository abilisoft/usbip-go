#!/usr/bin/env bash

set -euo pipefail

die() {
	printf 'changelog-runner: %s\n' "$*" >&2
	exit 1
}

runfiles_root() {
	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}"
		return
	fi

	if [[ -d "${BASH_SOURCE[0]}.runfiles/_main" ]]; then
		printf '%s\n' "${BASH_SOURCE[0]}.runfiles/_main"
		return
	fi

	if [[ -n "${RUNFILES_DIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${RUNFILES_DIR}/${TEST_WORKSPACE}"
		return
	fi

	printf '.\n'
}

resolve_runfile() {
	local path=$1
	local root

	if [[ "${path}" = /* && -e "${path}" ]]; then
		printf '%s\n' "${path}"
		return
	fi

	root=$(runfiles_root)
	if [[ -e "${root}/${path}" ]]; then
		printf '%s\n' "${root}/${path}"
		return
	fi

	if [[ -e "${path}" ]]; then
		printf '%s\n' "${path}"
		return
	fi

	die "runfile not found: ${path}"
}

read_args_file() {
	local path=$1
	local line
	path=$(resolve_runfile "${path}")

	while IFS= read -r line || [[ -n "${line}" ]]; do
		[[ -z "${line//[[:space:]]/}" ]] && continue
		printf '%s\0' "${line}"
	done <"${path}"
}

workspace_root() {
	local candidates=()
	local candidate
	local root

	[[ -n "${BUILD_WORKSPACE_DIRECTORY:-}" ]] && candidates+=("${BUILD_WORKSPACE_DIRECTORY}")
	[[ -n "${PWD:-}" ]] && candidates+=("${PWD}")
	candidates+=(".")

	for candidate in "${candidates[@]}"; do
		root=$(git -C "${candidate}" rev-parse --show-toplevel 2>/dev/null) || continue
		printf '%s\n' "${root}"
		return
	done

	for candidate in "${BUILD_WORKSPACE_DIRECTORY:-}" "${PWD:-}" "."; do
		[[ -n "${candidate}" && -d "${candidate}" ]] || continue
		(cd "${candidate}" && pwd -P)
		return
	done

	die 'could not find workspace'
}

args=()
if [[ -n "${CHANGELOG_ARGS:-}" ]]; then
	while IFS= read -r -d '' arg; do
		args+=("${arg}")
	done < <(read_args_file "${CHANGELOG_ARGS}")
fi
args+=("$@")

cliff=''
config=''
index=0
while ((index < ${#args[@]})); do
	arg=${args[index]}
	case "${arg}" in
	--cliff=*)
		cliff=${arg#--cliff=}
		;;
	--config=*)
		config=${arg#--config=}
		;;
	*)
		break
		;;
	esac
	((index += 1))
done

[[ -n "${cliff}" ]] || die '--cliff is required'
[[ -n "${config}" ]] || die '--config is required'

cliff=$(resolve_runfile "${cliff}")
config=$(resolve_runfile "${config}")
workspace=$(workspace_root)

if ! git -C "${workspace}" rev-parse --verify HEAD >/dev/null 2>&1; then
	exit 0
fi

cliff_args=(
	--config "${config}"
	--latest
	--strip header
	# Use native TLS so private CAs installed in the runner's system trust
	# store are honored if the changelog configuration performs API calls.
	--use-native-tls
)

if [[ "${CHANGELOG_OFFLINE:-1}" != "0" ]]; then
	cliff_args+=(--offline)
fi

cliff_args+=("${args[@]:index}")

cd "${workspace}"
exec "${cliff}" "${cliff_args[@]}"
