// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

func withModules(names ...string) fstest.MapFS {
	m := fstest.MapFS{}

	for _, n := range names {
		m["sys/module/"+n] = &fstest.MapFile{Mode: fs.ModeDir}
	}

	return m
}

func TestImporterModulesAvailable_AllPresent(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewImporterAdapter(kernel.WithFS(withModules(kernel.ModuleUsbipCore, kernel.ModuleVHCIHCD)))
	require.NoError(t, err)

	require.NoError(t, a.ModulesAvailable(context.Background()))
}

func TestImporterModulesAvailable_MissingVHCI(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewImporterAdapter(kernel.WithFS(withModules(kernel.ModuleUsbipCore)))
	require.NoError(t, err)

	err = a.ModulesAvailable(context.Background())
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
	require.ErrorContains(t, err, "modprobe "+kernel.ModuleVHCIHCD,
		"error hint must include the exact modprobe command")
}

func TestImporterModulesAvailable_MissingCore(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewImporterAdapter(kernel.WithFS(withModules(kernel.ModuleVHCIHCD)))
	require.NoError(t, err)

	err = a.ModulesAvailable(context.Background())
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
	require.ErrorContains(t, err, "modprobe "+kernel.ModuleUsbipCore)
}

func TestExporterModulesAvailable_AllPresent(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewExporterAdapter(kernel.WithFS(withModules(kernel.ModuleUsbipCore, testUeventSubsystemUSBIPHost)))
	require.NoError(t, err)

	require.NoError(t, a.ModulesAvailable(context.Background()))
}

func TestExporterModulesAvailable_MissingHost(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewExporterAdapter(kernel.WithFS(withModules(kernel.ModuleUsbipCore)))
	require.NoError(t, err)

	err = a.ModulesAvailable(context.Background())
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
	require.ErrorContains(t, err, "modprobe "+kernel.ModuleUsbipHost)
}

// TestEventsAdapter_NoModulesAvailableMethod confirms the EventsAdapter
// does NOT expose ModulesAvailable per v1 contract §5.1.
func TestEventsAdapter_NoModulesAvailableMethod(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewEventsAdapter(kernel.WithFS(withModules(kernel.ModuleUsbipCore, kernel.ModuleVHCIHCD)))
	require.NoError(t, err)

	// Runtime assertion: the events adapter's concrete type must not
	// advertise a ModulesAvailable method. A failed interface cast is
	// the only observation tool we have at test time; if somebody
	// adds a ModulesAvailable method to EventsAdapter in the future
	// the cast succeeds and this test fails loudly.
	_, hasMethod := any(a).(modulesAvailabler)
	require.False(t, hasMethod, "EventsAdapter must NOT expose ModulesAvailable per v1 contract §5.1")
}

// modulesAvailabler is the method-set we assert EventsAdapter does not
// satisfy.
type modulesAvailabler interface {
	ModulesAvailable(ctx context.Context) error
}
