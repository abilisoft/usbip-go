#!/usr/bin/env bash

set -euo pipefail

script_path() {
	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/tools/scripts/start_release.sh"
		return
	fi

	printf '%s\n' "$(dirname "${BASH_SOURCE[0]}")/start_release.sh"
}

readonly test_branch='main'
readonly test_repository='abilisoft/usbip-go'
readonly test_sha='0123456789abcdef0123456789abcdef01234567'
readonly test_stale_sha='abcdef0123456789abcdef0123456789abcdef01'
readonly test_tag='v1.2.3'

tmp=${TEST_TMPDIR:-$(mktemp -d)}
fake_bin="${tmp}/bin"
log="${tmp}/gh.log"
mkdir -p "${fake_bin}"

cat >"${fake_bin}/gh" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >>"${START_RELEASE_TEST_LOG}"

case "$*" in
*"git/ref/heads/"*)
	head_read_count=$(grep -Fc "git/ref/heads/" "${START_RELEASE_TEST_LOG}")
	case "${START_RELEASE_TEST_SCENARIO}" in
		head-failure) exit 1 ;;
		head-failure-after-create)
			((head_read_count == 1)) || exit 1
			;;
		empty-head) exit 0 ;;
		empty-head-after-create)
			((head_read_count == 1)) || exit 0
			;;
		head-advanced-after-create)
			if ((head_read_count > 1)); then
				printf '%s\n' "${START_RELEASE_TEST_HEAD_SHA_AFTER_CREATE}"
				exit 0
			fi
			;;
	esac
	printf '%s\n' "${START_RELEASE_TEST_HEAD_SHA}"
	;;
"api --method POST "*)
	[[ ${START_RELEASE_TEST_SCENARIO} != create-failure ]] || exit 1
	;;
"workflow run "*)
	case "${START_RELEASE_TEST_SCENARIO}" in
		dispatch-failure | rollback-failure) exit 1 ;;
	esac
	;;
"api --method DELETE "*)
	[[ ${START_RELEASE_TEST_SCENARIO} != rollback-failure ]] || exit 1
	;;
*)
	printf 'unexpected gh invocation: %s\n' "$*" >&2
	exit 1
	;;
esac
FAKE
chmod +x "${fake_bin}/gh"

run_start() {
	local scenario=$1
	local tag=${2:-${test_tag}}
	local branch=${3:-${test_branch}}
	local sha=${4:-${test_sha}}
	local head_sha=${5:-${test_sha}}
	local head_sha_after_create=${6:-${head_sha}}

	: >"${log}"
	DEFAULT_BRANCH="${test_branch}" \
		GH_TOKEN='test-token' \
		GITHUB_REF_NAME="${branch}" \
		GITHUB_REPOSITORY="${test_repository}" \
		GITHUB_SHA="${sha}" \
		PATH="${fake_bin}:${PATH}" \
		RELEASE_TAG="${tag}" \
		START_RELEASE_TEST_HEAD_SHA="${head_sha}" \
		START_RELEASE_TEST_HEAD_SHA_AFTER_CREATE="${head_sha_after_create}" \
		START_RELEASE_TEST_LOG="${log}" \
		START_RELEASE_TEST_SCENARIO="${scenario}" \
		"$(script_path)" >/dev/null 2>&1
}

expect_failure() {
	if run_start "$@"; then
		printf 'expected start_release failure for scenario %s\n' "$1" >&2
		exit 1
	fi
}

run_start success
cat >"${tmp}/want-success" <<WANT
api repos/${test_repository}/git/ref/heads/${test_branch} --jq .object.sha
api --method POST repos/${test_repository}/git/refs -f ref=refs/tags/${test_tag} -f sha=${test_sha}
api repos/${test_repository}/git/ref/heads/${test_branch} --jq .object.sha
workflow run release.yml --repo ${test_repository} --ref ${test_tag} -f tag=${test_tag}
WANT
diff -u "${tmp}/want-success" "${log}"

expect_failure invalid-tag '1.2.3'
[[ ! -s ${log} ]] || {
	printf 'invalid tags must fail before GitHub API access\n' >&2
	exit 1
}

: >"${log}"
if DEFAULT_BRANCH="${test_branch}" \
	GH_TOKEN='' \
	GITHUB_REF_NAME="${test_branch}" \
	GITHUB_REPOSITORY="${test_repository}" \
	GITHUB_SHA="${test_sha}" \
	PATH="${fake_bin}:${PATH}" \
	RELEASE_TAG="${test_tag}" \
	START_RELEASE_TEST_LOG="${log}" \
	"$(script_path)" >/dev/null 2>&1; then
	printf 'missing required environment must fail\n' >&2
	exit 1
fi
[[ ! -s ${log} ]] || {
	printf 'missing environment must fail before GitHub API access\n' >&2
	exit 1
}

expect_failure wrong-branch "${test_tag}" feature
[[ ! -s ${log} ]] || {
	printf 'non-default branches must fail before GitHub API access\n' >&2
	exit 1
}

expect_failure stale-head "${test_tag}" "${test_branch}" "${test_stale_sha}" "${test_sha}"
[[ $(wc -l <"${log}") -eq 1 ]] || {
	printf 'stale dispatch must stop after reading the default-branch head\n' >&2
	exit 1
}

expect_failure head-failure
[[ $(wc -l <"${log}") -eq 1 ]] || {
	printf 'head lookup failure must stop before tag creation\n' >&2
	exit 1
}

expect_failure empty-head
[[ $(wc -l <"${log}") -eq 1 ]] || {
	printf 'empty head response must stop before tag creation\n' >&2
	exit 1
}

expect_failure create-failure
[[ $(wc -l <"${log}") -eq 2 ]] || {
	printf 'tag creation failure must not dispatch or delete a pre-existing tag\n' >&2
	exit 1
}

expect_post_create_rollback() {
	local scenario=$1

	expect_failure "${scenario}" "${test_tag}" "${test_branch}" \
		"${test_sha}" "${test_sha}" "${test_stale_sha}"
	cat >"${tmp}/want-post-create-rollback" <<WANT
api repos/${test_repository}/git/ref/heads/${test_branch} --jq .object.sha
api --method POST repos/${test_repository}/git/refs -f ref=refs/tags/${test_tag} -f sha=${test_sha}
api repos/${test_repository}/git/ref/heads/${test_branch} --jq .object.sha
api --method DELETE repos/${test_repository}/git/refs/tags/${test_tag}
WANT
	diff -u "${tmp}/want-post-create-rollback" "${log}"
}

expect_post_create_rollback head-advanced-after-create
expect_post_create_rollback head-failure-after-create
expect_post_create_rollback empty-head-after-create

expect_failure dispatch-failure
grep -Fqx \
	"api --method DELETE repos/${test_repository}/git/refs/tags/${test_tag}" \
	"${log}"

expect_failure rollback-failure
grep -Fqx \
	"api --method DELETE repos/${test_repository}/git/refs/tags/${test_tag}" \
	"${log}"
