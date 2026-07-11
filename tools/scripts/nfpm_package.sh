#!/usr/bin/env bash

set -euo pipefail

stage=$1
config=$2
nfpm=$3
packager=$4
target=$5
shift 5

rm -rf "${stage}"
mkdir -p "${stage}"
trap 'rm -rf "${stage}"' EXIT

while (($# > 0)); do
	source=$1
	destination=$2
	shift 2

	mkdir -p "$(dirname "${destination}")"
	cp -fL "${source}" "${destination}"
done

"${nfpm}" package --config "${config}" --packager "${packager}" --target "${target}"
