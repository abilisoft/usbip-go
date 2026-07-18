#!/usr/bin/env bash

set -euo pipefail

readonly exit_failure=1
readonly exit_success=0
readonly valid_other_digest='ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff'
readonly expected_assets=(
	multiple.intoto.jsonl
	usbip-go_1.0.2_checksums.txt
	usbip-go_1.0.2_checksums.txt.sigstore.json
	usbip-go_1.0.2_linux_amd64.deb
	usbip-go_1.0.2_linux_amd64.rpm
	usbip-go_1.0.2_linux_amd64.tar.gz
	usbip-go_1.0.2_linux_amd64.tar.gz.sbom.json
	usbip-go_1.0.2_linux_arm64.deb
	usbip-go_1.0.2_linux_arm64.rpm
	usbip-go_1.0.2_linux_arm64.tar.gz
	usbip-go_1.0.2_linux_arm64.tar.gz.sbom.json
	usbip-go_1.0.2_linux_armv7.deb
	usbip-go_1.0.2_linux_armv7.rpm
	usbip-go_1.0.2_linux_armv7.tar.gz
	usbip-go_1.0.2_linux_armv7.tar.gz.sbom.json
)
readonly expected_subjects=(
	usbip-go_1.0.2_linux_amd64.deb
	usbip-go_1.0.2_linux_amd64.rpm
	usbip-go_1.0.2_linux_amd64.tar.gz
	usbip-go_1.0.2_linux_arm64.deb
	usbip-go_1.0.2_linux_arm64.rpm
	usbip-go_1.0.2_linux_arm64.tar.gz
	usbip-go_1.0.2_linux_armv7.deb
	usbip-go_1.0.2_linux_armv7.rpm
	usbip-go_1.0.2_linux_armv7.tar.gz
)

validator_path() {
	if [[ -n ${TEST_SRCDIR:-} && -n ${TEST_WORKSPACE:-} ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/validate_release_recovery_assets.sh"
		return
	fi

	printf '%s\n' "$(dirname "${BASH_SOURCE[0]}")/validate_release_recovery_assets.sh"
}

tmp=${TEST_TMPDIR:-$(mktemp -d)}
subject_lines=''
asset_rows=''
staged_asset_lines=''
declare -A subject_hashes=()

for index in "${!expected_subjects[@]}"; do
	subject_name=${expected_subjects[${index}]}
	subject_hash=$(printf '%064x' "$((index + 1))")
	subject_hashes[${subject_name}]=${subject_hash}
	subject_lines+="${subject_hash}  ${subject_name}"$'\n'
done
subject_lines=${subject_lines%$'\n'}
expected_subjects_base64=$(printf '%s' "${subject_lines}" | base64 -w0)

for index in "${!expected_assets[@]}"; do
	asset_name=${expected_assets[${index}]}
	asset_hash=${subject_hashes[${asset_name}]:-${valid_other_digest}}
	asset_rows+="$((index + 100))"$'\t'"${asset_name}"$'\t100\t'"sha256:${asset_hash}"$'\tuploaded\n'
	if [[ ${asset_name} != 'multiple.intoto.jsonl' ]]; then
		staged_asset_lines+="${asset_hash}  ${asset_name}"$'\n'
	fi
done
asset_rows=${asset_rows%$'\n'}
staged_asset_lines=${staged_asset_lines%$'\n'}
expected_assets_base64=$(printf '%s' "${staged_asset_lines}" | base64 -w0)

run_case() {
	local name=$1
	local expected_status=$2
	local expected_message=$3
	shift 3
	local output_file="${tmp}/${name}.out"
	local output
	local status=${exit_success}
	local baseline=(
		"RECOVERY_RELEASE_ID=123"
		"RECOVERY_RELEASE_TAG=v1.0.2"
		"RECOVERY_RELEASE_DRAFT=true"
		"RECOVERY_RELEASE_PRERELEASE=false"
		"RECOVERY_EXPECTED_ASSETS_BASE64=${expected_assets_base64}"
		"RECOVERY_EXPECTED_SUBJECTS_BASE64=${expected_subjects_base64}"
		"RECOVERY_ASSET_ROWS=${asset_rows}"
	)

	env "${baseline[@]}" "$@" "$(validator_path)" >"${output_file}" 2>&1 || status=$?

	if [[ ${status} -ne ${expected_status} ]]; then
		printf '%s: expected status %d, got %d\n' \
			"${name}" "${expected_status}" "${status}" >&2
		printf '%s\n' "$(<"${output_file}")" >&2
		exit "${exit_failure}"
	fi

	output=$(<"${output_file}")
	if [[ ${output} != *"${expected_message}"* ]]; then
		printf '%s: expected output to contain %q\n' "${name}" "${expected_message}" >&2
		printf '%s\n' "${output}" >&2
		exit "${exit_failure}"
	fi
}

wrong_subject_rows=${asset_rows/sha256:${subject_hashes[${expected_subjects[0]}]}/sha256:${valid_other_digest}}
wrong_staged_rows=${asset_rows//sha256:${valid_other_digest}/sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee}
missing_asset_rows=$(printf '%s\n' "${asset_rows}" | grep -F -v $'\tusbip-go_1.0.2_linux_armv7.rpm\t')
unexpected_asset_rows=${asset_rows/usbip-go_1.0.2_linux_armv7.rpm/unexpected.rpm}
empty_asset_rows=${asset_rows/$'\t100\t'/$'\t0\t'}
pending_asset_rows=${asset_rows/%uploaded/pending}
malformed_subjects_base64=$(printf '%s' 'not-a-hash  artifact' | base64 -w0)

run_case valid "${exit_success}" 'validated 15 assets, 14 staged digests, and nine attested subject digests'
run_case wrong_release_id "${exit_failure}" 'release ID must be a positive integer' \
	RECOVERY_RELEASE_ID=0
run_case wrong_tag "${exit_failure}" 'must target v1.0.2' RECOVERY_RELEASE_TAG=v1.0.3
run_case public_release "${exit_failure}" 'must remain a draft' RECOVERY_RELEASE_DRAFT=false
run_case prerelease "${exit_failure}" 'must not be a prerelease' RECOVERY_RELEASE_PRERELEASE=true
run_case malformed_subjects "${exit_failure}" 'subject hash line is malformed' \
	RECOVERY_EXPECTED_SUBJECTS_BASE64="${malformed_subjects_base64}"
run_case missing_asset "${exit_failure}" 'must contain exactly 15 assets' \
	RECOVERY_ASSET_ROWS="${missing_asset_rows}"
run_case unexpected_asset "${exit_failure}" 'contains an unexpected asset' \
	RECOVERY_ASSET_ROWS="${unexpected_asset_rows}"
run_case empty_asset "${exit_failure}" 'asset is empty' RECOVERY_ASSET_ROWS="${empty_asset_rows}"
run_case pending_asset "${exit_failure}" 'asset is not fully uploaded' \
	RECOVERY_ASSET_ROWS="${pending_asset_rows}"
run_case wrong_subject_digest "${exit_failure}" 'does not match the attested subject' \
	RECOVERY_ASSET_ROWS="${wrong_subject_rows}"
run_case wrong_staged_digest "${exit_failure}" 'does not match the staged release asset' \
	RECOVERY_ASSET_ROWS="${wrong_staged_rows}"
