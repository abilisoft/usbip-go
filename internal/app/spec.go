// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import "github.com/abilisoft/usbip-go/pkg/domain"

// RemoteDeviceSpec is what OP_REP_IMPORT decodes into. Passed to
// ImporterKernel.AttachRemote by the importer service so the kernel
// adapter can synthesise the usbip_vhci attach payload. The importer
// also supplies ReserveLocalPort as a narrow transaction hook: the
// production adapter invokes it after selecting a free port and before
// starting the kernel handoff, closing the interval in which a
// concurrent Detach could otherwise miss a live-but-unpublished port.
//
// The type lives in internal/app rather than pkg/domain because it is
// an adapter-interface contract, not consumer data. Public callers
// never observe a RemoteDeviceSpec directly — they pass a
// domain.RemoteEndpoint and the importer decodes a spec off the wire.
type RemoteDeviceSpec struct {
	// Device is the 312-byte device body decoded from OP_REP_IMPORT,
	// populated via the wire codec.
	Device domain.Device

	// DevID is the (busnum<<16)|devnum identifier as it appears on the
	// wire. The kernel adapter writes this value into the vhci
	// usbip_sockfd attach payload.
	DevID domain.DeviceID

	// Speed is the negotiated USB speed reported by the remote
	// exporter; also required by the vhci attach payload.
	Speed domain.Speed

	// Remote is the peer that surfaced the spec. Carried through for
	// structured logging and telemetry only.
	Remote domain.RemoteEndpoint

	// ReserveLocalPort publishes the selected local port to the importer
	// before the kernel mutation starts. ImporterKernel implementations
	// MUST invoke a non-nil hook synchronously after port selection and
	// before making the attachment live. Specs constructed outside the
	// importer may leave it nil.
	ReserveLocalPort func(domain.PortID) error
}
