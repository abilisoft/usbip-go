#!/usr/bin/env bash

set -euo pipefail

repository_file() {
	local path=$1

	if [[ -n ${TEST_SRCDIR:-} && -n ${TEST_WORKSPACE:-} ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/${path}"
		return
	fi

	printf '%s\n' "$(dirname "${BASH_SOURCE[0]}")/../../${path}"
}

workflow=$(repository_file '.github/workflows/recover-v1.0.2.yml')
pure_validator=$(repository_file 'tools/scripts/validate_release_recovery.sh')
live_validator=$(repository_file 'tools/scripts/validate_live_release_recovery.sh')
pure_asset_validator=$(repository_file 'tools/scripts/validate_release_recovery_assets.sh')
live_asset_validator=$(repository_file 'tools/scripts/validate_live_release_recovery_assets.sh')
readonly workflow pure_validator live_validator pure_asset_validator live_asset_validator
readonly fixed_tag='v1.0.2'
readonly fixed_tag_object='f0c7083fdee40e1e31ebc170992fa5f43efe8d60'
readonly fixed_target='72aa5a6b585d1f5b6230c8362254ea2a6296ec75'

fail() {
	printf '%s\n' "$1" >&2
	exit 1
}

count_matches() {
	local file=$1
	local pattern=$2
	local count

	count=$(grep -F -c -- "${pattern}" "${file}" || true)
	printf '%s\n' "${count}"
}

expect_count() {
	local file=$1
	local pattern=$2
	local expected=$3
	local description=$4
	local actual

	actual=$(count_matches "${file}" "${pattern}")
	if ((actual != expected)); then
		fail "expected ${expected} ${description}, found ${actual}"
	fi
}

line_number() {
	local file=$1
	local pattern=$2
	local line

	line=$(grep -F -n -m1 -- "${pattern}" "${file}" || true)
	[[ -n ${line} ]] || fail "missing ordered workflow step: ${pattern}"
	printf '%s\n' "${line%%:*}"
}

expect_count "${workflow}" '  workflow_dispatch:' 1 'workflow-dispatch trigger'
expect_count "${workflow}" '    inputs:' 1 'workflow-dispatch input block'
expect_count "${workflow}" '      release-tag:' 2 'release confirmation input and validated job output'
expect_count "${workflow}" '        type: choice' 1 'fixed choice input type'
expect_count "${workflow}" '          - v1.0.2' 1 'only recovery choice'
expect_count "${workflow}" "          RECOVERY_TAG: \${{ inputs.release-tag }}" 1 'untrusted confirmation handoff to validation'
expect_count "${workflow}" '          ref: 72aa5a6b585d1f5b6230c8362254ea2a6296ec75' 1 'fixed preflight source checkout'
expect_count "${workflow}" "      source-ref: \${{ needs.validate-tag.outputs.source-commit }}" 5 'validated source handoffs to prereq gates'
expect_count "${workflow}" "          ref: \${{ needs.validate-tag.outputs.source-commit }}" 2 'validated source staging and publication checkouts'
expect_count "${workflow}" "          ref: \${{ github.sha }}" 3 'dispatch-controller checkouts'
expect_count "${workflow}" '          persist-credentials: false' 6 'credential-free recovery checkouts'
expect_count "${workflow}" '      - uses: actions/checkout@' 6 'recovery checkout steps'
expect_count "${workflow}" '          RECOVERY_REQUIRED_RELEASE_STATE: preflight' 2 'preflight release-state validations'
expect_count "${workflow}" '          RECOVERY_REQUIRED_RELEASE_STATE: draft' 2 'bound draft validations'
expect_count "${workflow}" 'tools/scripts/validate_live_release_recovery.sh' 4 'live recovery validator invocations'
expect_count "${workflow}" 'tools/scripts/validate_live_release_recovery_assets.sh' 1 'bound asset validator invocation'
expect_count "${workflow}" '        run: make ci-local' 1 'exact-source local CI gate'
expect_count "${workflow}" "          make changelog > \"\${RELEASE_NOTES_PATH}\"" 1 'exact-source release-notes rendering'
expect_count "${workflow}" "            printf \"Immutable source commit: \\\`%s\\\`.\\n\\n\"" 1 'immutable source disclosure in release notes'
expect_count "${workflow}" "            printf \"Recovery controller: \\\`.github/workflows/recover-v1.0.2.yml@%s\\\` on protected \\\`main\\\`.\\n\\n\"" 1 'recovery controller disclosure in release notes'
expect_count "${workflow}" '              '\''Its SLSA statement is protected-main workflow-dispatch provenance, not a new tag-push event.'\''' 1 'honest recovery provenance disclosure'
expect_count "${workflow}" '        run: make release' 1 'exact-source GoReleaser staging'
expect_count "${workflow}" '    uses: slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@v2.1.0' 1 'pinned SLSA builder identity'
expect_count "${workflow}" "      base64-subjects: \${{ needs.release.outputs.hashes }}" 1 'artifact hash handoff'
expect_count "${workflow}" "      draft-release: 'true'" 1 'draft-preserving provenance setting'
expect_count "${workflow}" '      upload-assets: true' 1 'provenance release upload'
expect_count "${workflow}" "      upload-tag-name: \${{ needs.validate-tag.outputs.release-tag }}" 1 'validated recovery tag upload destination'
expect_count "${workflow}" '    needs: [provenance, release, validate-tag]' 1 'publication dependency on provenance, release, and validation'
expect_count "${workflow}" "      release-id: \${{ steps.validate-draft.outputs.release-id }}" 1 'bound draft ID output'
expect_count "${workflow}" "      release-assets: \${{ steps.hash-artifacts.outputs.release-assets }}" 1 'staged release digest output'
expect_count "${workflow}" "          RECOVERY_EXPECTED_RELEASE_ID: \${{ needs.release.outputs.release-id }}" 1 'bound draft ID revalidation'
expect_count "${workflow}" "          RECOVERY_RELEASE_ID: \${{ needs.release.outputs.release-id }}" 1 'bound draft ID publication handoff'
expect_count "${workflow}" "          RECOVERY_EXPECTED_ASSETS_BASE64: \${{ needs.release.outputs.release-assets }}" 1 'staged release digest validation handoff'
expect_count "${workflow}" "          RECOVERY_EXPECTED_SUBJECTS_BASE64: \${{ needs.release.outputs.hashes }}" 1 'attested subject digest validation handoff'
expect_count "${workflow}" 'select(.tag_name == "v1.0.2" and .draft == true)' 0 'independent draft lookups'
expect_count "${workflow}" '-F draft=false >/dev/null' 1 'single draft publication mutation'

input_key_count=$(awk '
	$0 == "    inputs:" { in_inputs = 1; next }
	in_inputs && /^[^ ]/ { in_inputs = 0 }
	in_inputs && /^      [[:alnum:]_-]+:$/ { count++ }
	END { print count + 0 }
' "${workflow}")
if ((input_key_count != 1)); then
	fail "recovery workflow must expose exactly one non-selecting confirmation input, found ${input_key_count}"
fi

subject_name_count=$(awk '
	/expected_artifacts=\(/ { in_subjects = 1; next }
	in_subjects && /^[[:space:]]*\)/ { in_subjects = 0; next }
	in_subjects && /[^[:space:]]/ { count++ }
	END { print count + 0 }
' "${workflow}")
if ((subject_name_count != 9)); then
	fail "recovery workflow must hash exactly nine SLSA subjects, found ${subject_name_count}"
fi

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
actual_assets=$(awk '
	/readonly expected_assets=\(/ { in_assets = 1; next }
	in_assets && /^\)/ { in_assets = 0; next }
	in_assets && /[^[:space:]]/ { print $1 }
' "${pure_asset_validator}")
expected_asset_lines=$(printf '%s\n' "${expected_assets[@]}")
if [[ ${actual_assets} != "${expected_asset_lines}" ]]; then
	fail 'recovery asset validator exact 15-asset roster changed'
fi

if grep -Eq -- '(^|[[:space:]])git[[:space:]]+(tag|push)|/git/refs|--method[[:space:]]+(DELETE|POST)' "${workflow}"; then
	fail 'recovery workflow must not create, move, or delete Git refs'
fi

if grep -Eq '^[[:space:]]+(if|continue-on-error):' "${workflow}"; then
	fail 'recovery pipeline must retain default fail-closed job and step behavior'
fi

revalidate_stage_line=$(line_number "${workflow}" '      - name: Revalidate immutable source before staging draft')
stage_line=$(line_number "${workflow}" '      - name: Stage recovered draft release')
bind_draft_line=$(line_number "${workflow}" '      - name: Bind the recovered draft release ID')
hash_line=$(line_number "${workflow}" '      - name: Hash recovered release artifacts for SLSA provenance')
if ! ((revalidate_stage_line < stage_line && stage_line < bind_draft_line && bind_draft_line < hash_line)); then
	fail 'recovery must revalidate, stage, bind one draft ID, and only then hash exact artifacts'
fi

publish_step_line=$(line_number "${workflow}" '      - name: Revalidate and publish the exact recovered draft')
revalidate_publish_line=$(line_number "${workflow}" '          .local/release-control/tools/scripts/validate_live_release_recovery.sh')
asset_validation_line=$(line_number "${workflow}" '          .local/release-control/tools/scripts/validate_live_release_recovery_assets.sh')
publish_line=$(line_number "${workflow}" "          gh api --method PATCH \"repos/\${RECOVERY_REPOSITORY}/releases/\${RECOVERY_RELEASE_ID}\"")
if ! ((publish_step_line < revalidate_publish_line && revalidate_publish_line < asset_validation_line && asset_validation_line < publish_line)); then
	fail 'one exact draft ID must be revalidated with its assets immediately before publication'
fi

for reusable in \
	.github/workflows/_security.yml \
	.github/workflows/_unit-tests.yml \
	.github/workflows/_conformance.yml \
	.github/workflows/_arch-checks.yml \
	.github/workflows/_coverage.yml; do
	file=$(repository_file "${reusable}")
	checkout_count=$(count_matches "${file}" '      - uses: actions/checkout@')
	expect_count "${file}" '      source-ref:' 1 "source-ref declaration in ${reusable}"
	expect_count "${file}" "          ref: \${{ inputs.source-ref || github.sha }}" \
		"${checkout_count}" "explicit source-ref checkout in ${reusable}"
	expect_count "${file}" '          persist-credentials: false' \
		"${checkout_count}" "credential-free checkout in ${reusable}"
done

expect_count "${pure_validator}" "readonly fixed_controller_ref='refs/heads/main'" 1 'fixed protected controller ref'
expect_count "${pure_validator}" "readonly fixed_release_tag='${fixed_tag}'" 1 'fixed recovery tag'
expect_count "${pure_validator}" "readonly fixed_tag_object_sha='${fixed_tag_object}'" 1 'fixed annotated tag object'
expect_count "${pure_validator}" "readonly fixed_target_commit='${fixed_target}'" 1 'fixed source commit'
expect_count "${pure_validator}" "printf 'release-tag=%s\\n'" 1 'validated release-tag output'
expect_count "${pure_validator}" "printf 'tag-object-sha=%s\\n'" 1 'validated tag-object output'
expect_count "${pure_validator}" "printf 'source-commit=%s\\n'" 1 'validated source-commit output'
expect_count "${pure_validator}" "printf 'release-id=%s\\n'" 1 'bound release-ID output'

expect_count "${live_validator}" '--paginate' 1 'complete release-state pagination'
expect_count "${live_validator}" '--slurp' 0 'GitHub CLI-compatible release pagination'
expect_count "${live_validator}" "git -C \"\${RECOVERY_CONTROLLER_PATH}\" rev-parse HEAD" 1 'controller checkout verification'
expect_count "${live_validator}" "git -C \"\${RECOVERY_SOURCE_PATH}\" rev-parse HEAD" 1 'source checkout verification'
expect_count "${live_validator}" "git -C \"\${RECOVERY_SOURCE_PATH}\" rev-parse \"refs/tags/\${fixed_release_tag}\"" 1 'local tag-object verification'
expect_count "${live_validator}" "git -C \"\${RECOVERY_SOURCE_PATH}\" rev-parse \"refs/tags/\${fixed_release_tag}^{commit}\"" 1 'local tag-target verification'

expect_count "${live_asset_validator}" "releases/\${RECOVERY_RELEASE_ID}" 2 'bound release-ID API lookups'
expect_count "${live_asset_validator}" '--paginate' 1 'complete asset pagination'
expect_count "${live_asset_validator}" '--slurp' 0 'GitHub CLI-compatible asset pagination'
expect_count "${live_asset_validator}" '(.digest // "")' 1 'remote asset SHA-256 digest retrieval'
expect_count "${pure_asset_validator}" 'published asset digest does not match the attested subject' 1 'remote subject digest binding'
expect_count "${pure_asset_validator}" 'published asset digest does not match the staged release asset' 1 'remote staged-asset digest binding'
