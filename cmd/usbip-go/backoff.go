// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// errInvalidBackoff is the sentinel base for malformed --backoff specs.
var errInvalidBackoff = errors.New("invalid backoff spec")

// parseBackoff translates a non-empty spec string into a concrete
// BackoffStrategy. Accepted grammars:
//   - "exp:min:max"       → ExponentialBackoff{min, max, 0.2 jitter}
//   - "fixed:delay"       → FixedBackoff{delay}
//
// An empty spec is a caller-level concern — the attach command checks
// spec=="" directly and passes nil to AttachOptions.Backoff. Splitting
// that branch out of parseBackoff keeps this function's return shape
// consistent (never returns both nil values).
func parseBackoff(spec string) (usbip.BackoffStrategy, error) {
	kind, rest, ok := strings.Cut(spec, ":")
	if !ok {
		return nil, fmt.Errorf("%w: missing separator in %q", errInvalidBackoff, spec)
	}

	switch kind {
	case "fixed":
		return parseFixedBackoff(rest)
	case "exp":
		return parseExpBackoff(rest)
	default:
		return nil, fmt.Errorf("%w: unknown kind %q", errInvalidBackoff, kind)
	}
}

// parseFixedBackoff handles "fixed:<duration>".
func parseFixedBackoff(rest string) (usbip.BackoffStrategy, error) {
	d, err := time.ParseDuration(rest)
	if err != nil {
		return nil, fmt.Errorf("%w: bad duration %q: %w", errInvalidBackoff, rest, err)
	}

	if d < 0 {
		return nil, fmt.Errorf("%w: negative delay %s", errInvalidBackoff, d)
	}

	return usbip.FixedBackoff{Delay: d}, nil
}

// expBackoffParts is the required field count for "exp:<min>:<max>".
const expBackoffParts = 2

// parseExpBackoff handles "exp:<min>:<max>". Jitter is fixed at the
// spec default of 0.2; exposing it on the CLI is deferred until a
// genuine user need surfaces.
func parseExpBackoff(rest string) (usbip.BackoffStrategy, error) {
	parts := strings.Split(rest, ":")
	if len(parts) != expBackoffParts {
		return nil, fmt.Errorf("%w: exp requires min:max, got %q", errInvalidBackoff, rest)
	}

	minD, err := time.ParseDuration(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: bad min %q: %w", errInvalidBackoff, parts[0], err)
	}

	maxD, err := time.ParseDuration(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: bad max %q: %w", errInvalidBackoff, parts[1], err)
	}

	if minD <= 0 || maxD <= 0 || maxD < minD {
		return nil, fmt.Errorf("%w: exp min/max invalid (min=%s max=%s)", errInvalidBackoff, minD, maxD)
	}

	return usbip.MustNewExponentialBackoff(usbip.ExponentialBackoffConfig{
		Min:    minD,
		Max:    maxD,
		Jitter: defaultBackoffJitter,
	}), nil
}

// defaultBackoffJitter matches the library-default jitter fraction
// documented on usbip.AttachOptions.Backoff.
const defaultBackoffJitter = 0.2
