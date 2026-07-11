// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/require"
)

var errSessionIDRandomSource = errors.New("session id random source")

func TestNewSessionIDReturnsRandomSourceError(t *testing.T) {
	t.Parallel()

	id, err := newSessionID(
		iotest.ErrReader(errSessionIDRandomSource),
		time.UnixMilli(0),
	)

	require.Zero(t, id)
	require.ErrorIs(t, err, errSessionIDRandomSource)
}
