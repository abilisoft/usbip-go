#!/usr/bin/env bash

# Workspace status command for Bazel release stamping.
# Produces stable release metadata from canonical vMAJOR.MINOR.PATCH tags.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=tools/scripts/version_lib.sh
source "${SCRIPT_DIR}/version_lib.sh"

echo "STABLE_VERSION $(harness_pep440_version)"
echo "STABLE_GIT_COMMIT $(harness_git_full_sha)"
echo "STABLE_GIT_SHA_SHORT $(harness_git_short_sha)"
echo "STABLE_GIT_DIRTY $(harness_git_dirty_flag)"
echo "STABLE_BUILD_DATE $(harness_git_commit_date)"
