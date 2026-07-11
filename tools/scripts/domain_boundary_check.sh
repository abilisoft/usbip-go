#!/usr/bin/env bash

set -euo pipefail

if [[ ! -d pkg/domain ]]; then
	printf 'pkg/domain not found — DDD value-object layer is missing\n' >&2
	exit 1
fi

if grep -rEn 'github\.com/abilisoft/usbip-go/internal/' pkg/domain/; then
	printf 'pkg/domain imports internal/ — boundary violation\n' >&2
	exit 1
fi

tmpfile=$(mktemp)
trap 'rm -f "${tmpfile}"' EXIT
dollar='$'
template="{{ ${dollar}pkg := .ImportPath }}{{ range .Imports }}{{ ${dollar}pkg }} -> {{ . }}{{ \"\\n\" }}{{ end }}"
if ! go list -f "${template}" ./pkg/domain/... >"${tmpfile}"; then
	printf 'go list ./pkg/domain/... failed — cannot enforce boundary\n' >&2
	cat "${tmpfile}" >&2
	exit 1
fi

offenders=$(awk -F' -> ' '$2 ~ /\./ && $2 !~ /^github\.com\/abilisoft\/usbip-go\// { print $0 }' "${tmpfile}")
if [[ -n "${offenders}" ]]; then
	printf 'pkg/domain has third-party imports — pure-stdlib invariant violated:\n%s\n' "${offenders}" >&2
	exit 1
fi

if [[ -d internal/app ]] && grep -rEn 'github\.com/abilisoft/usbip-go/internal/adapter/(kernel|transport)' internal/app/; then
	printf 'internal/app imports internal/adapter/{kernel,transport} — boundary violation\n' >&2
	exit 1
fi
