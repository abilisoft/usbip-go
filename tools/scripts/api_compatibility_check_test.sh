#!/usr/bin/env bash

set -euo pipefail

readonly exit_failure=1
readonly exit_success=0
readonly git_author_email='api-check@example.invalid'
readonly git_author_name='API Check Test'

script_dir=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir

script_path() {
	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/api_compatibility_check.sh"
		return
	fi

	printf '%s\n' "${script_dir}/api_compatibility_check.sh"
}

tmp=${TEST_TMPDIR:-$(mktemp -d)}
fake_bin="${tmp}/bin"
mkdir -p "${fake_bin}"

cat >"${fake_bin}/apidiff" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${APIDIFF_MODE}" == 'incompatible' ]]; then
	printf 'Incompatible changes:\n- removed symbol\n'
else
	printf 'Compatible changes:\n- added symbol\n'
fi
FAKE
chmod +x "${fake_bin}/apidiff"

run_case() {
	local name="$1"
	local expected_status="$2"
	local subject="$3"
	local body="$4"
	local apidiff_mode="$5"
	local repo="${tmp}/${name}"

	mkdir -p "${repo}/api"
	git -C "${repo}" init -q
	git -C "${repo}" config user.name "${git_author_name}"
	git -C "${repo}" config user.email "${git_author_email}"
	git -C "${repo}" config commit.gpgsign false
	: >"${repo}/api/pkg_usbip.json"
	: >"${repo}/api/pkg_domain.json"
	printf 'baseline\n' >"${repo}/state"
	git -C "${repo}" add .
	git -C "${repo}" commit -qm 'chore: establish baseline'
	printf 'changed\n' >>"${repo}/state"
	git -C "${repo}" add state
	if [[ -n "${body}" ]]; then
		git -C "${repo}" commit -qm "${subject}" -m "${body}"
	else
		git -C "${repo}" commit -qm "${subject}"
	fi

	local status=${exit_success}
	(
		cd "${repo}"
		APIDIFF_MODE="${apidiff_mode}" PATH="${fake_bin}:${PATH}" \
			"$(script_path)" >/dev/null 2>&1
	) || status=$?

	if [[ ${status} -ne ${expected_status} ]]; then
		printf '%s: expected status %d, got %d\n' \
			"${name}" "${expected_status}" "${status}" >&2
		exit "${exit_failure}"
	fi
}

run_case subject_marker "${exit_success}" \
	'feat(api)!: remove obsolete symbol' '' incompatible
run_case footer_marker "${exit_success}" \
	'feat(api): remove obsolete symbol' 'BREAKING CHANGE: remove obsolete symbol' incompatible
run_case hyphenated_footer_marker "${exit_success}" \
	'feat(api): remove obsolete symbol' 'BREAKING-CHANGE: remove obsolete symbol' incompatible
run_case legacy_marker "${exit_failure}" \
	'BREAKING: remove obsolete symbol' '' incompatible
run_case missing_marker "${exit_failure}" \
	'feat(api): remove obsolete symbol' '' incompatible
run_case compatible_change "${exit_success}" \
	'feat(api): add symbol' '' compatible
