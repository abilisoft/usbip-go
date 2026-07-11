// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/spf13/cobra"
)

// Flag defaults are authoritative in the operations-observability and
// json-contracts OpenSpec documents. Centralised here so the table in --help
// matches the documented defaults without drift.
const (
	defaultListen             = "0.0.0.0:3240"
	defaultStatusSocket       = "/run/usbip-go/status.sock"
	defaultStatusSocketGroup  = "usbip-go"
	defaultMaxSessions        = 128
	defaultMaxSessionsPerPeer = 8
	defaultAcceptRateLimit    = 10.0
	defaultMaxHandshakeBytes  = 65536
	defaultHandshakeTimeout   = domain.DefaultExporterHandshakeTimeout
	defaultShutdownTimeout    = domain.DefaultShutdownTimeout
	defaultDrainTimeout       = 60 * time.Second
)

// ServeConfig is the parsed command-line configuration for the
// `usbip-go serve` subcommand. Every field is populated by cobra
// during flag parsing; zero values should not be observed after
// PersistentPreRunE runs.
type ServeConfig struct {
	// Listen is the TCP bind address. Ignored when systemd passes a
	// named socket via LISTEN_FDNAMES=usbip-go.
	Listen string
	// StatusSocket is the UDS path for the health/status endpoint.
	// Empty string disables the endpoint entirely.
	StatusSocket string
	// StatusSocketGroup is the group to chown the status UDS to after
	// bind; the socket mode is always 0660.
	StatusSocketGroup string
	// HealthAddr is the optional HTTP listener that serves /healthz
	// and /readyz; empty string disables the endpoint.
	HealthAddr string
	// AllowCIDR is the accept-path ACL. Empty slice means permit all.
	AllowCIDR []string
	// MaxSessions caps total concurrent sessions.
	MaxSessions int
	// MaxSessionsPerPeer caps concurrent sessions per source IP.
	MaxSessionsPerPeer int
	// AcceptRateLimit is the token-bucket rate cap (accepts / second).
	AcceptRateLimit float64
	// MaxHandshakeBytes caps bytes read during the handshake phase.
	MaxHandshakeBytes int
	// HandshakeTimeout bounds the full handshake completion time.
	HandshakeTimeout time.Duration
	// ShutdownTimeout is the graceful-shutdown budget before force-close.
	ShutdownTimeout time.Duration
}

// bindServeFlags registers every `usbip-go serve` flag on cmd and
// wires the parsed values into cfg. Defaults come from the operations-observability
// and json-contracts OpenSpec documents; any change there must land here first.
func bindServeFlags(cmd *cobra.Command, cfg *ServeConfig) {
	flags := cmd.PersistentFlags()

	flags.StringVar(&cfg.Listen, "listen", defaultListen,
		"TCP bind address; ignored when LISTEN_FDS names 'usbip-go'")
	flags.StringVar(&cfg.StatusSocket, "status-socket", defaultStatusSocket,
		"UDS path for health/status; empty disables")
	flags.StringVar(&cfg.StatusSocketGroup, "status-socket-group", defaultStatusSocketGroup,
		"group ownership applied via chown after UDS bind (mode 0660)")
	flags.StringVar(&cfg.HealthAddr, "health-addr", "",
		"HTTP listener for /healthz + /readyz (e.g. 127.0.0.1:9240); empty disables")
	flags.StringSliceVar(&cfg.AllowCIDR, "allow-cidr", nil,
		"accept-path ACL; repeatable CIDR entries. Empty permits all")
	flags.IntVar(&cfg.MaxSessions, "max-sessions", defaultMaxSessions,
		"maximum concurrent sessions")
	flags.IntVar(&cfg.MaxSessionsPerPeer, "max-sessions-per-peer", defaultMaxSessionsPerPeer,
		"per-source-IP session cap")
	flags.Float64Var(&cfg.AcceptRateLimit, "accept-rate-limit", defaultAcceptRateLimit,
		"token-bucket accept rate (requests/second)")
	flags.IntVar(&cfg.MaxHandshakeBytes, "max-handshake-bytes", defaultMaxHandshakeBytes,
		"hard cap per OP request/response")
	flags.DurationVar(&cfg.HandshakeTimeout, "handshake-timeout", defaultHandshakeTimeout,
		"deadline for full handshake completion")
	flags.DurationVar(&cfg.ShutdownTimeout, "shutdown-timeout", defaultShutdownTimeout,
		"graceful shutdown budget before force-close")
	// Logging flags (--log-level, --log-format, --verbose) live on the
	// root command as persistent flags. The serve subcommand reads the
	// pre-built logger via loggerFromCtx; redefining the flags here
	// would shadow the root's parse + handler choice.
}
