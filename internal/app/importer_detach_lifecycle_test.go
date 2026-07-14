// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

const detachCoordinationTimeout = 2 * time.Second

var errSharedDetach = errors.New("shared detach failure")

// observedDoneContext signals when Detach starts waiting on the caller's
// cancellation channel. Tests use that observation as a deterministic proof
// that a follower joined the active shared attempt before the owner completes.
type observedDoneContext struct {
	deadline     func() (time.Time, bool)
	done         func() <-chan struct{}
	err          func() error
	value        func(any) any
	doneObserved chan struct{}
	once         sync.Once
}

func (c *observedDoneContext) Deadline() (time.Time, bool) { return c.deadline() }

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.doneObserved) })

	return c.done()
}

func (c *observedDoneContext) Err() error        { return c.err() }
func (c *observedDoneContext) Value(key any) any { return c.value(key) }

func newObservedDoneContext(ctx context.Context) *observedDoneContext {
	return &observedDoneContext{
		deadline:     ctx.Deadline,
		done:         ctx.Done,
		err:          ctx.Err,
		value:        ctx.Value,
		doneObserved: make(chan struct{}),
	}
}

func requireSignal(t *testing.T, ch <-chan struct{}, description string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(detachCoordinationTimeout):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func TestImporterDetachSharesSuccessfulAttempt(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 12

	detachEntered := make(chan struct{})
	releaseDetach := make(chan struct{})

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return portID, nil
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error {
			close(detachEntered)
			<-releaseDetach

			return nil
		},
	}

	imp, _ := attachOnce(t, kernel)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	ownerResult := make(chan error, 1)
	go func() { ownerResult <- imp.Detach(context.Background(), portID) }()

	requireSignal(t, detachEntered, "owner kernel detach")

	followerCtx := newObservedDoneContext(context.Background())

	followerResult := make(chan error, 1)
	go func() { followerResult <- imp.Detach(followerCtx, portID) }()

	requireSignal(t, followerCtx.doneObserved, "follower shared-attempt wait")
	close(releaseDetach)

	require.NoError(t, <-ownerResult)
	require.NoError(t, <-followerResult)
	require.Len(t, kernel.DetachPortCalls(), 1,
		"overlapping callers must share one kernel mutation")
}

func TestImporterDetachSharesFailureAndAllowsRetry(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 13

	firstDetachEntered := make(chan struct{})
	releaseFirstDetach := make(chan struct{})

	var detachCalls atomic.Int32

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return portID, nil
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error {
			if detachCalls.Add(1) == 1 {
				close(firstDetachEntered)
				<-releaseFirstDetach

				return errSharedDetach
			}

			return nil
		},
	}

	imp, _ := attachOnce(t, kernel)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	ownerResult := make(chan error, 1)
	go func() { ownerResult <- imp.Detach(context.Background(), portID) }()

	requireSignal(t, firstDetachEntered, "first kernel detach")

	followerCtx := newObservedDoneContext(context.Background())

	followerResult := make(chan error, 1)
	go func() { followerResult <- imp.Detach(followerCtx, portID) }()

	requireSignal(t, followerCtx.doneObserved, "follower shared-attempt wait")
	close(releaseFirstDetach)

	require.ErrorIs(t, <-ownerResult, errSharedDetach)
	require.ErrorIs(t, <-followerResult, errSharedDetach)
	require.EqualValues(t, 1, detachCalls.Load())

	require.NoError(t, imp.Detach(context.Background(), portID),
		"a completed failed attempt must release ownership for a later retry")
	require.EqualValues(t, 2, detachCalls.Load())
}

func TestImporterDetachFollowerCancellationDoesNotCancelOwner(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 14

	detachEntered := make(chan struct{})
	releaseDetach := make(chan struct{})

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return portID, nil
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error {
			close(detachEntered)
			<-releaseDetach

			return nil
		},
	}

	imp, _ := attachOnce(t, kernel)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	ownerResult := make(chan error, 1)
	go func() { ownerResult <- imp.Detach(context.Background(), portID) }()

	requireSignal(t, detachEntered, "owner kernel detach")

	baseFollowerCtx, cancelFollower := context.WithCancel(context.Background())
	followerCtx := newObservedDoneContext(baseFollowerCtx)

	followerResult := make(chan error, 1)
	go func() { followerResult <- imp.Detach(followerCtx, portID) }()

	requireSignal(t, followerCtx.doneObserved, "follower cancellation wait")
	cancelFollower()
	require.ErrorIs(t, <-followerResult, context.Canceled)

	select {
	case err := <-ownerResult:
		t.Fatalf("owner completed before its kernel mutation was released: %v", err)
	default:
	}

	close(releaseDetach)
	require.NoError(t, <-ownerResult)
	require.Len(t, kernel.DetachPortCalls(), 1)
}

func TestImporterDetachUntrackedSharesSuccessfulAttempt(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 25

	detachEntered := make(chan struct{})
	releaseDetach := make(chan struct{})

	kernel := &ImporterKernelMock{
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error {
			close(detachEntered)
			<-releaseDetach

			return nil
		},
	}

	imp := newImporterForTest(t, app.WithImporterKernel(kernel))
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	ownerResult := make(chan error, 1)
	go func() { ownerResult <- imp.Detach(context.Background(), portID) }()

	requireSignal(t, detachEntered, "untracked owner kernel detach")

	followerCtx := newObservedDoneContext(context.Background())

	followerResult := make(chan error, 1)
	go func() { followerResult <- imp.Detach(followerCtx, portID) }()

	requireSignal(t, followerCtx.doneObserved, "untracked follower shared-attempt wait")
	close(releaseDetach)

	require.NoError(t, <-ownerResult)
	require.NoError(t, <-followerResult)
	require.Len(t, kernel.DetachPortCalls(), 1,
		"overlapping untracked callers must share one kernel mutation")
}

func TestImporterDetachUntrackedSharesFailureAndAllowsRetry(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 26

	firstDetachEntered := make(chan struct{})
	releaseFirstDetach := make(chan struct{})

	var detachCalls atomic.Int32

	kernel := &ImporterKernelMock{
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error {
			if detachCalls.Add(1) == 1 {
				close(firstDetachEntered)
				<-releaseFirstDetach

				return errSharedDetach
			}

			return nil
		},
	}

	imp := newImporterForTest(t, app.WithImporterKernel(kernel))
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	ownerResult := make(chan error, 1)
	go func() { ownerResult <- imp.Detach(context.Background(), portID) }()

	requireSignal(t, firstDetachEntered, "untracked failing kernel detach")

	followerCtx := newObservedDoneContext(context.Background())

	followerResult := make(chan error, 1)
	go func() { followerResult <- imp.Detach(followerCtx, portID) }()

	requireSignal(t, followerCtx.doneObserved, "untracked failing-attempt follower")
	close(releaseFirstDetach)

	require.ErrorIs(t, <-ownerResult, errSharedDetach)
	require.ErrorIs(t, <-followerResult, errSharedDetach)
	require.EqualValues(t, 1, detachCalls.Load())

	require.NoError(t, imp.Detach(context.Background(), portID),
		"a completed untracked failure must release ownership for retry")
	require.EqualValues(t, 2, detachCalls.Load())
}

func TestImporterUntrackedDetachRejectsSamePortReservation(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 27

	detachEntered := make(chan struct{})
	releaseDetach := make(chan struct{})

	var releaseOnce sync.Once

	release := func() {
		releaseOnce.Do(func() { close(releaseDetach) })
	}

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(
			_ context.Context, _ net.Conn, spec app.RemoteDeviceSpec,
		) (domain.PortID, error) {
			reserveErr := spec.ReserveLocalPort(portID)
			if reserveErr == nil {
				return 0, fmt.Errorf("reservation unexpectedly succeeded: %w", errSharedDetach)
			}

			return 0, fmt.Errorf("reserve local port: %w", reserveErr)
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error {
			close(detachEntered)
			<-releaseDetach

			return nil
		},
	}

	imp := newAttachPublicationImporter(
		t, kernel, testutil.NewFakeClockAt(importerTestEpoch()),
	)
	t.Cleanup(func() {
		release()
		require.NoError(t, imp.Close())
	})

	detachResult := make(chan error, 1)
	go func() { detachResult <- imp.Detach(context.Background(), portID) }()

	requireSignal(t, detachEntered, "active untracked detach")

	_, err := imp.Attach(
		context.Background(), testRemote(), attachBusID(), app.AttachOptions{},
	)
	require.ErrorIs(t, err, app.ErrAttachInProgress)
	require.Len(t, kernel.AttachRemoteCalls(), 1)

	release()
	require.NoError(t, <-detachResult)
}

func TestImporterCloseWaitsForUntrackedDetach(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 28

	detachEntered := make(chan struct{})
	releaseDetach := make(chan struct{})

	kernel := &ImporterKernelMock{
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error {
			close(detachEntered)
			<-releaseDetach

			return nil
		},
	}

	clock := newObservedReconnectClock()
	imp := newImporterForTest(
		t,
		app.WithImporterKernel(kernel),
		app.WithImporterClock(clock),
	)

	detachResult := make(chan error, 1)
	go func() { detachResult <- imp.Detach(context.Background(), portID) }()

	requireSignal(t, detachEntered, "untracked detach before Close")

	closeResult := make(chan error, 1)
	go func() { closeResult <- imp.Close() }()

	select {
	case <-clock.calls:
		// Close has armed its bounded wait while the detach still owns the
		// lifecycle wait-group entry.
	case <-time.After(detachCoordinationTimeout):
		t.Fatal("Close did not start its bounded lifecycle wait")
	}

	select {
	case err := <-closeResult:
		t.Fatalf("Close returned before untracked Detach finished: %v", err)
	default:
	}

	close(releaseDetach)
	require.NoError(t, <-detachResult)
	require.NoError(t, <-closeResult)
}

func TestImporterDetachRemovesOnlyItsExactHandle(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 15

	firstDetachEntered := make(chan struct{})
	releaseFirstDetach := make(chan struct{})

	var detachCalls atomic.Int32

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return portID, nil
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error {
			if detachCalls.Add(1) == 1 {
				close(firstDetachEntered)
				<-releaseFirstDetach
			}

			return nil
		},
	}

	imp, _ := attachOnce(t, kernel)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	oldDetachResult := make(chan error, 1)
	go func() { oldDetachResult <- imp.Detach(context.Background(), portID) }()

	requireSignal(t, firstDetachEntered, "old-generation kernel detach")

	replacement, err := imp.Attach(
		context.Background(), testRemote(), attachBusID(), app.AttachOptions{},
	)
	require.NoError(t, err)
	require.Equal(t, portID, replacement.ID)

	close(releaseFirstDetach)
	require.NoError(t, <-oldDetachResult)

	require.NoError(t, imp.Detach(context.Background(), portID),
		"old detach completion must not delete the replacement handle")
	require.EqualValues(t, 2, detachCalls.Load(),
		"the replacement handle must remain registered for its own detach")
}

// newAttachPublicationImporter supplies the minimal transport and codec needed
// to park Attach inside a kernel test double after it reserves a concrete port.
func newAttachPublicationImporter(
	t *testing.T, kernel *ImporterKernelMock, clock app.Clock,
) *app.Importer {
	t.Helper()

	transport := &TransportMock{
		DialFunc: func(
			_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions,
		) (net.Conn, error) {
			return newFakeConn(), nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) {
			return attachDevice(), nil
		},
	}

	return newImporterForTest(
		t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterClock(clock),
	)
}

type attachResult struct {
	port domain.Port
	err  error
}

func TestImporterDetachWaitsForReservedHandlePublication(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 16

	kernelLive := make(chan struct{})
	returnHandoff := make(chan struct{})
	detachEntered := make(chan struct{})
	releaseDetach := make(chan struct{})

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(
			_ context.Context, _ net.Conn, spec app.RemoteDeviceSpec,
		) (domain.PortID, error) {
			reserveErr := spec.ReserveLocalPort(portID)
			if reserveErr != nil {
				return 0, fmt.Errorf("reserve local port: %w", reserveErr)
			}

			// The production adapter performs its sysfs write after the
			// reservation. Treat the port as kernel-live from this point and
			// hold the return to expose the old publication gap deterministically.
			close(kernelLive)
			<-returnHandoff

			return portID, nil
		},
		DetachPortFunc: func(_ context.Context, got domain.PortID) error {
			if got != portID {
				return errSharedDetach
			}

			close(detachEntered)
			<-releaseDetach

			return nil
		},
	}

	imp := newAttachPublicationImporter(
		t, kernel, testutil.NewFakeClockAt(importerTestEpoch()),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	attachDone := make(chan attachResult, 1)

	go func() {
		port, err := imp.Attach(
			context.Background(), testRemote(), attachBusID(), app.AttachOptions{},
		)
		attachDone <- attachResult{port: port, err: err}
	}()

	requireSignal(t, kernelLive, "reserved kernel handoff")

	detachCtx := newObservedDoneContext(context.Background())

	detachDone := make(chan error, 1)
	go func() { detachDone <- imp.Detach(detachCtx, portID) }()

	requireSignal(t, detachCtx.doneObserved, "publication reservation wait")

	select {
	case err := <-detachDone:
		t.Fatalf("Detach returned before handle publication: %v", err)
	default:
	}

	close(returnHandoff)

	attached := <-attachDone
	require.NoError(t, attached.err)
	require.Equal(t, portID, attached.port.ID)

	requireSignal(t, detachEntered, "reserved-port teardown")
	close(releaseDetach)

	require.NoError(t, <-detachDone)
	require.Len(t, kernel.DetachPortCalls(), 1,
		"publication waiter and compensating path must share one teardown")
}

func TestImporterDetachPublicationTimeoutStillCompensates(t *testing.T) {
	t.Parallel()

	const (
		portID             domain.PortID = 17
		publicationTimeout               = 3 * time.Second
	)

	kernelLive := make(chan struct{})
	returnHandoff := make(chan struct{})
	detachEntered := make(chan struct{})
	releaseDetach := make(chan struct{})

	var detachCalls atomic.Int32

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(
			_ context.Context, _ net.Conn, spec app.RemoteDeviceSpec,
		) (domain.PortID, error) {
			reserveErr := spec.ReserveLocalPort(portID)
			if reserveErr != nil {
				return 0, fmt.Errorf("reserve local port: %w", reserveErr)
			}

			close(kernelLive)
			<-returnHandoff

			return portID, nil
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error {
			if detachCalls.Add(1) == 1 {
				close(detachEntered)
				<-releaseDetach

				return errSharedDetach
			}

			return nil
		},
	}

	clock := testutil.NewFakeClockAt(importerTestEpoch())
	imp := newAttachPublicationImporter(t, kernel, clock)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	attachDone := make(chan attachResult, 1)

	go func() {
		port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{
			ShutdownTimeout: publicationTimeout,
		})
		attachDone <- attachResult{port: port, err: err}
	}()

	requireSignal(t, kernelLive, "timed reserved kernel handoff")

	detachCtx := newObservedDoneContext(context.Background())

	firstDetach := make(chan error, 1)
	go func() { firstDetach <- imp.Detach(detachCtx, portID) }()

	requireSignal(t, detachCtx.doneObserved, "timed publication wait")
	require.Equal(t, 1, clock.Pending(), "publication deadline must be armed before observation")

	clock.Advance(publicationTimeout)
	require.ErrorIs(t, <-firstDetach, context.DeadlineExceeded)

	close(returnHandoff)

	attached := <-attachDone
	require.NoError(t, attached.err)
	require.Equal(t, portID, attached.port.ID)

	requireSignal(t, detachEntered, "late compensating teardown")

	followerCtx := newObservedDoneContext(context.Background())

	followerDone := make(chan error, 1)
	go func() { followerDone <- imp.Detach(followerCtx, portID) }()

	requireSignal(t, followerCtx.doneObserved, "compensating teardown follower")

	close(releaseDetach)
	require.ErrorIs(t, <-followerDone, errSharedDetach)
	require.EqualValues(t, 1, detachCalls.Load(),
		"timed-out caller must leave exactly one compensating teardown")

	require.NoError(t, imp.Detach(context.Background(), portID),
		"failed compensation must retain the exact handle for explicit retry")
	require.EqualValues(t, 2, detachCalls.Load())
}
