// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// This file isolates the statusExporter methods that pass straight
// through to the concrete *usbip.Exporter (BoundDevices / Sessions /
// Drain) and the defaultKernelModuleProbe wrapper. None has a test
// seam: each calls a real Exporter or real /sys probe whose
// behaviour is exercised end-to-end by the integration suite. The
// project's hermetic coverage gate carves these out via
// .testcoverage.yaml so the unit-test floor stays meaningful;
// integration coverage is tracked separately.

// defaultKernelModuleProbe wraps usbip.ProbeKernelModules. Used as the
// production default for statusExporter.kernelModuleProbe; extracted
// so the method value is stable (rather than a fresh closure per
// instance) and so the wrap adds an error prefix that tests do not
// have to reproduce.
func defaultKernelModuleProbe(ctx context.Context) (map[string]usbip.ModuleState, error) {
	mods, err := usbip.ProbeKernelModules(ctx)
	if err != nil {
		return mods, fmt.Errorf("probe kernel modules: %w", err)
	}

	return mods, nil
}

// BoundDevices reports the current EXPORT list — devices currently
// claimed by usbip-host that are not actively attached by an
// importer. Uses ListExported (filtered driver=usbip-host AND
// usbip_status != USED) so the JSON field name matches its
// content; a ListAvailable snapshot would also include unbound
// USB devices and contradict the field's semantics. A failure
// propagates to the handler so GET / can render a
// bound_devices_error field rather than masquerading the failure
// as an empty bound_devices array.
func (s *statusExporter) BoundDevices(ctx context.Context) ([]usbip.Device, error) {
	devs, err := s.exp.ListExported(ctx)
	if err != nil {
		return nil, fmt.Errorf("list bound devices: %w", err)
	}

	return devs, nil
}

// Sessions mirrors Exporter.Sessions; the caller owns the returned
// slice per the pkg/usbip contract.
func (s *statusExporter) Sessions(ctx context.Context) []usbip.Session {
	return s.exp.Sessions(ctx)
}

// Drain flips accepting=false and fires the run-side cancellation (if
// installed) so Serve returns. runDaemon exclusively owns the subsequent
// bounded Exporter.Shutdown and keeps the status socket alive until it
// completes; performing Shutdown here would let Serve return and tear down
// status while this goroutine was still draining.
func (s *statusExporter) Drain(_ context.Context) error {
	s.markAccepting(false)

	cancel := s.drain.Load()
	if cancel != nil {
		(*cancel)()
	}

	return nil
}
