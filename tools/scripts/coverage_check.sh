#!/usr/bin/env bash

set -euo pipefail

usage() {
	printf 'usage: coverage_check.sh COVERAGE_LCOV CONFIG_YAML\n' >&2
	exit 2
}

coverage_file=${1:-}
config_file=${2:-}
[[ -n "${coverage_file}" && -n "${config_file}" ]] || usage

if [[ -n "${BUILD_WORKSPACE_DIRECTORY:-}" ]]; then
	case "${coverage_file}" in
	/*) ;;
	*) coverage_file="${BUILD_WORKSPACE_DIRECTORY}/${coverage_file}" ;;
	esac
	case "${config_file}" in
	/*) ;;
	*) config_file="${BUILD_WORKSPACE_DIRECTORY}/${config_file}" ;;
	esac
fi

[[ -f "${coverage_file}" ]] || {
	printf 'coverage-check: coverage file not found: %s\n' "${coverage_file}" >&2
	exit 1
}
[[ -f "${config_file}" ]] || {
	printf 'coverage-check: config file not found: %s\n' "${config_file}" >&2
	exit 1
}

awk '
function trim(s) {
	gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
	return s
}
BEGIN {
	section = ""
	in_exclude = 0
}
/^threshold:/ {
	section = "threshold"
	in_exclude = 0
	next
}
/^exclude:/ {
	section = "exclude"
	in_exclude = 0
	next
}
section == "threshold" && /^[[:space:]]+[A-Za-z]+:/ {
	key = trim($1)
	sub(/:$/, "", key)
	value = trim($2)
	threshold[key] = value + 0
	next
}
section == "exclude" && /^[[:space:]]+paths:/ {
	in_exclude = 1
	next
}
section == "exclude" && in_exclude && /^[[:space:]]+-[[:space:]]+/ {
	line = $0
	sub(/^[[:space:]]+-[[:space:]]+/, "", line)
	sub(/[[:space:]]+#.*$/, "", line)
	line = trim(line)
	if (line != "") {
		excludes[++exclude_count] = line
	}
	next
}
END {
	if (!("package" in threshold) || !("total" in threshold)) {
		print "coverage-check: missing package or total threshold in config" > "/dev/stderr"
		exit 1
	}
	printf("package_threshold=%s\n", threshold["package"])
	printf("total_threshold=%s\n", threshold["total"])
	for (i = 1; i <= exclude_count; i++) {
		printf("exclude=%s\n", excludes[i])
	}
}
' "${config_file}" >"${coverage_file}.thresholds"

package_threshold=$(awk -F= '$1 == "package_threshold" { print $2 }' "${coverage_file}.thresholds")
total_threshold=$(awk -F= '$1 == "total_threshold" { print $2 }' "${coverage_file}.thresholds")
mapfile -t exclude_patterns < <(awk -F= '$1 == "exclude" { print $2 }' "${coverage_file}.thresholds")
rm -f "${coverage_file}.thresholds"

awk -v package_threshold="${package_threshold}" -v total_threshold="${total_threshold}" '
function dirname(path) {
	sub(/\/[^/]*$/, "", path)
	return path
}
function pct(hit, found) {
	if (found == 0) {
		return 100
	}
	return (hit * 100.0) / found
}
function excluded(path,    i) {
	for (i = 1; i <= exclude_count; i++) {
		if (path ~ excludes[i]) {
			return 1
		}
	}
	return 0
}
BEGIN {
	for (i = 1; i < ARGC; i++) {
		if (ARGV[i] == "--") {
			config_done = 1
			ARGV[i] = ""
			continue
		}
		if (!config_done) {
			excludes[++exclude_count] = ARGV[i]
			ARGV[i] = ""
		}
	}
}
/^SF:/ {
	current = substr($0, 4)
	skip = excluded(current)
	pkg = dirname(current)
	next
}
/^LF:/ && !skip {
	found = substr($0, 4) + 0
	total_found += found
	pkg_found[pkg] += found
	next
}
/^LH:/ && !skip {
	hit = substr($0, 4) + 0
	total_hit += hit
	pkg_hit[pkg] += hit
	next
}
END {
	fail = 0
	total_pct = pct(total_hit, total_found)
	printf("coverage: total %.2f%% (%d/%d), threshold %.2f%%\n", total_pct, total_hit, total_found, total_threshold)
	if (total_pct + 0.000001 < total_threshold) {
		printf("coverage-check: total coverage %.2f%% is below threshold %.2f%%\n", total_pct, total_threshold) > "/dev/stderr"
		fail = 1
	}
	for (pkg in pkg_found) {
		pkg_pct = pct(pkg_hit[pkg], pkg_found[pkg])
		printf("coverage: %s %.2f%% (%d/%d), threshold %.2f%%\n", pkg, pkg_pct, pkg_hit[pkg], pkg_found[pkg], package_threshold)
		if (pkg_pct + 0.000001 < package_threshold) {
			printf("coverage-check: package %s coverage %.2f%% is below threshold %.2f%%\n", pkg, pkg_pct, package_threshold) > "/dev/stderr"
			fail = 1
		}
	}
	exit fail
}
' "${exclude_patterns[@]}" -- "${coverage_file}"
