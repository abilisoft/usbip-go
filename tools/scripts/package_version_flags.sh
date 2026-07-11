#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly HARNESS_PACKAGE_RELEASE="1"
readonly HARNESS_APK_PACKAGE_RELEASE="0"

version=$("${SCRIPT_DIR}/package_version.sh")

printf '%s=%s ' "--//tools/bazel:harness_package_version" "${version}"
printf '%s=%s ' "--//tools/bazel:harness_package_release" "${HARNESS_PACKAGE_RELEASE}"
printf '%s=%s\n' "--//tools/bazel:harness_apk_package_release" "${HARNESS_APK_PACKAGE_RELEASE}"
