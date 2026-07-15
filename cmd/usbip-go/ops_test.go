// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

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

// runAckSchemaTest drives a single ack-producing subcommand end-to-end
// with --output=json and asserts the resulting bytes start with the
// `{"schema":` prefix. Factored out because the four call sites (attach,
// detach, bind, unbind) only differ in mock wiring + argv.
func runAckSchemaTest(
	t *testing.T,
	imp *mockImporter,
	exp *mockExporter,
	argv []string,
) {
	t.Helper()

	swapFactories(t, imp, exp)

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(argv)

	require.NoError(t, cmd.Execute())
	require.True(
		t,
		bytes.HasPrefix(out.Bytes(), []byte(schemaFirstPrefix)),
		"%v ack must begin with %q, got %s", argv, schemaFirstPrefix, out.String(),
	)
}

// TestAttachAckSchemaFirst — attach ack bytes begin with `{"schema":`.
func TestAttachAckSchemaFirst(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		attachFn: func(
			_ context.Context,
			_ usbip.RemoteEndpoint,
			_ usbip.BusID,
			_ usbip.AttachOptions,
		) (usbip.Port, error) {
			return usbip.Port{ID: 3, Status: domain.StatusUsed, BusID: testNestedBusID}, nil
		},
	}
	runAckSchemaTest(t, imp, &mockExporter{},
		[]string{testOutputJSONFlag, testAttachCommand, testRemoteHost, testNestedBusID})
}

// TestDetachAckSchemaFirst — detach ack bytes begin with `{"schema":`.
func TestDetachAckSchemaFirst(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		detachFn: func(_ context.Context, _ usbip.PortID) error { return nil },
	}
	runAckSchemaTest(t, imp, &mockExporter{},
		[]string{testOutputJSONFlag, testDetachCommand, "3"})
}

// TestBindAckSchemaFirst — bind ack bytes begin with `{"schema":`.
func TestBindAckSchemaFirst(t *testing.T) {
	t.Parallel()

	exp := &mockExporter{
		bindFn: func(_ context.Context, _ usbip.BusID) error { return nil },
	}
	runAckSchemaTest(t, &mockImporter{}, exp,
		[]string{testOutputJSONFlag, testBindCommand, testNestedBusID})
}

// TestUnbindAckSchemaFirst — unbind ack bytes begin with `{"schema":`.
func TestUnbindAckSchemaFirst(t *testing.T) {
	t.Parallel()

	exp := &mockExporter{
		unbindFn: func(_ context.Context, _ usbip.BusID) error { return nil },
	}
	runAckSchemaTest(t, &mockImporter{}, exp,
		[]string{testOutputJSONFlag, testUnbindCommand, testNestedBusID})
}

// TestAttachMissingBusID — `attach <host>` with no busid → cobra
// prints usage, returns error.
func TestAttachMissingBusID(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{testAttachCommand, testRemoteHost})

	err := cmd.Execute()
	require.Error(t, err)
}

// TestAttachSuccessJSON — attach with --output=json emits the ack
// envelope with op=testAttachCommand and port details.
func TestAttachSuccessJSON(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		attachFn: func(
			_ context.Context,
			_ usbip.RemoteEndpoint,
			_ usbip.BusID,
			_ usbip.AttachOptions,
		) (usbip.Port, error) {
			return usbip.Port{ID: 3, Status: domain.StatusUsed, BusID: testNestedBusID}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{testOutputJSONFlag, testAttachCommand, testRemoteHost, testNestedBusID})

	err := cmd.Execute()
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &m))
	require.Equal(t, "v1", m["schema"])
	require.Equal(t, testAttachCommand, m["op"])
}

// TestAttachMalformedBackoff — --backoff=garbage exits with usage.
func TestAttachMalformedBackoff(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{testAttachCommand, "--backoff=nonsense", testRemoteHost, testNestedBusID})

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
	cmd.SetArgs([]string{testOutputJSONFlag, testDetachCommand, "3"})

	err := cmd.Execute()
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &m))
	require.Equal(t, testDetachCommand, m["op"])
}

// TestDetachInvalidPortID — non-numeric port id → usage error.
func TestDetachInvalidPortID(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{testDetachCommand, "notanum"})

	err := cmd.Execute()
	require.Error(t, err)
}

// TestPortNoFilterListsAll — `port` alone lists all attached ports.
func TestPortNoFilterListsAll(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		listPortsFn: func(_ context.Context) ([]usbip.Port, error) {
			return []usbip.Port{
				{ID: 0, Status: domain.StatusUsed, BusID: testNestedBusID},
				{ID: 1, Status: domain.StatusError, BusID: "2-2"},
				{ID: 5, Status: domain.StatusError + 1, BusID: "3-3"},
				{ID: 2, Status: domain.StatusNull},
				{ID: 3, Status: domain.StatusNotAssigned},
				{ID: 4, Status: domain.StatusAvailable},
			}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{testOutputJSONFlag, testPortCommand})

	err := cmd.Execute()
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &m))
	require.Equal(t, "v1", m["schema"])

	ports, _ := m["ports"].([]any)
	require.Len(t, ports, 4)
	require.Contains(t, out.String(), `"status":"not-assigned"`)
	require.Contains(t, out.String(), `"status":"status(5)"`)
}

// TestPortNotAssignedIsActive proves a kernel-claimed port remains visible
// while the virtual device is waiting for its local USB address. Operators
// must be able to discover and detach that in-progress attachment.
func TestPortNotAssignedIsActive(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		listPortsFn: func(_ context.Context) ([]usbip.Port, error) {
			return []usbip.Port{{ID: 43, Status: domain.StatusNotAssigned}}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{testOutputJSONFlag, testPortCommand, "--id=43"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, out.String(), `"id":43`)
	require.Contains(t, out.String(), `"status":"not-assigned"`)
}

// TestPortIDNotAttached — `port --id N` where N is absent → exit 5
// (ErrDeviceNotFound).
func TestPortIDNotAttached(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		listPortsFn: func(_ context.Context) ([]usbip.Port, error) {
			return []usbip.Port{{ID: 0, Status: domain.StatusUsed, BusID: testNestedBusID}}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{testPortCommand, "--id=42"})

	err := cmd.Execute()
	require.Error(t, err)
	require.Equal(t, ExitDeviceNotFound, MapError(err))
}

// TestPortIDFreeNotAttached proves that kernel capacity rows are not
// attachments merely because ListPorts exposes their numeric IDs.
func TestPortIDFreeNotAttached(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status domain.Status
	}{
		{name: testStatusNullName, status: domain.StatusNull},
		{name: testStatusAvailableName, status: domain.StatusAvailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			imp := &mockImporter{
				listPortsFn: func(_ context.Context) ([]usbip.Port, error) {
					return []usbip.Port{{ID: 42, Status: test.status}}, nil
				},
			}
			swapFactories(t, imp, &mockExporter{})

			cmd := newRootCmd()
			cmd.SetArgs([]string{testPortCommand, "--id=42"})

			err := cmd.Execute()
			require.Error(t, err)
			require.Equal(t, ExitDeviceNotFound, MapError(err))
		})
	}
}

// TestBindSuccess — bind <busid> invokes Exporter.Bind.
func TestBindSuccess(t *testing.T) {
	t.Parallel()

	var called bool

	exp := &mockExporter{
		bindFn: func(_ context.Context, b usbip.BusID) error {
			called = true

			require.Equal(t, usbip.BusID(testNestedBusID), b)

			return nil
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{testOutputJSONFlag, testBindCommand, testNestedBusID})

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

			require.Equal(t, usbip.BusID(testNestedBusID), b)

			return nil
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{testOutputJSONFlag, testUnbindCommand, testNestedBusID})

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
	cmd.SetArgs([]string{testBindCommand, "not a busid"})

	err := cmd.Execute()
	require.Error(t, err)
}
