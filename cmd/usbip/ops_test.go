package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestAttachMissingBusID — `attach <host>` with no busid → cobra
// prints usage, returns error.
func TestAttachMissingBusID(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"attach", "10.0.0.5"})

	err := cmd.Execute()
	require.Error(t, err)
}

// TestAttachSuccessJSON — attach with --output=json emits the ack
// envelope with op="attach" and port details.
func TestAttachSuccessJSON(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		attachFn: func(
			_ context.Context,
			_ usbip.RemoteEndpoint,
			_ usbip.BusID,
			_ usbip.AttachOptions,
		) (usbip.Port, error) {
			return usbip.Port{ID: 3, Status: domain.StatusUsed, BusID: "1-1.2"}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--output=json", "attach", "10.0.0.5", "1-1.2"})

	err := cmd.Execute()
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &m))
	require.Equal(t, "v1", m["schema"])
	require.Equal(t, "attach", m["op"])
}

// TestAttachMalformedBackoff — --backoff=garbage exits with usage.
func TestAttachMalformedBackoff(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"attach", "--backoff=nonsense", "10.0.0.5", "1-1.2"})

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid backoff spec")
}

// TestDetachSuccess — detach <port> returns nil and JSON ack has op=detach.
func TestDetachSuccess(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		detachFn: func(_ context.Context, _ usbip.PortID) error {
			return nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--output=json", "detach", "3"})

	err := cmd.Execute()
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &m))
	require.Equal(t, "detach", m["op"])
}

// TestDetachInvalidPortID — non-numeric port id → usage error.
func TestDetachInvalidPortID(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"detach", "notanum"})

	err := cmd.Execute()
	require.Error(t, err)
}

// TestPortNoFilterListsAll — `port` alone lists all attached ports.
func TestPortNoFilterListsAll(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		listPortsFn: func(_ context.Context) ([]usbip.Port, error) {
			return []usbip.Port{{ID: 0, BusID: "1-1.2"}, {ID: 1, BusID: "2-2"}}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--output=json", "port"})

	err := cmd.Execute()
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &m))
	require.Equal(t, "v1", m["schema"])

	ports, _ := m["ports"].([]any)
	require.Len(t, ports, 2)
}

// TestPortIDNotAttached — `port --id N` where N is absent → exit 5
// (ErrDeviceNotFound).
func TestPortIDNotAttached(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		listPortsFn: func(_ context.Context) ([]usbip.Port, error) {
			return []usbip.Port{{ID: 0, BusID: "1-1.2"}}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"port", "--id=42"})

	err := cmd.Execute()
	require.Error(t, err)
	require.Equal(t, ExitDeviceNotFound, MapError(err))
}

// TestBindSuccess — bind <busid> invokes Exporter.Bind.
func TestBindSuccess(t *testing.T) {
	t.Parallel()

	var called bool

	exp := &mockExporter{
		bindFn: func(_ context.Context, b usbip.BusID) error {
			called = true

			require.Equal(t, usbip.BusID("1-1.2"), b)

			return nil
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--output=json", "bind", "1-1.2"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.True(t, called)
}

// TestUnbindSuccess — unbind <busid> invokes Exporter.Unbind.
func TestUnbindSuccess(t *testing.T) {
	t.Parallel()

	var called bool

	exp := &mockExporter{
		unbindFn: func(_ context.Context, b usbip.BusID) error {
			called = true

			require.Equal(t, usbip.BusID("1-1.2"), b)

			return nil
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--output=json", "unbind", "1-1.2"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.True(t, called)
}

// TestBindInvalidBusID — malformed busid fails domain validation.
func TestBindInvalidBusID(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"bind", "not a busid"})

	err := cmd.Execute()
	require.Error(t, err)
}
