#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=tools/scripts/version_lib.sh
source "${SCRIPT_DIR}/version_lib.sh"

harness_pep440_version
