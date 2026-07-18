#!/usr/bin/env bash

set -euo pipefail

readonly exit_failure=1
readonly exit_success=0
readonly fixed_controller_commit='1111111111111111111111111111111111111111'
readonly fixed_tag_object='f0c7083fdee40e1e31ebc170992fa5f43efe8d60'
readonly fixed_target_commit='72aa5a6b585d1f5b6230c8362254ea2a6296ec75'

validator_path() {
	if [[ -n ${TEST_SRCDIR:-} && -n ${TEST_WORKSPACE:-} ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/validate_live_release_recovery.sh"
		return
	fi

	printf '%s\n' "$(dirname "${BASH_SOURCE[0]}")/validate_live_release_recovery.sh"
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
		printf 'commit\t%s\n' "${FAKE_TARGET_COMMIT}"
	else
		printf 'tag\t%s\n' "${FAKE_TAG_OBJECT}"
	fi
	;;
*/git/tags/*)
	verified=true
	if [[ ${FAKE_GH_MODE:-} == 'unverified' ]]; then
		verified=false
	fi
	printf 'v1.0.2\tcommit\t%s\t%s\n' "${FAKE_TARGET_COMMIT}" "${verified}"
	;;
*/releases\?*)
	case "${FAKE_GH_MODE:-}" in
	draft) printf '1\t0\t123\n' ;;
	public) printf '0\t1\t\n' ;;
	*) printf '0\t0\t\n' ;;
	esac
	;;
*)
	printf 'unexpected endpoint: %s\n' "${endpoint}" >&2
	exit 1
	;;
esac
EOF

cat >"${fake_bin}/git" <<'EOF'
#!/usr/bin/env bash

set -euo pipefail

if [[ ${FAKE_GIT_MODE:-} == 'failure' ]]; then
	exit 1
fi

path=${2:-}
operation=${3:-}
argument=${4:-}
if [[ ${operation} != 'rev-parse' ]]; then
	exit 1
fi

case "${path}:${argument}" in
controller:HEAD) printf '%s\n' "${FAKE_CONTROLLER_COMMIT}" ;;
source:HEAD)
	if [[ ${FAKE_GIT_MODE:-} == 'wrong_source' ]]; then
		printf '%s\n' 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
	else
		printf '%s\n' "${FAKE_TARGET_COMMIT}"
	fi
	;;
source:refs/tags/v1.0.2) printf '%s\n' "${FAKE_TAG_OBJECT}" ;;
source:refs/tags/v1.0.2\^\{commit\}) printf '%s\n' "${FAKE_TARGET_COMMIT}" ;;
*) exit 1 ;;
esac
EOF

chmod +x "${fake_bin}/gh" "${fake_bin}/git"

run_case() {
	local name=$1
	local expected_status=$2
	local mode=$3
	local git_mode=$4
	local expected_message=$5
	local output_file="${tmp}/${name}.out"
	local output
	local status=${exit_success}

	PATH="${fake_bin}:${PATH}" \
		FAKE_CONTROLLER_COMMIT=${fixed_controller_commit} \
		FAKE_GH_MODE=${mode} \
		FAKE_GIT_MODE=${git_mode} \
		FAKE_TAG_OBJECT=${fixed_tag_object} \
		FAKE_TARGET_COMMIT=${fixed_target_commit} \
		RECOVERY_CONTROLLER_COMMIT=${fixed_controller_commit} \
		RECOVERY_CONTROLLER_PATH=controller \
		RECOVERY_CONTROLLER_REF=refs/heads/main \
		RECOVERY_REPOSITORY=abilisoft/usbip-go \
		RECOVERY_REQUIRED_RELEASE_STATE=preflight \
		RECOVERY_SOURCE_PATH=source \
		RECOVERY_TAG=v1.0.2 \
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

run_case absent "${exit_success}" '' '' 'validated immutable release recovery'
run_case draft "${exit_success}" draft '' 'validated immutable release recovery'
run_case public "${exit_failure}" public '' 'recovery replay is forbidden'
run_case lightweight "${exit_failure}" lightweight '' 'must point to an annotated tag object'
run_case unverified "${exit_failure}" unverified '' 'signature must be verified by GitHub'
run_case wrong_source "${exit_failure}" '' wrong_source 'source does not match the fixed source commit'
run_case api_failure "${exit_failure}" api_failure '' 'unable to read the live recovery tag ref'
run_case git_failure "${exit_failure}" '' failure 'unable to resolve the checked-out recovery controller commit'
