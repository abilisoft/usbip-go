// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestExporterServe_DevlistEmitsAccepted pins the accept counter
// contract on the OP_REQ_DEVLIST branch. The serve loop emits the
// usbip_exporter_sessions_accepted_total counter on every handshake
// terminal transition; the devlist branch was missing the
// OutcomeHandshakeOK emission, so accept-rate dashboards undercounted
// every `usbip list -r` request by 100%.
func TestExporterServe_DevlistEmitsAccepted(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := app.MustNewMetrics(reg)

	want := []domain.Device{{BusID: domain.BusID("1-1")}}

	kernel := &ExporterKernelMock{
		ListLocalDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return want, nil
		},
		ListExportedDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return want, nil
		},
	}

	replyBody := []byte("REPLY")
	replyWritten := make(chan []domain.Device, 1)

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		EncodeOpRepDevlistFunc: func(w io.Writer, devs []domain.Device) error {
			_, err := w.Write(replyBody)
			if err != nil {
				return fmt.Errorf("write reply: %w", err)
			}

			replyWritten <- devs

			return nil
		},
	}

	lis := newPipeListener()

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
		app.WithExporterMetrics(metrics),
	)

	ctx, cancel := context.WithCancel(context.Background())

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)

	_, err = client.Write(opHeader(wire.OpReqDevlist))
	require.NoError(t, err)

	got, err := drainN(client, len(replyBody))
	require.NoError(t, err)
	require.Equal(t, replyBody, got)

	_, err = client.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, client.Close())

	select {
	case devs := <-replyWritten:
		require.Equal(t, want, devs)
	case <-time.After(2 * time.Second):
		t.Fatal("EncodeOpRepDevlist was not invoked")
	}

	cancel()
	require.NoError(t, <-serveDone)

	fams, err := reg.Gather()
	require.NoError(t, err)

	famByName := make(map[string]*dto.MetricFamily, len(fams))
	for _, f := range fams {
		famByName[f.GetName()] = f
	}

	accepted := counterLabelValue(t, famByName,
		"usbip_exporter_sessions_accepted_total",
		"outcome", string(app.OutcomeHandshakeOK))
	require.InDelta(t, 1.0, accepted, 0.0001,
		"devlist handshake must increment ExporterSessionAccepted(handshake_ok)")
}
