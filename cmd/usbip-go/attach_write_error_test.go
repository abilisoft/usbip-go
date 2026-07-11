// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// TestRenderAttachResult_WriteAckError pins the error path at the first
// fmt.Fprintln inside renderAttachResult (table mode). When the writer
// rejects the ack line the function must return "write attach ack: ..."
// without proceeding to the port table render.
func TestRenderAttachResult_WriteAckError(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}
	cmd.SetOut(failWriter{})
	cmd.SetErr(failWriter{})
	cmd.SetContext(withOutputCtx(context.Background(), outputTable))

	port := usbip.Port{
		ID:    3,
		BusID: domain.BusID(testRootBusID),
	}

	err := renderAttachResult(cmd, port)
	require.Error(t, err)
	require.Contains(t, err.Error(), "write attach ack")
}
