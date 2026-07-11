#!/usr/bin/env bash

set -euo pipefail

source_deb=$1
output_deb=$2
shift 2

case "${output_deb}" in
/*) ;;
*) output_deb="${PWD}/${output_deb}" ;;
esac

work=$(mktemp -d "${TMPDIR:-/tmp}/project-deb-sanitize-XXXXXXXX")
trap 'rm -rf "${work}"' EXIT

cp "${source_deb}" "${work}/package.deb"

(
	cd "${work}"
	ar x package.deb

	mkdir data
	tar -xzf data.tar.gz -C data
	rm -f data.tar.gz

	(
		cd data
		find . -mindepth 1 -print | sort
	) >manifest

	for parent in "$@"; do
		normalized=".${parent%/}"
		grep -Fxv "${normalized}" manifest >manifest.next
		mv manifest.next manifest
	done

	tar --no-recursion --sort=name --owner=0 --group=0 --numeric-owner -cf data.tar -C data -T manifest
	gzip -n data.tar

	mkdir -p "$(dirname "${output_deb}")"
	ar qcD "${output_deb}" debian-binary control.tar.gz data.tar.gz
)
