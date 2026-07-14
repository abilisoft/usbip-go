// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire_test

import (
	"bytes"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// FuzzDecodeHeader feeds arbitrary bytes through the OP-header
// decoder. Property under test: the codec MUST NOT panic on any
// input. It is allowed to return an error (truncated, version
// mismatch, unknown opcode, status != 0) but never an uncontrolled
// panic. CGO_ENABLED=0 + go-fuzz native runtime guarantees the
// crash surface is limited to Go panic/runtime errors, which the
// test harness records as findings.
//
// Seed corpus: a handful of representative malformed inputs that
// have been bug-reported historically. New crashes get added to
// testdata/fuzz/FuzzDecodeHeader on discovery.
func FuzzDecodeHeader(f *testing.F) {
	seeds := [][]byte{
		nil,
		{},
		{0x00},
		{0x01, 0x11}, // truncated header
		{0x01, 0x11, 0x80, 0x05, 0x00, 0x00, 0x00, 0x00}, // OP_REQ_DEVLIST
		{0x01, 0x11, 0x00, 0x05, 0x00, 0x00, 0x00, 0x01}, // status != 0
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, // version mismatch
		{0x01, 0x11, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00}, // unknown opcode
		bytes.Repeat([]byte{0xff}, 8),                    // garbage
	}

	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _, _, _ = wire.DecodeHeader(bytes.NewReader(data))
	})
}

// FuzzDecodeOpRepDevlist exercises the device-list reply decoder
// against arbitrary bytes. The decoder enforces a hard cap on
// declared device count (MaxDevlistDevices) and validates each
// device descriptor; it must not panic, allocate without bound,
// or hang on any input.
func FuzzDecodeOpRepDevlist(f *testing.F) {
	var oneDevice bytes.Buffer

	err := wire.EncodeOpRepDevlist(&oneDevice, []domain.Device{{
		Path:  "/sys/devices/1-1",
		BusID: domain.BusID("1-1"),
		Speed: domain.SpeedHigh,
	}})
	if err != nil {
		f.Fatalf("build valid devlist seed: %v", err)
	}

	seeds := [][]byte{
		// Valid complete replies, including the common empty listing and a
		// body-bearing frame. The full header ensures the body decoder runs.
		{0x01, 0x11, 0x00, 0x05, 0, 0, 0, 0, 0, 0, 0, 0},
		oneDevice.Bytes(),
		// Truncated full frame: claims 1 device but has no descriptor.
		{0x01, 0x11, 0x00, 0x05, 0, 0, 0, 0, 0, 0, 0, 1},
		// Hostile full frame: claims max u32 devices.
		{0x01, 0x11, 0x00, 0x05, 0, 0, 0, 0, 0xff, 0xff, 0xff, 0xff},
		nil,
	}

	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _, _ = wire.DecodeOpRepDevlist(bytes.NewReader(data))
	})
}

// FuzzDecodeDevice fuzzes the 312-byte device descriptor decoder.
// Property: every byte sequence either decodes (validated busid +
// fields) or returns an error; never panics.
func FuzzDecodeDevice(f *testing.F) {
	// One valid 312-byte zero-padded descriptor with a syntactically
	// valid busid in slot.
	valid := make([]byte, 312)
	copy(valid[256:], "1-1.2")
	f.Add(valid)
	f.Add([]byte{})
	f.Add(make([]byte, 100)) // truncated

	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _, _ = wire.DecodeDevice(bytes.NewReader(data))
	})
}
