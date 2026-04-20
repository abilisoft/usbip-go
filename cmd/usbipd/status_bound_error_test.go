package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// errBoundDevicesInjected is the static sentinel the RED uses so
// err113 stays quiet and the test's intent is legible when the
// assertion fires.
var errBoundDevicesInjected = errors.New("injected bound-devices failure")

// erroringStatusSource is a fakeStatusSource-compatible stub whose
// BoundDevices returns a populated error; every other method returns
// empty/nil. Used by the RANK 12 RED to assert the status handler
// surfaces the bound-devices failure rather than silently rendering
// an empty bound_devices array.
type erroringStatusSource struct{}

func (*erroringStatusSource) BoundDevices(_ context.Context) ([]usbip.Device, error) {
	return nil, errBoundDevicesInjected
}

func (*erroringStatusSource) Sessions(_ context.Context) []usbip.Session {
	return nil
}

func (*erroringStatusSource) Listening() listeningState {
	return listeningState{}
}

func (*erroringStatusSource) KernelModules(_ context.Context) (map[string]usbip.ModuleState, error) {
	return map[string]usbip.ModuleState{}, nil
}

func (*erroringStatusSource) Drain(_ context.Context) error { return nil }

// TestStatusBoundDevicesErrorSurfaced proves the RANK 12 contract:
// when ListAvailable fails, the status handler must surface the
// failure via the bound_devices_error JSON field rather than quietly
// handing the client an empty bound_devices array. An operator
// polling / would otherwise see bound_devices=[] and assume the
// daemon has no exports, when the truth is that /sys is inaccessible.
func TestStatusBoundDevicesErrorSurfaced(t *testing.T) {
	t.Parallel()

	src := &erroringStatusSource{}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handleStatusGet(rec, req, src)

	resp := rec.Result()

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))

	require.Contains(t, decoded, "bound_devices_error",
		"ListAvailable failure must surface via bound_devices_error, not an empty bound_devices array")

	require.NotEmpty(t, decoded["bound_devices_error"],
		"bound_devices_error must carry a non-empty human-readable reason")
}
