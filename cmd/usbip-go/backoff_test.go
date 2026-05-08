// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestParseBackoff covers every accepted grammar plus the rejection
// paths for malformed specs. Constructor outputs are typed (not
// stringly compared) so a future rename of FixedBackoff /
// ExponentialBackoff fails the test loudly.
func TestParseBackoff(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		spec     string
		wantErr  bool
		wantKind string
		wantD    time.Duration
	}{
		{name: "fixed-1s", spec: "fixed:1s", wantKind: "fixed", wantD: time.Second},
		{name: "fixed-zero", spec: "fixed:0s", wantKind: "fixed", wantD: 0},
		{name: "exp-100ms-30s", spec: "exp:100ms:30s", wantKind: "exp"},
		{name: "no-separator", spec: "garbage", wantErr: true},
		{name: "unknown-kind", spec: "wat:5s", wantErr: true},
		{name: "fixed-bad-duration", spec: "fixed:not-a-duration", wantErr: true},
		{name: "fixed-negative", spec: "fixed:-1s", wantErr: true},
		{name: "exp-wrong-arity", spec: "exp:1s", wantErr: true},
		{name: "exp-bad-min", spec: "exp:bogus:30s", wantErr: true},
		{name: "exp-bad-max", spec: "exp:1s:bogus", wantErr: true},
		{name: "exp-zero-min", spec: "exp:0s:30s", wantErr: true},
		{name: "exp-min-gt-max", spec: "exp:30s:1s", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseBackoff(tc.spec)
			if tc.wantErr {
				require.ErrorIs(t, err, errInvalidBackoff)
				require.Nil(t, got)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)

			switch tc.wantKind {
			case "fixed":
				fb, ok := got.(usbip.FixedBackoff)
				require.True(t, ok)
				require.Equal(t, tc.wantD, fb.Delay)
			case "exp":
				_, ok := got.(*usbip.ExponentialBackoff)
				require.True(t, ok)
			}
		})
	}
}
