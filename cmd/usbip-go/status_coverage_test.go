// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// errKernelModulesProbe is the static sentinel returned by the fake
// status source when modulesErr is set; defining it here (rather than
// inline via errors.New) keeps the err113 linter happy and lets future
// tests assert the exact error via errors.Is.
var errKernelModulesProbe = errors.New("kernel-module probe failed")

// errSimulatedWriteDrop is the static sentinel returned by
// errResponseWriter.Write to force json.Encoder.Encode into its error
// branch; static-error policy (err113) requires a package-level var
// rather than a per-call errors.New.
var errSimulatedWriteDrop = errors.New("simulated network drop")

// errSimulatedDrain is the static sentinel returned by the fake
// status source when drainErr is set so the drain-error log branch
// in handleStatusDrain is exercised deterministically.
var errSimulatedDrain = errors.New("simulated drain failure")

// safeBuffer is a bytes.Buffer with mutex-protected Write/String so
// async log emissions from Drain goroutines don't race the main test
// goroutine reading the captured output. slog.Handler implementations
// call Write while the test reads String() concurrently — without
// this guard `go test -race` flags the access.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, err := s.buf.Write(p)
	if err != nil {
		// bytes.Buffer.Write never returns a non-nil error per the
		// stdlib contract; the wrap is purely to satisfy wrapcheck
		// without suppressing it.
		return n, fmt.Errorf("safeBuffer write: %w", err)
	}

	return n, nil
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.String()
}

// ctxWithCapturedLogger returns a context carrying a JSON slog logger
// writing to a returned safeBuffer; tests use it to assert that a
// specific handler emitted (or did NOT emit) a given message via the
// ctx-bound logger path rather than slog.Default. This is the
// behaviour pin Codex flagged as missing from the first revision of
// these coverage tests.
func ctxWithCapturedLogger(t *testing.T) (context.Context, *safeBuffer) {
	t.Helper()

	buf := &safeBuffer{}
	log := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := context.WithValue(t.Context(), loggerContextKey{}, log)

	return ctx, buf
}

// TestHandleStatusGet_KernelModulesError covers the
// kernel-module-probe-failed log branch and pins that the error is
// routed through the ctx-bound logger (loggerOrDefault path), not
// slog.Default. handleStatusGet treats a KernelModules error as
// best-effort: the response is still served (status 200, schema v1)
// and the failure is surfaced via a structured log line carrying the
// probe error.
func TestHandleStatusGet_KernelModulesError(t *testing.T) {
	t.Parallel()

	src := &fakeStatusSource{
		modulesErr: errKernelModulesProbe,
	}

	ctx, buf := ctxWithCapturedLogger(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)

	handleStatusGet(rec, req, src)

	require.Equal(t, http.StatusOK, rec.Code,
		"kernel-module probe error MUST NOT degrade the / handler — the schema still serves")
	require.Contains(t, rec.Body.String(), "\"schema\":\"v1\"",
		"v1 schema envelope still present despite probe failure")

	logs := buf.String()
	require.Contains(t, logs, "status: kernel-module probe failed",
		"the probe failure MUST be logged via the ctx-bound logger so operators "+
			"see it in journald rather than the line vanishing into a swallowed error")
	require.Contains(t, logs, errKernelModulesProbe.Error(),
		"the underlying probe error MUST appear in the structured log payload")
}

// errResponseWriter returns errSimulatedWriteDrop from every Write
// call, forcing json.Encoder.Encode to bubble up an error so the
// "encode response failed" branch in handleStatusGet is exercised.
type errResponseWriter struct {
	header http.Header
}

func (e *errResponseWriter) Header() http.Header {
	if e.header == nil {
		e.header = http.Header{}
	}

	return e.header
}

func (e *errResponseWriter) Write(_ []byte) (int, error) {
	return 0, errSimulatedWriteDrop
}

func (e *errResponseWriter) WriteHeader(_ int) {}

// TestHandleStatusGet_EncodeError covers the "status: encode response
// failed" warn-log branch and pins it to the ctx-bound logger. The
// handler MUST NOT panic when the underlying ResponseWriter fails
// mid-encode (e.g. client closed the UDS connection partway through
// the JSON serialisation); the failure is surfaced as a structured
// log line carrying the simulated write error and the request returns
// cleanly.
func TestHandleStatusGet_EncodeError(t *testing.T) {
	t.Parallel()

	src := &fakeStatusSource{}
	w := &errResponseWriter{}
	ctx, buf := ctxWithCapturedLogger(t)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)

	require.NotPanics(t, func() {
		handleStatusGet(w, req, src)
	}, "handler MUST NOT panic when the response writer errors")

	logs := buf.String()
	require.Contains(t, logs, "status: encode response failed",
		"a mid-encode write failure MUST be logged so the operator sees the "+
			"truncated response in journald rather than a silent error")
	require.Contains(t, logs, errSimulatedWriteDrop.Error(),
		"the underlying write error MUST appear in the structured log payload")
}

// TestHandleStatusDrain_DrainError covers the drain-error log branch
// and pins it to the ctx-bound logger. The handler is fire-and-forget
// — it returns 202 Accepted regardless of whether the underlying
// Drain ultimately fails. The error is captured via slog at Error
// level so operators see it in journald rather than vanishing into a
// swallowed goroutine return. We poll for the log emission directly
// (rather than just drainCalled) because the log call happens AFTER
// Drain returns; an early test exit would race the goroutine.
func TestHandleStatusDrain_DrainError(t *testing.T) {
	t.Parallel()

	src := &fakeStatusSource{
		drainErr: errSimulatedDrain,
	}
	rec := httptest.NewRecorder()
	started := &atomic.Bool{}

	ctx, buf := ctxWithCapturedLogger(t)

	handleStatusDrain(ctx, started, rec, src)

	require.Equal(t, http.StatusAccepted, rec.Code,
		"first POST /drain MUST return 202 even if the underlying Drain will fail")

	// Synchronise on the log emission, not on drainCalled — the log
	// call is the contract we're pinning, and it happens AFTER Drain
	// returns the error. drainCalled increments BEFORE Drain returns
	// so polling on it would leave the log assertion racy.
	require.Eventually(t, func() bool {
		return strings.Contains(buf.String(), "status: drain returned error")
	}, time.Second, 10*time.Millisecond,
		"async Drain goroutine MUST emit the error log via the ctx-bound logger")

	require.Contains(t, buf.String(), errSimulatedDrain.Error(),
		"the underlying drain error MUST appear in the structured log payload")
	require.GreaterOrEqual(t, src.drainCalled.Load(), int32(1),
		"Drain MUST have been invoked exactly once for the first POST")
}

// TestApplyStatusSocketACL_ChownPermissionDenied covers the
// "chown skipped (non-fatal)" log branch and pins it to the
// ctx-bound logger. A non-root process cannot chown a file to a
// group it does not belong to (EPERM); we verify that the helper
// logs the failure via the ctx-bound logger and returns cleanly
// without aborting startup.
//
// Skipped when:
//   - running as root: root can chown to any group, hitting the
//     success path instead of the EPERM branch we want to cover;
//   - "root" group is unknown to NSS (minimal containers without
//     /etc/group entries): the lookup fails before chown runs and
//     a different branch (the lookup-failed log at status.go:368) is
//     exercised — TestApplyStatusSocketACL_UnknownGroupRoutesViaCtxLogger
//     already covers that case.
func TestApplyStatusSocketACL_ChownPermissionDenied(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("test exercises EPERM from os.Chown; skipped under root because root can chown to any group")
	}

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	f, err := os.OpenFile(filepath.Clean(sockPath), os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	ctx, buf := ctxWithCapturedLogger(t)

	require.NotPanics(t, func() {
		applyStatusSocketACL(ctx, sockPath, "root")
	}, "applyStatusSocketACL MUST NOT panic when chown is denied — chown is a non-fatal convenience")

	logs := buf.String()
	// Branch the assertion based on which path was actually hit:
	// minimal containers without /etc/group hit the lookup-failed
	// branch; full hosts hit the chown-skipped branch. Either way
	// the contract — log via the ctx-bound logger, do not abort —
	// must hold. Without this dual check the test silently passes
	// in the container even when it doesn't exercise the intended
	// branch.
	switch {
	case strings.Contains(logs, "status-socket chown skipped"):
		// Intended path — chown EPERM branch covered.
	case strings.Contains(logs, "status-socket group lookup failed"):
		t.Skipf("/etc/group has no 'root' entry on this host; the lookup-failed " +
			"branch fires instead of the chown EPERM branch under test")
	default:
		t.Fatalf("expected either chown-skipped or lookup-failed log entry; got: %s", logs)
	}

	info, err := os.Stat(sockPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"the file mode is unaffected by the chown helper, even on the EPERM path")
}
