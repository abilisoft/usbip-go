#!/usr/bin/env bash

set -euo pipefail

die() {
	printf 'repo-coverage-test-runner: %s\n' "$*" >&2
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

normalize() {
	local path=$1
	path=${path#./}
	while [[ "${path}" == */../* ]]; do
		path=${path/\/..\//\/}
	done
	printf '%s\n' "${path}"
}

workspace_root() {
	local candidates=()
	local candidate
	local root

	[[ -n "${REPO_COVERAGE_WORKSPACE:-}" ]] && candidates+=("${REPO_COVERAGE_WORKSPACE}")
	[[ -n "${BUILD_WORKSPACE_DIRECTORY:-}" ]] && candidates+=("${BUILD_WORKSPACE_DIRECTORY}")
	[[ -n "${PWD:-}" ]] && candidates+=("${PWD}")
	candidates+=("$(runfiles_root)" ".")

	for candidate in "${candidates[@]}"; do
		root=$(git -C "${candidate}" rev-parse --show-toplevel 2>/dev/null) || continue
		# Bazel 9.1.1 exposes the real repository's .git directory as a
		# symlink in the execroot. Git then treats the generated execroot as
		# the worktree and recursively scans bazel-out while enumerating
		# untracked files. Resolve that source symlink back to the actual
		# checkout so coverage still includes real untracked source files
		# without walking generated runfile cycles.
		if [[ -L "${root}/.git" ]]; then
			local git_dir
			git_dir=$(readlink -f "${root}/.git")
			if [[ "${git_dir}" == */.git ]]; then
				local source_root=${git_dir%/.git}
				if git -C "${source_root}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
					printf '%s\n' "${source_root}"
					return
				fi
			fi
		fi
		printf '%s\n' "${root}"
		return
	done

	for candidate in "${REPO_COVERAGE_WORKSPACE:-}" "${BUILD_WORKSPACE_DIRECTORY:-}" "$(runfiles_root)" "."; do
		[[ -n "${candidate}" && -d "${candidate}" ]] || continue
		(cd "${candidate}" && pwd -P)
		return
	done

	die 'could not find workspace; set REPO_COVERAGE_WORKSPACE when running outside Bazel'
}

has_suffix() {
	local file=$1
	local suffix

	for suffix in "${suffixes[@]}"; do
		[[ "${file}" == *"${suffix}" ]] && return 0
	done

	return 1
}

is_excluded() {
	local file=$1
	local prefix

	for prefix in "${exclude_prefixes[@]}"; do
		[[ "${file}" == "${prefix}"* ]] && return 0
	done

	return 1
}

covered=()
suffixes=()
exclude_prefixes=()
args=()

if [[ -n "${REPO_COVERAGE_ARGS:-}" ]]; then
	while IFS= read -r -d '' arg; do
		args+=("${arg}")
	done < <(read_args_file "${REPO_COVERAGE_ARGS}")
fi
args+=("$@")

for arg in "${args[@]}"; do
	case "${arg}" in
	--covered=*)
		covered+=("$(normalize "${arg#--covered=}")")
		;;
	--include-suffix=*)
		suffixes+=("${arg#--include-suffix=}")
		;;
	--exclude-prefix=*)
		exclude_prefixes+=("${arg#--exclude-prefix=}")
		;;
	--workspace=*)
		export REPO_COVERAGE_WORKSPACE=${arg#--workspace=}
		;;
	*)
		die "unexpected argument: ${arg}"
		;;
	esac
done

((${#suffixes[@]} > 0)) || die 'at least one --include-suffix is required'

workspace=$(workspace_root)
declare -A covered_set=()
for file in "${covered[@]}"; do
	covered_set["${file}"]=1
done

workspace_files() {
	if git -C "${workspace}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
		git -C "${workspace}" ls-files -z --cached --others --exclude-standard
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

missing=()
while IFS= read -r -d '' file; do
	[[ -f "${workspace}/${file}" ]] || continue
	file=$(normalize "${file}")
	is_excluded "${file}" && continue
	has_suffix "${file}" || continue
	[[ -n "${covered_set[${file}]:-}" ]] && continue
	missing+=("${file}")
done < <(workspace_files)

if ((${#missing[@]} > 0)); then
	printf 'repo-coverage-test-runner: repo files missing from Bazel coverage:\n' >&2
	printf '%s\n' "${missing[@]}" | sort >&2
	exit 1
fi
