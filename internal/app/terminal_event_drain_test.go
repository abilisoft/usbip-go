// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestImporterWatchCloseBarrierDrainsAcceptedEventExactlyOnce(t *testing.T) {
	t.Parallel()

	accepted := domain.PortReconnectExhaustedEvent{
		At:       time.Unix(1, 0),
		Attempts: 3,
	}
	postClose := domain.PortReconnectExhaustedEvent{At: time.Unix(2, 0)}

	got := app.ExerciseImporterSubscriberBarrierForTest(accepted, postClose)

	require.True(t, got.PublishActive)
	require.True(t, got.PublishSent)
	require.False(t, got.PostCloseActive)
	require.False(t, got.PostCloseSent)
	require.Equal(t, []domain.Event{accepted}, got.Delivered)
}

func TestExporterWatchCloseBarrierDrainsAcceptedEventExactlyOnce(t *testing.T) {
	t.Parallel()

	accepted := domain.SessionEndedEvent{At: time.Unix(3, 0), Reason: "shutdown"}
	postClose := domain.SessionEndedEvent{At: time.Unix(4, 0), Reason: "late"}

	got := app.ExerciseSessionSubscriberBarrierForTest(accepted, postClose)

	require.True(t, got.PublishActive)
	require.True(t, got.PublishSent)
	require.False(t, got.PostCloseActive)
	require.False(t, got.PostCloseSent)
	require.Equal(t, []domain.Event{accepted}, got.Delivered)
}

func TestImporterWatchSlowSubscriberDropsOverflow(t *testing.T) {
	t.Parallel()

	first := domain.PortReconnectExhaustedEvent{At: time.Unix(5, 0), Attempts: 1}
	overflow := domain.PortReconnectExhaustedEvent{At: time.Unix(6, 0), Attempts: 2}

	got := app.ExerciseImporterSubscriberOverflowForTest(first, overflow)

	require.True(t, got.FirstActive)
	require.True(t, got.FirstSent)
	require.True(t, got.OverflowActive)
	require.False(t, got.OverflowSent)
	require.Equal(t, []domain.Event{first}, got.Buffered)
}

func TestImporterTerminalDrainStopsWhenConsumerStops(t *testing.T) {
	t.Parallel()

	first := domain.PortReconnectExhaustedEvent{At: time.Unix(7, 0), Attempts: 1}
	second := domain.PortReconnectExhaustedEvent{At: time.Unix(8, 0), Attempts: 2}
	delivered := make([]domain.Event, 0, 1)

	remaining := app.DrainImporterSubscriberForTest(
		[]domain.Event{first, second},
		func(event domain.Event, watchErr error) bool {
			require.NoError(t, watchErr)

			delivered = append(delivered, event)

			return false
		},
	)

	require.Equal(t, []domain.Event{first}, delivered)
	require.Equal(t, 1, remaining)
}

func TestExporterTerminalDrainStopsWhenConsumerStops(t *testing.T) {
	t.Parallel()

	first := domain.SessionEndedEvent{At: time.Unix(9, 0), Reason: "shutdown"}
	second := domain.SessionEndedEvent{At: time.Unix(10, 0), Reason: "late"}
	delivered := make([]domain.Event, 0, 1)

	remaining := app.DrainSessionSubscriberForTest(
		[]domain.Event{first, second},
		func(event domain.Event) bool {
			delivered = append(delivered, event)

			return false
		},
	)

	require.Equal(t, []domain.Event{first}, delivered)
	require.Equal(t, 1, remaining)
}
