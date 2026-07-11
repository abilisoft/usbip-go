#!/usr/bin/env bash

set -euo pipefail

die() {
	printf 'toolwrap-test-runner: %s\n' "$*" >&2
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

	if [[ -n "${BUILD_WORKSPACE_DIRECTORY:-}" ]]; then
		printf '%s\n' "${BUILD_WORKSPACE_DIRECTORY}"
		return
	fi

	printf '.\n'
}

resolve_args_file() {
	local path=$1
	local root

	if [[ "${path}" = /* && -f "${path}" ]]; then
		printf '%s\n' "${path}"
		return
	fi

	root=$(runfiles_root)
	if [[ -f "${root}/${path}" ]]; then
		printf '%s\n' "${root}/${path}"
		return
	fi

	if [[ -f "${path}" ]]; then
		printf '%s\n' "${path}"
		return
	fi

	die "args file not found: ${path}"
}

read_args_file() {
	local path=$1
	local line
	path=$(resolve_args_file "${path}")

	while IFS= read -r line || [[ -n "${line}" ]]; do
		[[ -z "${line//[[:space:]]/}" ]] && continue
		printf '%s\0' "${line}"
	done <"${path}"
}

stage_file() {
	local source=$1
	local destination=$2

	mkdir -p "$(dirname "${destination}")"
	cp -pL "${source}" "${destination}"
}

stage_dereferenced_tree() {
	local source_root=$1
	local stage_parent=${TEST_TMPDIR:-/tmp}
	local stage
	local source
	local rel

	stage=$(mktemp -d "${stage_parent%/}/toolwrap-stage-XXXXXXXX")

	while IFS= read -r -d '' source; do
		rel=${source#"${source_root}/"}
		case "${rel}" in
		vendor/* | *.go | *.yaml | *.yml | *.toml | *.json | *.work | go.mod | go.sum | */go.mod | */go.sum)
			stage_file "${source}" "${stage}/${rel}"
			;;
		esac
	done < <(find -L "${source_root}" -type f -print0 2>/dev/null)

	printf '%s\n' "${stage}"
}

args=()
if [[ -n "${TOOLWRAP_ARGS:-}" ]]; then
	while IFS= read -r -d '' arg; do
		args+=("${arg}")
	done < <(read_args_file "${TOOLWRAP_ARGS}")
fi
args+=("$@")

root=$(runfiles_root)
tool=''
dereference=false
path_dirs=()
index=0

while ((index < ${#args[@]})); do
	arg=${args[index]}
	case "${arg}" in
	--)
		((index += 1))
		break
		;;
	--tool=*)
		tool=${arg#--tool=}
		;;
	--path-prepend=*)
		path_dirs+=("${arg#--path-prepend=}")
		;;
	--dereference=true)
		dereference=true
		;;
	--dereference=false)
		dereference=false
		;;
	*)
		break
		;;
	esac
	((index += 1))
done

[[ -n "${tool}" ]] || die '--tool is required'

tool_args=("${args[@]:index}")
tool_path="${root}/${tool}"
[[ -x "${tool_path}" ]] || die "tool is not executable: ${tool}"

if ((${#path_dirs[@]} > 0)); then
	path_prefix=''
	for path_dir in "${path_dirs[@]}"; do
		if [[ -z "${path_prefix}" ]]; then
			path_prefix="${root}/${path_dir}"
		else
			path_prefix="${path_prefix}:${root}/${path_dir}"
		fi
	done
	export PATH="${path_prefix}${PATH:+:${PATH}}"
fi

workdir=${root}
if [[ "${dereference}" == true ]]; then
	workdir=$(stage_dereferenced_tree "${root}")
fi

cd "${workdir}"
exec "${tool_path}" "${tool_args[@]}"
