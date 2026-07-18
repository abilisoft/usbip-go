#!/usr/bin/env bash

set -euo pipefail

readonly fixed_release_tag='v1.0.2'
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
readonly expected_staged_assets=(
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

fail() {
	printf '::error::%s\n' "$1" >&2
	exit 1
}

is_expected_asset() {
	local candidate=$1
	local expected

	for expected in "${expected_assets[@]}"; do
		[[ ${candidate} == "${expected}" ]] && return 0
	done
	return 1
}

is_expected_subject() {
	local candidate=$1
	local expected

	for expected in "${expected_subjects[@]}"; do
		[[ ${candidate} == "${expected}" ]] && return 0
	done
	return 1
}

is_expected_staged_asset() {
	local candidate=$1
	local expected

	for expected in "${expected_staged_assets[@]}"; do
		[[ ${candidate} == "${expected}" ]] && return 0
	done
	return 1
}

if [[ ! ${RECOVERY_RELEASE_ID:-} =~ ^[1-9][0-9]*$ ]]; then
	fail 'recovered draft release ID must be a positive integer'
fi

if [[ ${RECOVERY_RELEASE_TAG:-} != "${fixed_release_tag}" ]]; then
	fail "recovered draft must target ${fixed_release_tag}"
fi

if [[ ${RECOVERY_RELEASE_DRAFT:-} != 'true' ]]; then
	fail 'recovered release must remain a draft during asset validation'
fi

if [[ ${RECOVERY_RELEASE_PRERELEASE:-} != 'false' ]]; then
	fail 'recovered draft must not be a prerelease'
fi

if [[ -z ${RECOVERY_EXPECTED_SUBJECTS_BASE64:-} ]]; then
	fail 'expected recovered SLSA subject hashes are required'
fi

if [[ -z ${RECOVERY_EXPECTED_ASSETS_BASE64:-} ]]; then
	fail 'expected recovered staged release asset hashes are required'
fi

decoded_subjects=$(printf '%s' "${RECOVERY_EXPECTED_SUBJECTS_BASE64}" | base64 --decode) ||
	fail 'expected recovered SLSA subject hashes are not valid base64'

declare -A expected_subject_hashes=()
while IFS= read -r subject_line; do
	if [[ ! ${subject_line} =~ ^([0-9a-f]{64})[[:space:]]+([^[:space:]]+)$ ]]; then
		fail 'expected recovered SLSA subject hash line is malformed'
	fi
	subject_hash=${BASH_REMATCH[1]}
	subject_name=${BASH_REMATCH[2]}
	if ! is_expected_subject "${subject_name}"; then
		fail "unexpected recovered SLSA subject: ${subject_name}"
	fi
	if [[ -n ${expected_subject_hashes[${subject_name}]+present} ]]; then
		fail "duplicate expected recovered SLSA subject: ${subject_name}"
	fi
	expected_subject_hashes[${subject_name}]=${subject_hash}
done <<<"${decoded_subjects}"

if ((${#expected_subject_hashes[@]} != ${#expected_subjects[@]})); then
	fail 'recovered SLSA subject roster must contain exactly nine artifacts'
fi

for subject_name in "${expected_subjects[@]}"; do
	if [[ -z ${expected_subject_hashes[${subject_name}]+present} ]]; then
		fail "missing expected recovered SLSA subject: ${subject_name}"
	fi
done

decoded_staged_assets=$(printf '%s' "${RECOVERY_EXPECTED_ASSETS_BASE64}" | base64 --decode) ||
	fail 'expected recovered staged release asset hashes are not valid base64'

declare -A expected_staged_asset_hashes=()
while IFS= read -r staged_asset_line; do
	if [[ ! ${staged_asset_line} =~ ^([0-9a-f]{64})[[:space:]]+([^[:space:]]+)$ ]]; then
		fail 'expected recovered staged release asset hash line is malformed'
	fi
	staged_asset_hash=${BASH_REMATCH[1]}
	staged_asset_name=${BASH_REMATCH[2]}
	if ! is_expected_staged_asset "${staged_asset_name}"; then
		fail "unexpected recovered staged release asset: ${staged_asset_name}"
	fi
	if [[ -n ${expected_staged_asset_hashes[${staged_asset_name}]+present} ]]; then
		fail "duplicate expected recovered staged release asset: ${staged_asset_name}"
	fi
	expected_staged_asset_hashes[${staged_asset_name}]=${staged_asset_hash}
done <<<"${decoded_staged_assets}"

if ((${#expected_staged_asset_hashes[@]} != ${#expected_staged_assets[@]})); then
	fail 'recovered staged release asset roster must contain exactly 14 artifacts'
fi

for staged_asset_name in "${expected_staged_assets[@]}"; do
	if [[ -z ${expected_staged_asset_hashes[${staged_asset_name}]+present} ]]; then
		fail "missing expected recovered staged release asset: ${staged_asset_name}"
	fi
done

if [[ -z ${RECOVERY_ASSET_ROWS:-} ]]; then
	fail 'recovered draft asset metadata is required'
fi

declare -A asset_digests=()
declare -A asset_names=()
while IFS=$'\t' read -r asset_id asset_name asset_size asset_digest asset_state; do
	if [[ ! ${asset_id} =~ ^[1-9][0-9]*$ ]]; then
		fail "recovered draft asset has invalid ID: ${asset_name:-unknown}"
	fi
	if ! is_expected_asset "${asset_name}"; then
		fail "recovered draft contains an unexpected asset: ${asset_name:-unknown}"
	fi
	if [[ -n ${asset_names[${asset_name}]+present} ]]; then
		fail "recovered draft asset name is duplicated: ${asset_name}"
	fi
	if [[ ! ${asset_size} =~ ^[1-9][0-9]*$ ]]; then
		fail "recovered draft asset is empty: ${asset_name}"
	fi
	if [[ ! ${asset_digest} =~ ^sha256:([0-9a-f]{64})$ ]]; then
		fail "recovered draft asset lacks a valid SHA-256 digest: ${asset_name}"
	fi
	asset_sha256=${BASH_REMATCH[1]}
	if [[ ${asset_state} != 'uploaded' ]]; then
		fail "recovered draft asset is not fully uploaded: ${asset_name}"
	fi
	asset_names[${asset_name}]=${asset_id}
	asset_digests[${asset_name}]=${asset_sha256}
done <<<"${RECOVERY_ASSET_ROWS}"

if ((${#asset_names[@]} != ${#expected_assets[@]})); then
	fail 'recovered draft must contain exactly 15 assets'
fi

for asset_name in "${expected_assets[@]}"; do
	if [[ -z ${asset_names[${asset_name}]+present} ]]; then
		fail "recovered draft is missing required asset: ${asset_name}"
	fi
done

for subject_name in "${expected_subjects[@]}"; do
	if [[ ${asset_digests[${subject_name}]} != "${expected_subject_hashes[${subject_name}]}" ]]; then
		fail "published asset digest does not match the attested subject: ${subject_name}"
	fi
done

for staged_asset_name in "${expected_staged_assets[@]}"; do
	if [[ ${asset_digests[${staged_asset_name}]} != "${expected_staged_asset_hashes[${staged_asset_name}]}" ]]; then
		fail "published asset digest does not match the staged release asset: ${staged_asset_name}"
	fi
done

printf 'validated 15 assets, 14 staged digests, and nine attested subject digests for release %s\n' \
	"${RECOVERY_RELEASE_ID}"
