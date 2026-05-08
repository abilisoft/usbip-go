package main

import (
	"time"

	"github.com/spf13/cobra"
)

// Flag defaults — authoritative from spec §7.7. Centralised here so the
// table in --help matches the documented defaults without drift.
const (
	defaultListen             = "0.0.0.0:3240"
	defaultStatusSocket       = "/run/usbip-go/status.sock"
	defaultStatusSocketGroup  = "usbip"
	defaultMaxSessions        = 128
	defaultMaxSessionsPerPeer = 8
	defaultAcceptRateLimit    = 10.0
	defaultMaxHandshakeBytes  = 65536
	defaultHandshakeTimeout   = 10 * time.Second
	defaultShutdownTimeout    = 30 * time.Second
	defaultDrainTimeout       = 60 * time.Second
	defaultLogLevel           = "info"
	defaultLogFormat          = "auto"
)

// Config is the parsed command-line configuration for usbipd. Every
// field is populated by cobra during flag parsing; zero values should
// not be observed after PersistentPreRunE runs.
type Config struct {
	// Listen is the TCP bind address. Ignored when systemd passes a
	// named socket via LISTEN_FDNAMES=usbipd.
	Listen string
	// StatusSocket is the UDS path for the health/status endpoint.
	// Empty string disables the endpoint entirely.
	StatusSocket string
	// StatusSocketGroup is the group to chown the status UDS to after
	// bind; the socket mode is always 0660.
	StatusSocketGroup string
	// MetricsAddr is the optional Prometheus metrics HTTP listener;
	// empty string disables the endpoint.
	MetricsAddr string
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
	// LogLevel selects the slog threshold (error/warn/info/debug/trace).
	LogLevel string
	// LogFormat selects the handler (auto/pretty/json).
	LogFormat string
	// VerboseCount counts -v occurrences: 1=debug, 2=trace.
	VerboseCount int
}

// bindFlags registers every usbipd root-command flag on cmd and wires
// the parsed values into cfg. Defaults come from the spec §7.7 table;
// any change there must land here first.
func bindFlags(cmd *cobra.Command, cfg *Config) {
	flags := cmd.PersistentFlags()

	flags.StringVar(&cfg.Listen, "listen", defaultListen,
		"TCP bind address; ignored when LISTEN_FDS names 'usbipd'")
	flags.StringVar(&cfg.StatusSocket, "status-socket", defaultStatusSocket,
		"UDS path for health/status; empty disables")
	flags.StringVar(&cfg.StatusSocketGroup, "status-socket-group", defaultStatusSocketGroup,
		"group ownership applied via chown after UDS bind (mode 0660)")
	flags.StringVar(&cfg.MetricsAddr, "metrics-addr", "",
		"Prometheus metrics HTTP listener (e.g. 127.0.0.1:9240); empty disables")
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
	flags.StringVar(&cfg.LogLevel, "log-level", defaultLogLevel,
		"log level: error/warn/info/debug/trace")
	flags.StringVar(&cfg.LogFormat, "log-format", defaultLogFormat,
		"log handler: auto/pretty/json")
	flags.CountVarP(&cfg.VerboseCount, "verbose", "v",
		"verbose counter: -v=debug, -vv=trace")
}
