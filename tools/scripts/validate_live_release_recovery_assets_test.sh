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
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/validate_live_release_recovery_assets.sh"
		return
	fi

	printf '%s\n' "$(dirname "${BASH_SOURCE[0]}")/validate_live_release_recovery_assets.sh"
}

tmp=${TEST_TMPDIR:-$(mktemp -d)}
fake_bin="${tmp}/bin"
mkdir -p "${fake_bin}"

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

cat >"${fake_bin}/gh" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

if [[ ${FAKE_GH_MODE:-} == 'api_failure' ]]; then
	exit 1
fi

if [[ " $* " == *' --slurp '* && " $* " == *' --jq '* ]]; then
	exit 2
fi

endpoint=${2:-}
case "${endpoint}" in
*/releases/123) printf '123\tv1.0.2\ttrue\tfalse\n' ;;
*/releases/456) printf '123\tv1.0.2\ttrue\tfalse\n' ;;
*/releases/123/assets\?*) printf '%s\n' "${FAKE_ASSET_ROWS}" ;;
*)
	printf 'unexpected endpoint: %s\n' "${endpoint}" >&2
	exit 1
	;;
esac
EOF
chmod +x "${fake_bin}/gh"

run_case() {
	local name=$1
	local expected_status=$2
	local release_id=$3
	local mode=$4
	local expected_message=$5
	local output_file="${tmp}/${name}.out"
	local output
	local status=${exit_success}

	PATH="${fake_bin}:${PATH}" \
		FAKE_ASSET_ROWS="${asset_rows}" \
		FAKE_GH_MODE=${mode} \
		RECOVERY_EXPECTED_ASSETS_BASE64="${expected_assets_base64}" \
		RECOVERY_EXPECTED_SUBJECTS_BASE64="${expected_subjects_base64}" \
		RECOVERY_RELEASE_ID=${release_id} \
		RECOVERY_REPOSITORY=abilisoft/usbip-go \
		"$(validator_path)" >"${output_file}" 2>&1 || status=$?

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

run_case valid "${exit_success}" 123 '' 'validated 15 assets, 14 staged digests, and nine attested subject digests'
run_case wrong_live_id "${exit_failure}" 456 '' 'returned a different recovered draft release ID'
run_case api_failure "${exit_failure}" 123 api_failure 'unable to read the bound recovered draft release'
