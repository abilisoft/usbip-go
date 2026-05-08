// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

// TestMain lives in serve_main_test.go because the daemon side needs
// project-local TMPDIR redirection for AF_UNIX bind under sandboxed
// CI. That same TestMain also flips skipFlagCompletionRegistration so
// the importer-side parallel root construction tests stay race-clean
// against cobra's global flagCompletionFunctions map.
