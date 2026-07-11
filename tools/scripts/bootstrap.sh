#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly script_dir
repo_root="$(cd "${script_dir}/../.." && pwd)"
readonly repo_root
readonly tools_dir="${HARNESS_TOOLS_DIR:-${repo_root}/.local/tools}"
readonly bin_dir="${tools_dir}/bin"
readonly go_root="${tools_dir}/go"
readonly go_bin="${go_root}/bin/go"
readonly bazelisk_bin="${bin_dir}/bazelisk"
readonly bazelisk_version_file="${tools_dir}/bazelisk.version"
readonly bazelisk_home="${HARNESS_BAZELISK_HOME:-${repo_root}/.local/bazelisk}"
readonly bazelisk_version="1.29.0"
readonly bazel_output_user_root="${HARNESS_BAZEL_OUTPUT_USER_ROOT:-${repo_root}/.local/bazel}"

color_enabled() {
	[[ -t 2 && -z "${NO_COLOR:-}" && "${TERM:-}" != "dumb" ]]
}

if color_enabled; then
	readonly bold=$'\033[1m'
	readonly blue=$'\033[34m'
	readonly reset=$'\033[0m'
else
	readonly bold=''
	readonly blue=''
	readonly reset=''
fi

section() {
	printf '%s==>%s %s%s%s\n' "${bold}" "${reset}" "${blue}" "$1" "${reset}"
}

info() {
	printf '  %s%-18s%s %s\n' "${bold}" "$1" "${reset}" "$2"
}

require_cmd() {
	if ! command -v "$1" >/dev/null 2>&1; then
		printf 'missing required command: %s\n' "$1" >&2
		exit 1
	fi
}

download() {
	local url=$1
	local dest=$2

	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "${url}" -o "${dest}"
		return
	fi

	if command -v wget >/dev/null 2>&1; then
		wget -qO "${dest}" "${url}"
		return
	fi

	printf 'missing required command: curl or wget\n' >&2
	exit 1
}

verify_sha256() {
	local file=$1
	local expected=$2
	local actual

	require_cmd sha256sum
	actual=$(sha256sum "${file}")
	actual=${actual%% *}
	if [[ "${actual}" != "${expected}" ]]; then
		printf 'sha256 mismatch for %s: expected %s, got %s\n' "${file}" "${expected}" "${actual}" >&2
		exit 1
	fi
}

go_version() {
	awk '$1 == "go" { print $2; exit }' "${repo_root}/go.mod"
}

go_arch() {
	case "$(uname -m)" in
	x86_64 | amd64)
		printf 'amd64\n'
		;;
	aarch64 | arm64)
		printf 'arm64\n'
		;;
	*)
		printf 'unsupported Linux architecture: %s\n' "$(uname -m)" >&2
		exit 1
		;;
	esac
}

go_archive_sha256() {
	local version=$1
	local arch=$2

	case "${version}/${arch}" in
	1.26.5/amd64)
		printf '%s\n' "5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053"
		;;
	1.26.5/arm64)
		printf '%s\n' "fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49"
		;;
	*)
		printf 'Go bootstrap archive is not checksum-pinned: go%s linux-%s\n' "${version}" "${arch}" >&2
		exit 1
		;;
	esac
}

bazelisk_sha256() {
	local arch=$1

	case "${arch}" in
	amd64)
		printf '%s\n' "5a408715e932c0250d28bd84555f12edbf70117de42f9181691c736eacc4a992"
		;;
	arm64)
		printf '%s\n' "e20e8b0f4f240091b7a55bf17b9398bd4f40ee70ae0208dff95dd4c445fb4010"
		;;
	esac
}

installed_go_version() {
	if [[ ! -x "${go_bin}" ]]; then
		return 1
	fi

	GOTOOLCHAIN=local "${go_bin}" env GOVERSION | sed 's/^go//'
}

install_go() {
	local version arch url archive expected stage

	version="$(go_version)"
	if [[ "$(installed_go_version || true)" == "${version}" ]]; then
		return
	fi

	require_cmd tar

	arch="$(go_arch)"
	url="https://go.dev/dl/go${version}.linux-${arch}.tar.gz"
	archive="${tools_dir}/tmp/go${version}.linux-${arch}.tar.gz"
	expected=$(go_archive_sha256 "${version}" "${arch}")
	stage="${tools_dir}/tmp/go-${version}-${arch}"

	mkdir -p "${bin_dir}" "${tools_dir}/tmp"
	download "${url}" "${archive}"
	verify_sha256 "${archive}" "${expected}"
	rm -rf "${stage}"
	mkdir -p "${stage}"
	tar -C "${stage}" -xzf "${archive}"
	if [[ ! -x "${stage}/go/bin/go" ]]; then
		printf 'Go archive did not contain go/bin/go: %s\n' "${archive}" >&2
		exit 1
	fi
	rm -rf "${go_root}"
	mv "${stage}/go" "${go_root}"
	rm -rf "${stage}"
}

install_bazelisk() {
	local arch url download_path expected

	if [[ -x "${bazelisk_bin}" && -f "${bazelisk_version_file}" ]] &&
		[[ "$(<"${bazelisk_version_file}")" == "${bazelisk_version}" ]]; then
		return
	fi

	require_cmd install
	arch=$(go_arch)
	url="https://github.com/bazelbuild/bazelisk/releases/download/v${bazelisk_version}/bazelisk-linux-${arch}"
	download_path="${tools_dir}/tmp/bazelisk-${bazelisk_version}-linux-${arch}"
	expected=$(bazelisk_sha256 "${arch}")

	mkdir -p "${bin_dir}" "${tools_dir}/tmp"
	download "${url}" "${download_path}"
	verify_sha256 "${download_path}" "${expected}"
	install -m 0755 "${download_path}" "${bazelisk_bin}"
	printf '%s\n' "${bazelisk_version}" >"${bazelisk_version_file}"
}

print_environment() {
	section "tool environment"
	info workspace "${repo_root}"
	info tools "${tools_dir}"
	info bazelisk "${bazelisk_bin}"
	info bazelisk-home "${bazelisk_home}"
	info bazel-root "${bazel_output_user_root}"
	info bazel-cache "${repo_root}/.local/bazel-disk-cache"
	info bootstrap-go "${go_bin}"
}

install_go
install_bazelisk
print_environment
