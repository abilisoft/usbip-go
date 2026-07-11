#!/usr/bin/env bash

set -euo pipefail

die() {
	printf 'goreleaser-runner: %s\n' "$*" >&2
	exit 1
}

runfiles_root() {
	if [[ -n "${RUNFILES_DIR:-}" && -d "${RUNFILES_DIR}/_main" ]]; then
		printf '%s\n' "${RUNFILES_DIR}/_main"
		return
	fi

	if [[ -d "${BASH_SOURCE[0]}.runfiles/_main" ]]; then
		printf '%s\n' "${BASH_SOURCE[0]}.runfiles/_main"
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

args=()
if [[ -n "${GORELEASER_RUNNER_ARGS:-}" ]]; then
	while IFS= read -r -d '' arg; do
		args+=("${arg}")
	done < <(read_args_file "${GORELEASER_RUNNER_ARGS}")
fi
args+=("$@")

root=$(runfiles_root)
goreleaser=''
path_dirs=()
index=0

while ((index < ${#args[@]})); do
	arg=${args[index]}
	case "${arg}" in
	--)
		((index += 1))
		break
		;;
	--goreleaser=*)
		goreleaser=${arg#--goreleaser=}
		;;
	--path-prepend=*)
		path_dirs+=("${arg#--path-prepend=}")
		;;
	*)
		break
		;;
	esac
	((index += 1))
done

[[ -n "${goreleaser}" ]] || die '--goreleaser is required'

goreleaser_path="${root}/${goreleaser}"
[[ -x "${goreleaser_path}" ]] || die "goreleaser is not executable: ${goreleaser}"

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

workspace=${BUILD_WORKSPACE_DIRECTORY:-$(pwd)}
cd "${workspace}"

export GOMODCACHE="${GOMODCACHE:-${workspace}/.local/go-mod}"
export GOCACHE="${GOCACHE:-${workspace}/.local/go-build-cache}"
export GOTMPDIR="${GOTMPDIR:-${workspace}/.local/go-tmp}"
export GOFLAGS="${GOFLAGS:--mod=readonly}"
mkdir -p "${GOMODCACHE}" "${GOCACHE}" "${GOTMPDIR}"

extra_args=()
if [[ -n "${RELEASE_NOTES:-}" ]]; then
	extra_args+=("--release-notes=${RELEASE_NOTES}")
fi

exec "${goreleaser_path}" "${args[@]:index}" "${extra_args[@]}"
