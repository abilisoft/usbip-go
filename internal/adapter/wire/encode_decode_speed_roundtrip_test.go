// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// offDevSpeedWire pins the speed field offset inside the encoded
// device descriptor: path[256] + busid[32] + busnum u32 + devnum u32.
// Duplicated from layout.go's offDevSpeed (unexported) so a future
// silent shift of the field rejects this test rather than passing
// silently with both encode and decode rotating together.
const offDevSpeedWire = 296

// TestEncodeDecodeDeviceSpeed_AllValuesRoundTrip pins encode/decode
// symmetry for every domain.Speed value the kernel adapter can populate.
// Existing fixture tests only cover HS + SS; this complements them by
// closing the gap on Low, Full, Wireless, and SuperPlus so a wire-format
// drift in any single speed cannot ship.
func TestEncodeDecodeDeviceSpeed_AllValuesRoundTrip(t *testing.T) {
	t.Parallel()

	speeds := []domain.Speed{
		domain.SpeedUnknown,
		domain.SpeedLow,
		domain.SpeedFull,
		domain.SpeedHigh,
		domain.SpeedWireless,
		domain.SpeedSuper,
		domain.SpeedSuperPlus,
	}

	for _, sp := range speeds {
		t.Run(sp.String(), func(t *testing.T) {
			t.Parallel()

			orig := domain.Device{
				Path:          "/sys/bus/usb/devices/1-1",
				BusID:         "1-1",
				BusNum:        1,
				DevNum:        7,
				Speed:         sp,
				VendorID:      0x0951,
				ProductID:     0x1666,
				BcdDevice:     0x1100,
				Class:         0x09,
				Subclass:      0x00,
				Protocol:      0x00,
				ConfigValue:   1,
				NumConfigs:    1,
				NumInterfaces: 1,
			}

			var encoded bytes.Buffer
			require.NoError(t, wire.EncodeDevice(&encoded, orig),
				"EncodeDevice must accept every in-enum Speed value")

			// Pin the wire-level encoding: 4 bytes big-endian at offset 296
			// must equal the integer value of the Speed enum. Catches a
			// silent rotation of the encoded layout that an encode/decode
			// symmetry test alone could not.
			raw := encoded.Bytes()
			require.GreaterOrEqual(t, len(raw), offDevSpeedWire+4)

			gotWire := binary.BigEndian.Uint32(raw[offDevSpeedWire : offDevSpeedWire+4])
			require.Equal(t, uint32(sp), gotWire,
				"encoded speed bytes at offset %d must equal big-endian uint32(Speed)", offDevSpeedWire)

			got, flags, err := wire.DecodeDevice(bytes.NewReader(raw))
			require.NoError(t, err,
				"DecodeDevice must accept its own EncodeDevice output for Speed=%s", sp)
			require.Equal(t, sp, got.Speed,
				"speed must round-trip identically through encode/decode")
			require.Empty(t, flags.TruncatedPaddedStrings,
				"short canonical path/busid must not flag truncation")
			require.False(t, flags.TrailingBytes,
				"single device decode must not report trailing bytes")
		})
	}
}
