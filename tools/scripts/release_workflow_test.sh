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
changelog_runner=$(repository_file 'tools/scripts/changelog_runner.sh')
readonly changelog_runner
live_release_tag_validator=$(repository_file 'tools/scripts/validate_live_release_tag.sh')
readonly live_release_tag_validator
release_notes_validator=$(repository_file 'tools/scripts/validate_release_notes.sh')
readonly release_notes_validator
release_tag_validator=$(repository_file 'tools/scripts/validate_release_tag.sh')
readonly release_tag_validator
readonly expected_generator='    uses: slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@v2.1.0'
readonly raw_sha_generator_pattern='^[[:space:]]*uses:[[:space:]]+slsa-framework/slsa-github-generator/\.github/workflows/generator_generic_slsa3\.yml@[[:xdigit:]]{40}([[:space:]]|$)'

provenance_count=0
publish_count=0
release_job_count=0
push_trigger_count=0
stable_tag_trigger_count=0
prerelease_exclusion_count=0
release_event_created_count=0
release_event_deleted_count=0
release_event_forced_count=0
release_event_after_count=0
release_event_target_commit_count=0
release_repository_count=0
release_tag_input_count=0
release_tag_validator_count=0
release_revalidation_count=0
publish_revalidation_count=0
validator_checkout_count=0
validator_checkout_ref_count=0
validator_persist_credentials_count=0
validator_gh_token_count=0
validator_tag_ref_endpoint_count=0
validator_tag_object_endpoint_count=0
validator_tag_signature_query_count=0
validator_checked_out_commit_count=0
validator_tag_object_name_export_count=0
validator_tag_object_sha_export_count=0
validator_tag_object_type_export_count=0
validator_tag_signature_export_count=0
validator_tag_target_commit_export_count=0
validator_tag_target_type_export_count=0
input_validator_invocation_count=0
pipeline_condition_count=0
pipeline_continue_on_error_count=0
arch_needs_validate_count=0
conformance_needs_validate_count=0
coverage_needs_validate_count=0
security_needs_validate_count=0
unit_tests_needs_validate_count=0
generator_count=0
base64_subjects_count=0
draft_release_count=0
upload_assets_count=0
provenance_needs_release_count=0
publish_needs_provenance_count=0
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
release_notes_validator_count=0
release_notes_path_input_count=0
release_notes_tag_input_count=0
release_notes_handoff_count=0
release_checkout_count=0
release_checkout_ref_count=0
release_persist_credentials_count=0
publish_checkout_count=0
publish_checkout_ref_count=0
publish_persist_credentials_count=0
source_ref_handoff_count=0
changelog_current_count=0
changelog_latest_count=0
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
in_validate=false
current_job=''
release_revalidation_seen=false
release_notes_validation_seen=false
publish_revalidation_seen=false

while IFS= read -r line || [[ -n ${line} ]]; do
	if [[ ${line} == *'workflow_dispatch:'* || ${line} == *'start-release'* ]]; then
		printf 'release workflow must be triggered only by a signed tag push\n' >&2
		exit 1
	fi

	if [[ ${line} =~ ${raw_sha_generator_pattern} ]]; then
		printf 'SLSA generator must use its verifier-compatible semantic version tag, not a raw SHA\n' >&2
		exit 1
	fi

	if [[ ${line} == "          ref: \${{ github.event.after }}" ]]; then
		printf 'release jobs must never check out an annotated tag-object SHA\n' >&2
		exit 1
	fi

	case "${line}" in
	"          RELEASE_EVENT_AFTER: \${{ github.event.after }}") release_event_after_count=$((release_event_after_count + 1)) ;;
	"          RELEASE_EVENT_TARGET_COMMIT: \${{ github.sha }}") release_event_target_commit_count=$((release_event_target_commit_count + 1)) ;;
	"      source-ref: \${{ github.sha }}") source_ref_handoff_count=$((source_ref_handoff_count + 1)) ;;
	esac

	case "${line}" in
	'  push:') push_trigger_count=$((push_trigger_count + 1)) ;;
	"      - 'v*.*.*'") stable_tag_trigger_count=$((stable_tag_trigger_count + 1)) ;;
	"      - '!v*.*.*-*'") prerelease_exclusion_count=$((prerelease_exclusion_count + 1)) ;;
	"          make changelog > \"\${RELEASE_NOTES_PATH}\"") release_notes_command_count=$((release_notes_command_count + 1)) ;;
	esac

	if [[ ${line} =~ ^\ \ [[:alnum:]_-]+:$ ]]; then
		current_job=${line#'  '}
		current_job=${current_job%:}
		in_validate=false
		in_provenance=false
		in_publish=false
		in_release_job=false
		in_release_needs=false
		in_provenance_permissions=false
		case "${line}" in
		'  validate-tag:') in_validate=true ;;
		'  release:')
			release_job_count=$((release_job_count + 1))
			in_release_job=true
			release_revalidation_seen=false
			release_notes_validation_seen=false
			;;
		'  provenance:')
			provenance_count=$((provenance_count + 1))
			in_provenance=true
			;;
		'  publish:')
			publish_count=$((publish_count + 1))
			in_publish=true
			publish_revalidation_seen=false
			;;
		esac
		continue
	fi

	if [[ ${in_validate} == true ]]; then
		case "${line}" in
		'      - uses: actions/checkout@'*) validator_checkout_count=$((validator_checkout_count + 1)) ;;
		"          GH_TOKEN: \${{ secrets.GITHUB_TOKEN }}") validator_gh_token_count=$((validator_gh_token_count + 1)) ;;
		"          RELEASE_EVENT_CREATED: \${{ github.event.created }}") release_event_created_count=$((release_event_created_count + 1)) ;;
		"          RELEASE_EVENT_DELETED: \${{ github.event.deleted }}") release_event_deleted_count=$((release_event_deleted_count + 1)) ;;
		"          RELEASE_EVENT_FORCED: \${{ github.event.forced }}") release_event_forced_count=$((release_event_forced_count + 1)) ;;
		"          RELEASE_REPOSITORY: \${{ github.repository }}") release_repository_count=$((release_repository_count + 1)) ;;
		"          RELEASE_TAG: \${{ github.ref_name }}") release_tag_input_count=$((release_tag_input_count + 1)) ;;
		'        run: tools/scripts/validate_live_release_tag.sh') release_tag_validator_count=$((release_tag_validator_count + 1)) ;;
		"          ref: \${{ github.event.repository.default_branch }}") validator_checkout_ref_count=$((validator_checkout_ref_count + 1)) ;;
		'          persist-credentials: false') validator_persist_credentials_count=$((validator_persist_credentials_count + 1)) ;;
		esac
	fi

	if [[ ${line} == '    needs: validate-tag' ]]; then
		case "${current_job}" in
		arch) arch_needs_validate_count=$((arch_needs_validate_count + 1)) ;;
		conformance) conformance_needs_validate_count=$((conformance_needs_validate_count + 1)) ;;
		coverage) coverage_needs_validate_count=$((coverage_needs_validate_count + 1)) ;;
		security) security_needs_validate_count=$((security_needs_validate_count + 1)) ;;
		unit-tests) unit_tests_needs_validate_count=$((unit_tests_needs_validate_count + 1)) ;;
		esac
	fi

	if [[ ${line} =~ ^[[:space:]]+if: ]]; then
		case "${current_job}" in
		validate-tag | security | unit-tests | conformance | arch | coverage | release | provenance | publish)
			pipeline_condition_count=$((pipeline_condition_count + 1))
			;;
		esac
	fi

	if [[ ${line} =~ ^[[:space:]]+continue-on-error: ]]; then
		case "${current_job}" in
		validate-tag | security | unit-tests | conformance | arch | coverage | release | provenance | publish)
			pipeline_continue_on_error_count=$((pipeline_continue_on_error_count + 1))
			;;
		esac
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
		'      - uses: actions/checkout@'*) release_checkout_count=$((release_checkout_count + 1)) ;;
		"          ref: \${{ github.sha }}") release_checkout_ref_count=$((release_checkout_ref_count + 1)) ;;
		'          persist-credentials: false') release_persist_credentials_count=$((release_persist_credentials_count + 1)) ;;
		"          RELEASE_NOTES_PATH: build/release-notes.md") release_notes_path_input_count=$((release_notes_path_input_count + 1)) ;;
		"          RELEASE_TAG: \${{ github.ref_name }}") release_notes_tag_input_count=$((release_notes_tag_input_count + 1)) ;;
		'          tools/scripts/validate_release_notes.sh')
			release_notes_validator_count=$((release_notes_validator_count + 1))
			release_notes_validation_seen=true
			;;
		'        run: tools/scripts/validate_live_release_tag.sh')
			if [[ ${release_notes_validation_seen} != true ]]; then
				printf 'release notes must be validated before live-tag revalidation and draft staging\n' >&2
				exit 1
			fi
			release_revalidation_count=$((release_revalidation_count + 1))
			release_revalidation_seen=true
			;;
		'      - name: Stage draft release')
			if [[ ${release_revalidation_seen} != true ]]; then
				printf 'release tag must be revalidated immediately before draft staging\n' >&2
				exit 1
			fi
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
		'      - uses: actions/checkout@'*) publish_checkout_count=$((publish_checkout_count + 1)) ;;
		"          ref: \${{ github.sha }}") publish_checkout_ref_count=$((publish_checkout_ref_count + 1)) ;;
		'          persist-credentials: false') publish_persist_credentials_count=$((publish_persist_credentials_count + 1)) ;;
		'        run: tools/scripts/validate_live_release_tag.sh')
			publish_revalidation_count=$((publish_revalidation_count + 1))
			publish_revalidation_seen=true
			;;
		'      - name: Publish draft release')
			if [[ ${publish_revalidation_seen} != true ]]; then
				printf 'release tag must be revalidated immediately before publication\n' >&2
				exit 1
			fi
			;;
		*"select(.tag_name == \\\"\${TAG}\\\" and .draft == true)"*) publish_draft_lookup_count=$((publish_draft_lookup_count + 1)) ;;
		*"gh api --method PATCH \"repos/\${REPOSITORY}/releases/\${release_id}\""*) publish_patch_command_count=$((publish_patch_command_count + 1)) ;;
		'            -F draft=false >/dev/null') publish_draft_false_count=$((publish_draft_false_count + 1)) ;;
		esac
	fi
done <"${workflow}"

while IFS= read -r line || [[ -n ${line} ]]; do
	case "${line}" in
	'readonly tag_ref_endpoint='*) validator_tag_ref_endpoint_count=$((validator_tag_ref_endpoint_count + 1)) ;;
	$'\treadonly tag_object_endpoint='*) validator_tag_object_endpoint_count=$((validator_tag_object_endpoint_count + 1)) ;;
	*'.verification.verified | tostring'*) validator_tag_signature_query_count=$((validator_tag_signature_query_count + 1)) ;;
	$'\tRELEASE_CHECKED_OUT_COMMIT=$(git rev-parse HEAD)') validator_checked_out_commit_count=$((validator_checked_out_commit_count + 1)) ;;
	'export RELEASE_TAG_OBJECT_NAME='*) validator_tag_object_name_export_count=$((validator_tag_object_name_export_count + 1)) ;;
	'export RELEASE_TAG_OBJECT_SHA='*) validator_tag_object_sha_export_count=$((validator_tag_object_sha_export_count + 1)) ;;
	'export RELEASE_TAG_OBJECT_TYPE='*) validator_tag_object_type_export_count=$((validator_tag_object_type_export_count + 1)) ;;
	'export RELEASE_TAG_SIGNATURE_VERIFIED='*) validator_tag_signature_export_count=$((validator_tag_signature_export_count + 1)) ;;
	'export RELEASE_TAG_TARGET_COMMIT='*) validator_tag_target_commit_export_count=$((validator_tag_target_commit_export_count + 1)) ;;
	'export RELEASE_TAG_TARGET_TYPE='*) validator_tag_target_type_export_count=$((validator_tag_target_type_export_count + 1)) ;;
	"\"\$(validator_path)\"") input_validator_invocation_count=$((input_validator_invocation_count + 1)) ;;
	esac
done <"${live_release_tag_validator}"

while IFS= read -r line || [[ -n ${line} ]]; do
	case "${line}" in
	$'\t--current') changelog_current_count=$((changelog_current_count + 1)) ;;
	$'\t--latest') changelog_latest_count=$((changelog_latest_count + 1)) ;;
	esac
done <"${changelog_runner}"

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
if ((release_event_after_count != 3)); then
	printf 'expected tag-object event input for initial, staging, and publication validation; found %d\n' "${release_event_after_count}" >&2
	exit 1
fi
if ((release_event_target_commit_count != 3)); then
	printf 'expected peeled event-commit input for initial, staging, and publication validation; found %d\n' "${release_event_target_commit_count}" >&2
	exit 1
fi
expect_one "${release_event_created_count}" 'fresh-tag created event input'
expect_one "${release_event_deleted_count}" 'fresh-tag deleted event input'
expect_one "${release_event_forced_count}" 'fresh-tag forced event input'
expect_one "${release_repository_count}" 'release repository input'
expect_one "${release_tag_input_count}" 'release tag-name input'
expect_one "${release_tag_validator_count}" 'repository-owned release tag validator invocation'
expect_one "${release_revalidation_count}" 'pre-staging live tag revalidation'
expect_one "${publish_revalidation_count}" 'pre-publication live tag revalidation'
expect_one "${validator_checkout_count}" 'validator checkout action'
expect_one "${validator_checkout_ref_count}" 'default-branch validator checkout'
expect_one "${validator_persist_credentials_count}" 'credential-free validator checkout'
expect_one "${validator_gh_token_count}" 'read-only GitHub API token input'
expect_one "${validator_tag_ref_endpoint_count}" 'live release tag-ref lookup'
expect_one "${validator_tag_object_endpoint_count}" 'annotated release tag-object lookup'
expect_one "${validator_tag_signature_query_count}" 'GitHub tag-signature verification lookup'
expect_one "${validator_checked_out_commit_count}" 'checked-out release commit lookup'
expect_one "${validator_tag_object_name_export_count}" 'annotated tag-name validator input'
expect_one "${validator_tag_object_sha_export_count}" 'annotated tag-object SHA validator input'
expect_one "${validator_tag_object_type_export_count}" 'annotated tag-type validator input'
expect_one "${validator_tag_signature_export_count}" 'tag-signature validator input'
expect_one "${validator_tag_target_commit_export_count}" 'tag-target validator input'
expect_one "${validator_tag_target_type_export_count}" 'tag-target-type validator input'
expect_one "${input_validator_invocation_count}" 'pure release tag validator handoff'
expect_one "${arch_needs_validate_count}" 'architecture dependency on tag validation'
expect_one "${conformance_needs_validate_count}" 'conformance dependency on tag validation'
expect_one "${coverage_needs_validate_count}" 'coverage dependency on tag validation'
expect_one "${security_needs_validate_count}" 'security dependency on tag validation'
expect_one "${unit_tests_needs_validate_count}" 'unit-test dependency on tag validation'
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
expect_one "${release_notes_validator_count}" 'fail-closed release-notes validator invocation'
expect_one "${release_notes_path_input_count}" 'release-notes validator path input'
expect_one "${release_notes_handoff_count}" 'release-notes handoff to GoReleaser'
expect_one "${release_checkout_count}" 'release checkout action'
expect_one "${release_checkout_ref_count}" 'immutable release event-target checkout'
expect_one "${release_persist_credentials_count}" 'credential-free release checkout'
expect_one "${publish_checkout_count}" 'publish checkout action'
expect_one "${publish_checkout_ref_count}" 'immutable publish event-target checkout'
expect_one "${publish_persist_credentials_count}" 'credential-free publish checkout'
expect_one "${changelog_current_count}" 'current-tag changelog selection'

if ((source_ref_handoff_count != 5)); then
	printf 'release prereq workflows must each receive the peeled event commit; found %d handoffs\n' "${source_ref_handoff_count}" >&2
	exit 1
fi

if ((release_notes_tag_input_count != 2)); then
	printf 'release job must pass the tag to both release-note and live-tag validation steps\n' >&2
	exit 1
fi

if ((changelog_latest_count != 0)); then
	printf 'release changelog must not select an unrelated latest tag\n' >&2
	exit 1
fi

if ((release_needs_entry_count != 5)); then
	printf 'release job must depend on exactly architecture, conformance, coverage, security, and unit-test gates\n' >&2
	exit 1
fi

if ((pipeline_condition_count != 0)); then
	printf 'release pipeline jobs and steps must retain default fail-closed conditions\n' >&2
	exit 1
fi

if [[ ! -x ${live_release_tag_validator} || ! -x ${release_notes_validator} || ! -x ${release_tag_validator} ]]; then
	printf 'release validators must be executable repository-owned scripts\n' >&2
	exit 1
fi

if ((pipeline_continue_on_error_count != 0)); then
	printf 'release pipeline jobs and steps must not continue on validation or release errors\n' >&2
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
