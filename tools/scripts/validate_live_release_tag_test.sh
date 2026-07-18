#!/usr/bin/env bash

set -euo pipefail

readonly exit_failure=1
readonly exit_success=0
readonly expected_commit='0123456789abcdef0123456789abcdef01234567'

validator_path() {
	if [[ -n ${TEST_SRCDIR:-} && -n ${TEST_WORKSPACE:-} ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/validate_live_release_tag.sh"
		return
	fi

	printf '%s\n' "$(dirname "${BASH_SOURCE[0]}")/validate_live_release_tag.sh"
}

tmp=${TEST_TMPDIR:-$(mktemp -d)}
fake_bin="${tmp}/bin"
mkdir -p "${fake_bin}"

cat >"${fake_bin}/gh" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

if [[ ${FAKE_GH_MODE:-} == 'api_failure' ]]; then
	exit 1
fi

endpoint=${2:-}
case "${endpoint}" in
*/git/ref/tags/*)
	if [[ ${FAKE_GH_MODE:-} == 'lightweight' ]]; then
		printf 'commit\t%s\n' "${FAKE_COMMIT}"
	else
		printf 'tag\t%s\n' 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
	fi
	;;
*/git/tags/*)
	verified=true
	target=${FAKE_COMMIT}
	if [[ ${FAKE_GH_MODE:-} == 'unverified' ]]; then
		verified=false
	elif [[ ${FAKE_GH_MODE:-} == 'moved' ]]; then
		target='1123456789abcdef0123456789abcdef01234567'
	fi
	printf '%s\tcommit\t%s\t%s\n' "${FAKE_TAG}" "${target}" "${verified}"
	;;
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
	local mode=$3
	local expected_message=$4
	local output_file="${tmp}/${name}.out"
	local output
	local status=${exit_success}

	PATH="${fake_bin}:${PATH}" \
		FAKE_COMMIT=${expected_commit} \
		FAKE_GH_MODE=${mode} \
		FAKE_TAG=v1.2.3 \
		RELEASE_DEFAULT_BRANCH_COMMIT=${expected_commit} \
		RELEASE_EVENT_AFTER=${expected_commit} \
		RELEASE_EVENT_CREATED=true \
		RELEASE_EVENT_DELETED=false \
		RELEASE_EVENT_FORCED=false \
		RELEASE_REPOSITORY=abilisoft/usbip-go \
		RELEASE_TAG=v1.2.3 \
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

run_case fresh "${exit_success}" '' 'validated fresh stable release tag v1.2.3'
run_case lightweight "${exit_failure}" lightweight 'release ref must point to an annotated tag object'
run_case unverified "${exit_failure}" unverified 'signature must be verified by GitHub'
run_case moved "${exit_failure}" moved 'live annotated tag target does not match the release push event target'
run_case api_failure "${exit_failure}" api_failure ''
