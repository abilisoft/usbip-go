// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestParseRemoteHostLabelChars exercises every branch of the
// internal isHostLabelChar / validateHostLabelRune classifier:
// lowercase, uppercase, digits, hyphen, and rejected category
// (underscore — RFC 1034 disallows it in hostnames). Existing
// TestParseRemote covers the lowercase-only common case; this
// table adds the remaining alphabet/digit branches so the
// validator's switch statement reaches 100%.
func TestParseRemoteHostLabelChars(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"lowercase only", testHostLabel, false},
		{"uppercase only", "HOST", false},
		{"mixed alpha", "MixedHost", false},
		{"digits in label", "host123", false},
		{"hyphen mid label", "host-name", false},
		{"all digits", "1234", false},
		{"underscore rejected", "bad_host", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := domain.ParseRemote(tc.input)

			if tc.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
		})
	}
}
