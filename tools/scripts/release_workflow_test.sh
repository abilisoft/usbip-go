#!/usr/bin/env bash

set -euo pipefail

repository_file() {
	local path=$1

	if [[ -n "${TEST_SRCDIR:-}" && -n "${TEST_WORKSPACE:-}" ]]; then
		printf '%s\n' "${TEST_SRCDIR}/${TEST_WORKSPACE}/${path}"
		return
	fi

	printf '%s\n' "$(dirname "${BASH_SOURCE[0]}")/../../${path}"
}

workflow=$(repository_file '.github/workflows/release.yml')
readonly workflow
goreleaser_config=$(repository_file '.goreleaser.yml')
readonly goreleaser_config
makefile=$(repository_file 'Makefile')
readonly makefile
readonly expected_generator='    uses: slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@v2.1.0'
readonly raw_sha_generator_pattern='^[[:space:]]*uses:[[:space:]]+slsa-framework/slsa-github-generator/\.github/workflows/generator_generic_slsa3\.yml@[[:xdigit:]]{40}([[:space:]]|$)'

provenance_count=0
publish_count=0
release_job_count=0
push_trigger_count=0
stable_tag_trigger_count=0
prerelease_exclusion_count=0
canonical_tag_validation_count=0
generator_count=0
base64_subjects_count=0
draft_release_count=0
upload_assets_count=0
continue_on_error_count=0
provenance_needs_release_count=0
publish_needs_provenance_count=0
publish_condition_count=0
publish_draft_lookup_count=0
publish_patch_command_count=0
publish_draft_false_count=0
release_needs_header_count=0
release_needs_entry_count=0
release_needs_arch_count=0
release_needs_conformance_count=0
release_needs_coverage_count=0
release_needs_security_count=0
release_needs_unit_tests_count=0
release_notes_command_count=0
release_notes_nonempty_check_count=0
release_notes_handoff_count=0
permissions_header_count=0
actions_read_count=0
contents_write_count=0
id_token_write_count=0
permission_entry_count=0
in_provenance=false
in_publish=false
in_release_job=false
in_release_needs=false
in_provenance_permissions=false

while IFS= read -r line || [[ -n ${line} ]]; do
	if [[ ${line} == *'workflow_dispatch:'* || ${line} == *'start-release'* ]]; then
		printf 'release workflow must be triggered only by a signed tag push\n' >&2
		exit 1
	fi

	if [[ ${line} =~ ${raw_sha_generator_pattern} ]]; then
		printf 'SLSA generator must use its verifier-compatible semantic version tag, not a raw SHA\n' >&2
		exit 1
	fi

	case "${line}" in
	'  push:') push_trigger_count=$((push_trigger_count + 1)) ;;
	"      - 'v*.*.*'") stable_tag_trigger_count=$((stable_tag_trigger_count + 1)) ;;
	"      - '!v*.*.*-*'") prerelease_exclusion_count=$((prerelease_exclusion_count + 1)) ;;
	*"grep -Eq '^v[0-9]+\\.[0-9]+\\.[0-9]+$'"*) canonical_tag_validation_count=$((canonical_tag_validation_count + 1)) ;;
	'          make changelog > build/release-notes.md') release_notes_command_count=$((release_notes_command_count + 1)) ;;
	'          test -s build/release-notes.md') release_notes_nonempty_check_count=$((release_notes_nonempty_check_count + 1)) ;;
	esac

	if [[ ${line} =~ ^\ \ [[:alnum:]_-]+:$ ]]; then
		in_provenance=false
		in_publish=false
		in_release_job=false
		in_release_needs=false
		in_provenance_permissions=false
		case "${line}" in
		'  release:')
			release_job_count=$((release_job_count + 1))
			in_release_job=true
			;;
		'  provenance:')
			provenance_count=$((provenance_count + 1))
			in_provenance=true
			;;
		'  publish:')
			publish_count=$((publish_count + 1))
			in_publish=true
			;;
		esac
		continue
	fi

	if [[ ${in_release_job} == true ]]; then
		if [[ ${in_release_needs} == true ]]; then
			case "${line}" in
			'      - arch')
				release_needs_entry_count=$((release_needs_entry_count + 1))
				release_needs_arch_count=$((release_needs_arch_count + 1))
				;;
			'      - conformance')
				release_needs_entry_count=$((release_needs_entry_count + 1))
				release_needs_conformance_count=$((release_needs_conformance_count + 1))
				;;
			'      - coverage')
				release_needs_entry_count=$((release_needs_entry_count + 1))
				release_needs_coverage_count=$((release_needs_coverage_count + 1))
				;;
			'      - security')
				release_needs_entry_count=$((release_needs_entry_count + 1))
				release_needs_security_count=$((release_needs_security_count + 1))
				;;
			'      - unit-tests')
				release_needs_entry_count=$((release_needs_entry_count + 1))
				release_needs_unit_tests_count=$((release_needs_unit_tests_count + 1))
				;;
			'      - '*) release_needs_entry_count=$((release_needs_entry_count + 1)) ;;
			*) in_release_needs=false ;;
			esac
		fi

		case "${line}" in
		'    needs:')
			release_needs_header_count=$((release_needs_header_count + 1))
			in_release_needs=true
			;;
		'          RELEASE_NOTES: build/release-notes.md') release_notes_handoff_count=$((release_notes_handoff_count + 1)) ;;
		esac
	fi

	if [[ ${in_provenance} == true ]]; then
		case "${line}" in
		'    needs: [release]') provenance_needs_release_count=$((provenance_needs_release_count + 1)) ;;
		'    permissions:')
			permissions_header_count=$((permissions_header_count + 1))
			in_provenance_permissions=true
			;;
		"${expected_generator}")
			generator_count=$((generator_count + 1))
			in_provenance_permissions=false
			;;
		"      base64-subjects: \${{ needs.release.outputs.hashes }}") base64_subjects_count=$((base64_subjects_count + 1)) ;;
		'      continue-on-error:'*) continue_on_error_count=$((continue_on_error_count + 1)) ;;
		"      draft-release: 'true'") draft_release_count=$((draft_release_count + 1)) ;;
		'      upload-assets: true') upload_assets_count=$((upload_assets_count + 1)) ;;
		'      '*)
			if [[ ${in_provenance_permissions} == true ]]; then
				permission_entry_count=$((permission_entry_count + 1))
				case "${line}" in
				'      actions: read') actions_read_count=$((actions_read_count + 1)) ;;
				'      contents: write') contents_write_count=$((contents_write_count + 1)) ;;
				'      id-token: write') id_token_write_count=$((id_token_write_count + 1)) ;;
				esac
			fi
			;;
		*) in_provenance_permissions=false ;;
		esac
	fi

	if [[ ${in_publish} == true ]]; then
		case "${line}" in
		'    needs: [provenance]') publish_needs_provenance_count=$((publish_needs_provenance_count + 1)) ;;
		'    if:'*) publish_condition_count=$((publish_condition_count + 1)) ;;
		*"select(.tag_name == \\\"\${TAG}\\\" and .draft == true)"*) publish_draft_lookup_count=$((publish_draft_lookup_count + 1)) ;;
		*"gh api --method PATCH \"repos/\${REPOSITORY}/releases/\${release_id}\""*) publish_patch_command_count=$((publish_patch_command_count + 1)) ;;
		'            -F draft=false >/dev/null') publish_draft_false_count=$((publish_draft_false_count + 1)) ;;
		esac
	fi
done <"${workflow}"

expect_one() {
	local count=$1
	local description=$2

	if ((count != 1)); then
		printf 'expected exactly one %s, found %d\n' "${description}" "${count}" >&2
		exit 1
	fi
}

expect_one "${provenance_count}" 'provenance job'
expect_one "${publish_count}" 'publish job'
expect_one "${release_job_count}" 'release job'
expect_one "${push_trigger_count}" 'release push trigger'
expect_one "${stable_tag_trigger_count}" 'stable tag trigger pattern'
expect_one "${prerelease_exclusion_count}" 'prerelease trigger exclusion'
expect_one "${canonical_tag_validation_count}" 'canonical stable tag validation'
expect_one "${provenance_needs_release_count}" 'provenance dependency on release'
expect_one "${generator_count}" 'SLSA v2.1.0 generator reference'
expect_one "${base64_subjects_count}" 'SLSA subjects from release artifact hashes'
expect_one "${draft_release_count}" "draft-release: 'true' setting"
expect_one "${upload_assets_count}" 'upload-assets: true setting'
expect_one "${permissions_header_count}" 'provenance permissions block'
expect_one "${actions_read_count}" 'provenance actions: read permission'
expect_one "${contents_write_count}" 'provenance contents: write permission'
expect_one "${id_token_write_count}" 'provenance id-token: write permission'
expect_one "${publish_needs_provenance_count}" 'publish dependency on provenance'
expect_one "${publish_draft_lookup_count}" 'publish lookup restricted to the tagged draft release'
expect_one "${publish_patch_command_count}" 'publish PATCH command'
expect_one "${publish_draft_false_count}" 'publish draft=false update'
expect_one "${release_needs_header_count}" 'release dependency block'
expect_one "${release_needs_arch_count}" 'release dependency on architecture checks'
expect_one "${release_needs_conformance_count}" 'release dependency on conformance'
expect_one "${release_needs_coverage_count}" 'release dependency on coverage'
expect_one "${release_needs_security_count}" 'release dependency on security'
expect_one "${release_needs_unit_tests_count}" 'release dependency on unit tests'
expect_one "${release_notes_command_count}" 'release-notes renderer stdout redirection'
expect_one "${release_notes_nonempty_check_count}" 'release-notes nonempty check'
expect_one "${release_notes_handoff_count}" 'release-notes handoff to GoReleaser'

if ((release_needs_entry_count != 5)); then
	printf 'release job must depend on exactly architecture, conformance, coverage, security, and unit-test gates\n' >&2
	exit 1
fi

if ((continue_on_error_count != 0)); then
	printf 'provenance generation must fail closed\n' >&2
	exit 1
fi

if ((publish_condition_count != 0)); then
	printf 'publish job must retain the default successful-needs condition\n' >&2
	exit 1
fi

if ((permission_entry_count != 3)); then
	printf 'provenance permissions must contain exactly actions: read, contents: write, and id-token: write\n' >&2
	exit 1
fi

release_count=0
draft_count=0
replace_existing_draft_count=0
use_existing_draft_count=0
replace_mode_count=0
in_release=false

while IFS= read -r line || [[ -n ${line} ]]; do
	if [[ ${line} == 'release:' ]]; then
		release_count=$((release_count + 1))
		in_release=true
		continue
	fi
	if [[ ${in_release} == true && ${line} != ' '* && -n ${line} ]]; then
		in_release=false
	fi
	if [[ ${in_release} == true ]]; then
		case "${line}" in
		'  draft: true') draft_count=$((draft_count + 1)) ;;
		'  replace_existing_draft: true') replace_existing_draft_count=$((replace_existing_draft_count + 1)) ;;
		'  use_existing_draft: true') use_existing_draft_count=$((use_existing_draft_count + 1)) ;;
		'  mode: replace') replace_mode_count=$((replace_mode_count + 1)) ;;
		esac
	fi
done <"${goreleaser_config}"

expect_one "${release_count}" 'GoReleaser release block'
expect_one "${draft_count}" 'GoReleaser draft: true setting'
expect_one "${replace_existing_draft_count}" 'GoReleaser replace_existing_draft: true setting'
expect_one "${use_existing_draft_count}" 'GoReleaser use_existing_draft: true setting'
expect_one "${replace_mode_count}" 'GoReleaser mode: replace setting'

bootstrap_recipe_line_count=0
bootstrap_recipe_count=0
changelog_recipe_line_count=0
changelog_recipe_count=0
current_target=''

while IFS= read -r line || [[ -n ${line} ]]; do
	if [[ ${line} == 'start-release:' || ${line} == *'tools/scripts/start_release.sh'* ]]; then
		printf 'Make must not expose the incompatible API-created tag launcher\n' >&2
		exit 1
	fi

	case "${line}" in
	'bootstrap:')
		current_target='bootstrap'
		continue
		;;
	'changelog: bootstrap')
		current_target='changelog'
		continue
		;;
	$'\t'*) ;;
	'' | '# '*) ;;
	*)
		current_target=''
		;;
	esac

	if [[ ${current_target} == 'bootstrap' && ${line} == $'\t'* ]]; then
		bootstrap_recipe_line_count=$((bootstrap_recipe_line_count + 1))
		if [[ ${line} == $'\t@'* && ${line} == *' tools/scripts/bootstrap.sh 1>&2' ]]; then
			bootstrap_recipe_count=$((bootstrap_recipe_count + 1))
		fi
	fi

	if [[ ${current_target} == 'changelog' && ${line} == $'\t'* ]]; then
		changelog_recipe_line_count=$((changelog_recipe_line_count + 1))
		if [[ ${line} == $'\t@$(BAZEL) run //:changelog' ]]; then
			changelog_recipe_count=$((changelog_recipe_count + 1))
		fi
	fi
done <"${makefile}"

expect_one "${bootstrap_recipe_line_count}" 'bootstrap recipe line'
expect_one "${bootstrap_recipe_count}" 'quiet bootstrap recipe with stdout redirected to stderr'
expect_one "${changelog_recipe_line_count}" 'changelog recipe line'
expect_one "${changelog_recipe_count}" 'quiet changelog renderer recipe'
