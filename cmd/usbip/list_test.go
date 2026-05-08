package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"sync"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// mockImporter is a hand-rolled Importer implementation driven by
// per-test hooks. Missing hooks return zero values / nil error so the
// default is a no-op rather than a panic.
type mockImporter struct {
	listRemoteFn func(context.Context, usbip.RemoteEndpoint) ([]usbip.Device, error)
	listPortsFn  func(context.Context) ([]usbip.Port, error)
	attachFn     func(
		context.Context,
		usbip.RemoteEndpoint,
		usbip.BusID,
		usbip.AttachOptions,
	) (usbip.Port, error)
	detachFn func(context.Context, usbip.PortID) error
	watchFn  func(context.Context) iter.Seq[usbip.Event]
	closeFn  func() error
}

func (m *mockImporter) ListRemote(ctx context.Context, r usbip.RemoteEndpoint) ([]usbip.Device, error) {
	if m.listRemoteFn != nil {
		return m.listRemoteFn(ctx, r)
	}

	return nil, nil
}

func (m *mockImporter) ListPorts(ctx context.Context) ([]usbip.Port, error) {
	if m.listPortsFn != nil {
		return m.listPortsFn(ctx)
	}

	return nil, nil
}

func (m *mockImporter) Attach(
	ctx context.Context,
	r usbip.RemoteEndpoint,
	b usbip.BusID,
	o usbip.AttachOptions,
) (usbip.Port, error) {
	if m.attachFn != nil {
		return m.attachFn(ctx, r, b, o)
	}

	return usbip.Port{}, nil
}

func (m *mockImporter) Detach(ctx context.Context, id usbip.PortID) error {
	if m.detachFn != nil {
		return m.detachFn(ctx, id)
	}

	return nil
}

func (m *mockImporter) Watch(ctx context.Context) iter.Seq[usbip.Event] {
	if m.watchFn != nil {
		return m.watchFn(ctx)
	}

	return func(_ func(usbip.Event) bool) {}
}

func (m *mockImporter) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}

	return nil
}

// mockExporter is the Exporter counterpart.
type mockExporter struct {
	listAvailableFn func(context.Context) ([]usbip.Device, error)
	bindFn          func(context.Context, usbip.BusID) error
	unbindFn        func(context.Context, usbip.BusID) error
}

func (m *mockExporter) ListAvailable(ctx context.Context) ([]usbip.Device, error) {
	if m.listAvailableFn != nil {
		return m.listAvailableFn(ctx)
	}

	return nil, nil
}

func (m *mockExporter) Bind(ctx context.Context, b usbip.BusID) error {
	if m.bindFn != nil {
		return m.bindFn(ctx, b)
	}

	return nil
}

func (m *mockExporter) Unbind(ctx context.Context, b usbip.BusID) error {
	if m.unbindFn != nil {
		return m.unbindFn(ctx, b)
	}

	return nil
}

// factoriesMu serialises swapFactories so parallel subtests sharing the
// package-level factory vars don't clobber each other's mocks.
var factoriesMu sync.Mutex

// swapFactories installs mock factories for the duration of the test.
// The t.Cleanup restores the originals. swapFactories acquires
// factoriesMu and releases it in the cleanup so concurrent tests wait
// until the active test finishes before their own swap takes effect.
func swapFactories(t *testing.T, imp *mockImporter, exp *mockExporter) {
	t.Helper()

	factoriesMu.Lock()

	origImp := newImporter
	origExp := newExporter

	newImporter = func(_ ...usbip.ImporterOption) (Importer, error) {
		if imp == nil {
			return &mockImporter{}, nil
		}

		return imp, nil
	}

	newExporter = func(_ ...usbip.ExporterOption) (Exporter, error) {
		if exp == nil {
			return &mockExporter{}, nil
		}

		return exp, nil
	}

	t.Cleanup(func() {
		newImporter = origImp
		newExporter = origExp
		factoriesMu.Unlock()
	})
}

// errTest is a package-scoped sentinel used by tests that need to
// inject a deterministic failure. Defined once (not per-test) to
// satisfy err113 without sprinkling //nolint directives.
var errTest = errors.New("dial failed")

// sampleDevice is a minimal deterministic device fixture.
func sampleDevice() usbip.Device {
	return usbip.Device{
		BusID:     domain.BusID("1-1.2"),
		BusNum:    1,
		DevNum:    2,
		Speed:     domain.SpeedHigh,
		VendorID:  0x0951,
		ProductID: 0x1666,
	}
}

// TestListRequiresOneOfFlags — running `list` with no -r/-l/-p returns
// a usage error per spec §7.4.1.
func TestListRequiresOneOfFlags(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"list"})

	err := cmd.Execute()
	require.Error(t, err)
}

// TestListRemoteAndLocalMutuallyExclusive — spec §7.4.1 rule.
func TestListRemoteAndLocalMutuallyExclusive(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"list", "-r", "10.0.0.5", "-l"})

	err := cmd.Execute()
	require.Error(t, err)
	// Cobra phrasing: "if any flags in the group ... are set none of the
	// others can be". We key on the group-name substring since the
	// library wording has varied across versions.
	require.Contains(t, err.Error(), "remote local ports")
}

// TestListRemoteJSONHasSchemaV1 — spec §7.5 schema envelope.
func TestListRemoteJSONHasSchemaV1(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		listRemoteFn: func(_ context.Context, _ usbip.RemoteEndpoint) ([]usbip.Device, error) {
			return []usbip.Device{sampleDevice()}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--output=json", "list", "-r", "10.0.0.5"})

	err := cmd.Execute()
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &m))
	require.Equal(t, "v1", m["schema"])
	require.Contains(t, m, "devices")
}

// TestListLocalJSON — `list -l --output=json` renders ListAvailable.
func TestListLocalJSON(t *testing.T) {
	t.Parallel()

	exp := &mockExporter{
		listAvailableFn: func(_ context.Context) ([]usbip.Device, error) {
			return []usbip.Device{sampleDevice()}, nil
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--output=json", "list", "-l"})

	err := cmd.Execute()
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &m))
	require.Equal(t, "v1", m["schema"])
}

// TestListPortsJSON — `list -p --output=json` renders ListPorts.
func TestListPortsJSON(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		listPortsFn: func(_ context.Context) ([]usbip.Port, error) {
			return []usbip.Port{{ID: 0, Status: domain.StatusUsed, BusID: domain.BusID("1-1.2")}}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--output=json", "list", "-p"})

	err := cmd.Execute()
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &m))
	require.Equal(t, "v1", m["schema"])
	require.Contains(t, m, "ports")
}

// TestListRemoteTable — table output contains the busid somewhere.
func TestListRemoteTable(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		listRemoteFn: func(_ context.Context, _ usbip.RemoteEndpoint) ([]usbip.Device, error) {
			return []usbip.Device{sampleDevice()}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"list", "-r", "10.0.0.5"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, out.String(), "1-1.2")
}

// TestListRemoteError — an error from ListRemote propagates.
func TestListRemoteError(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		listRemoteFn: func(_ context.Context, _ usbip.RemoteEndpoint) ([]usbip.Device, error) {
			return nil, errTest
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"list", "-r", "10.0.0.5"})

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "dial failed")
}
