// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"context"
	"net"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// stubExporterKernel is a hand-rolled stand-in for app.ExporterKernel
// covering exactly the methods pkg/usbip forwarding tests exercise.
// Fields are function-typed so each test wires the exact behaviour it
// asserts; unset fields return a zero value / nil error by default.
type stubExporterKernel struct {
	listLocalDevicesFn func(ctx context.Context) ([]domain.Device, error)
	bindFn             func(ctx context.Context, busID domain.BusID) error
	unbindFn           func(ctx context.Context, busID domain.BusID) error
	exportOnConnFn     func(ctx context.Context, conn net.Conn, busID domain.BusID) error
	disconnectFn       func(ctx context.Context, busID domain.BusID) error
	modulesAvailableFn func(ctx context.Context) error
}

// ListLocalDevices dispatches to the hook or returns an empty slice.
func (s *stubExporterKernel) ListLocalDevices(ctx context.Context) ([]domain.Device, error) {
	if s.listLocalDevicesFn != nil {
		return s.listLocalDevicesFn(ctx)
	}

	return nil, nil
}

// ListExportedDevices reuses the same hook as ListLocalDevices —
// the public-facade exporter tests do not exercise the wire-side
// filter; the kernel adapter unit tests cover that path instead.
func (s *stubExporterKernel) ListExportedDevices(ctx context.Context) ([]domain.Device, error) {
	return s.ListLocalDevices(ctx)
}

// Bind dispatches to the hook or returns nil.
func (s *stubExporterKernel) Bind(ctx context.Context, busID domain.BusID) error {
	if s.bindFn != nil {
		return s.bindFn(ctx, busID)
	}

	return nil
}

// Unbind dispatches to the hook or returns nil.
func (s *stubExporterKernel) Unbind(ctx context.Context, busID domain.BusID) error {
	if s.unbindFn != nil {
		return s.unbindFn(ctx, busID)
	}

	return nil
}

// ExportOnConn dispatches to the hook or returns nil.
func (s *stubExporterKernel) ExportOnConn(ctx context.Context, conn net.Conn, busID domain.BusID) error {
	if s.exportOnConnFn != nil {
		return s.exportOnConnFn(ctx, conn, busID)
	}

	return nil
}

// Disconnect dispatches to the hook or returns nil.
func (s *stubExporterKernel) Disconnect(ctx context.Context, busID domain.BusID) error {
	if s.disconnectFn != nil {
		return s.disconnectFn(ctx, busID)
	}

	return nil
}

// ModulesAvailable dispatches to the hook or returns nil.
func (s *stubExporterKernel) ModulesAvailable(ctx context.Context) error {
	if s.modulesAvailableFn != nil {
		return s.modulesAvailableFn(ctx)
	}

	return nil
}
