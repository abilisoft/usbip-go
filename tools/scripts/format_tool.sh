#!/usr/bin/env bash

set -euo pipefail

readonly workspace="${BUILD_WORKSPACE_DIRECTORY:-$(pwd)}"
readonly name="__NAME__"
readonly display_name="${name#format_}"
readonly tool="__TOOL__"
readonly runfiles_name="__RUNFILES_NAME__"
readonly quiet_stdout="__QUIET_STDOUT__"
readonly args=(__ARGS__)
readonly extensions=(__EXTENSIONS__)
readonly excluded_prefixes=("vendor/")

execroot() {
	local self

	self="${BASH_SOURCE[0]}"
	self="$(readlink -f "${self}" 2>/dev/null || printf '%s' "${self}")"
	cd "$(dirname "${self}")/../../../.." && pwd
}

color_enabled() {
	[[ -t 1 && -z "${NO_COLOR:-}" && "${TERM:-}" != "dumb" ]]
}

if color_enabled; then
	readonly bold=$'\033[1m'
	readonly dim=$'\033[2m'
	readonly green=$'\033[32m'
	readonly reset=$'\033[0m'
else
	readonly bold=''
	readonly dim=''
	readonly green=''
	readonly reset=''
fi

print_summary() {
	if (($# == 2)); then
		printf '%s✓%s %s%s%s %s%d files%s\n' \
			"${green}" "${reset}" \
			"${bold}" "$1" "${reset}" \
			"${dim}" "$2" "${reset}"
		return
	fi

	printf '%s✓%s %s%s%s\n' "${green}" "${reset}" "${bold}" "$1" "${reset}"
}

matches_extension() {
	local file=$1
	local extension

	for extension in "${extensions[@]}"; do
		if [[ "${file}" == *."${extension}" ]]; then
			return 0
		fi
	done

	return 1
}

is_excluded() {
	local file=$1
	local prefix

	for prefix in "${excluded_prefixes[@]}"; do
		[[ "${file}" == "${prefix}"* ]] && return 0
	done

	return 1
}

selected_files() {
	local file

	if ((${#extensions[@]} == 0)); then
		return
	fi

	while IFS= read -r -d '' file; do
		if [[ -e "${workspace}/${file}" ]] && ! is_excluded "${file}" && matches_extension "${file}"; then
			printf '%s\0' "${file}"
		fi
	done < <(workspace_files)
}

workspace_files() {
	if git -C "${workspace}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
		git -C "${workspace}" ls-files --cached --others --exclude-standard -z
		return
	fi

	(
		cd "${workspace}"
		find . \
			\( -path './.git' -o -path './.git/*' \
			-o -path './.local' -o -path './.local/*' \
			-o -path './bazel-*' -o -path './bazel-*/*' \
			-o -path './build' -o -path './build/*' \
			-o -path './dist' -o -path './dist/*' \) -prune \
			-o -type f -printf '%P\0'
	)
}

run_tool() {
	local resolved_tool
	local tool_runfiles_dir

	if [[ "${tool}" == /* ]]; then
		resolved_tool="${tool}"
	else
		resolved_tool="$(execroot)/${tool}"
	fi

	if [[ -n "${runfiles_name}" ]]; then
		tool_runfiles_dir="${RUNFILES_DIR:-}"
		if [[ -z "${tool_runfiles_dir}" ]]; then
			tool_runfiles_dir="${BASH_SOURCE[0]}.runfiles"
		fi
		RUNFILES_DIR="${tool_runfiles_dir}" "${resolved_tool}" "${@:2}"
		return
	fi

	"${resolved_tool}" "${@:2}"
}

cd "${workspace}"
export BUILD_WORKSPACE_DIRECTORY="${workspace}"

files=()
mapfile -d '' -t files < <(selected_files)

if ((${#extensions[@]} > 0 && ${#files[@]} == 0)); then
	print_summary "${display_name}" 0
	exit 0
fi

if [[ "${quiet_stdout}" == "1" ]]; then
	run_tool "${tool}" "${args[@]}" "${files[@]}" >/dev/null 2>&1
else
	run_tool "${tool}" "${args[@]}" "${files[@]}"
fi

if ((${#extensions[@]} > 0)); then
	print_summary "${display_name}" "${#files[@]}"
else
	print_summary "${display_name}"
fi
