#!/usr/bin/env bash

set -euo pipefail

tmpfile=$(mktemp)
trap 'rm -f "${tmpfile}"' EXIT
if ! go list -f '{{ if .CgoFiles }}{{ .ImportPath }}{{ end }}' ./... >"${tmpfile}"; then
	printf 'go list ./... failed — cannot enforce no-cgo policy\n' >&2
	cat "${tmpfile}" >&2
	exit 1
fi

cgo_pkgs=$(grep -v '^$' "${tmpfile}" || true)
if [[ -n "${cgo_pkgs}" ]]; then
	printf 'found packages with cgo files — CGO_ENABLED=0 policy violation:\n%s\n' "${cgo_pkgs}" >&2
	exit 1
fi

if grep -rnE '(^|[[:space:]])import[[:space:]]+"C"' --include='*.go' .; then
	printf 'found single-line import "C" — CGO_ENABLED=0 policy violation\n' >&2
	exit 1
fi

if ! find . -name '*.go' -not -path './.git/*' -print0 | xargs -0 -r awk '
	/^import[[:space:]]*\(/ { in_block = 1; next }
	in_block && /^\)/ { in_block = 0; next }
	in_block && /^[[:space:]]*"C"/ {
		print FILENAME ":" FNR ": block-form import \"C\" (cgo)"
		found = 1
	}
	END { exit found ? 1 : 0 }
'; then
	printf 'found block-form import "C" — CGO_ENABLED=0 policy violation\n' >&2
	exit 1
fi
