// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"encoding/json"
	"time"
)

// Config contains all configuration for swarm mode.
type Config struct {
	// Enabled enables swarm mode.
	Enabled bool `json:"enabled" env:"PICOCLAW_SWARM_ENABLED"`

	// NodeID is the unique identifier for this node.
	// If empty, a hostname-based ID will be generated.
	NodeID string `json:"node_id,omitempty" env:"PICOCLAW_SWARM_NODE_ID"`

	// Discovery configuration for node discovery via NATS.
	Discovery DiscoveryConfig `json:"discovery"`

	// Handoff configuration for task handoff.
	Handoff HandoffConfig `json:"handoff"`

	// LoadMonitor configuration for load monitoring.
	LoadMonitor LoadMonitorConfig `json:"load_monitor"`

	// LeaderElection configuration for leader election.
	LeaderElection LeaderElectionConfig `json:"leader_election"`

	// Metrics configuration for observability.
	Metrics MetricsConfig `json:"metrics"`
}

// DiscoveryConfig contains configuration for NATS-based node discovery.
type DiscoveryConfig struct {
	// NATSURL is the NATS server URL (required).
	NATSURL string `json:"nats_url,omitempty" env:"PICOCLAW_SWARM_NATS_URL"`

	// NATSCredsFile is the path to NATS credentials file (NKeys/JWT auth).
	NATSCredsFile string `json:"nats_creds_file,omitempty" env:"PICOCLAW_SWARM_NATS_CREDS_FILE"`

	// NATSTLSCert is the path to the client TLS certificate for mTLS.
	NATSTLSCert string `json:"nats_tls_cert,omitempty"`

	// NATSTLSKey is the path to the client TLS key for mTLS.
	NATSTLSKey string `json:"nats_tls_key,omitempty"`

	// NATSTLSCACert is the path to the CA certificate for TLS verification.
	NATSTLSCACert string `json:"nats_tls_ca_cert,omitempty"`

	// HeartbeatInterval controls how often the node renews its KV entry.
	HeartbeatInterval Duration `json:"heartbeat_interval,omitempty"`

	// MemberTTL is the TTL for member entries in the KV bucket.
	MemberTTL Duration `json:"member_ttl,omitempty"`

	// SubjectPrefix overrides the default "picoclaw.swarm" subject prefix.
	SubjectPrefix string `json:"subject_prefix,omitempty"`

	// NodeTimeout is the timeout before marking a node as suspect.
	NodeTimeout Duration `json:"node_timeout,omitempty"`

	// DeadNodeTimeout is the timeout before marking a node as dead.
	DeadNodeTimeout Duration `json:"dead_node_timeout,omitempty"`
}

// HandoffConfig contains configuration for task handoff.
type HandoffConfig struct {
	// Enabled enables task handoff.
	Enabled bool `json:"enabled"`

	// LoadThreshold is the load score threshold (0-1) above which
	// tasks will be handed off to other nodes.
	LoadThreshold float64 `json:"load_threshold,omitempty"`

	// Timeout is the timeout for a handoff operation.
	Timeout Duration `json:"timeout,omitempty"`

	// MaxRetries is the maximum number of retries for handoff.
	MaxRetries int `json:"max_retries,omitempty"`

	// RetryDelay is the delay between retries.
	RetryDelay Duration `json:"retry_delay,omitempty"`

	// RequestTimeout is the timeout for a single NATS request-reply handoff.
	RequestTimeout Duration `json:"request_timeout,omitempty"`

	// MaxSessionHistory is the maximum number of recent session messages to include in handoff.
	// Set to 0 to send all messages (not recommended for production). Default: 10
	MaxSessionHistory int `json:"max_session_history,omitempty"`

	// Crypto contains security configuration for handoff.
	Crypto CryptoConfig `json:"crypto"`
}

// LoadMonitorConfig contains configuration for load monitoring.
type LoadMonitorConfig struct {
	// Enabled enables load monitoring.
	Enabled bool `json:"enabled"`

	// Interval is the interval between load samples.
	Interval Duration `json:"interval,omitempty"`

	// SampleSize is the number of samples to keep for averaging.
	SampleSize int `json:"sample_size,omitempty"`

	// CPUWeight is the weight for CPU usage in load score (0-1).
	CPUWeight float64 `json:"cpu_weight,omitempty"`

	// MemoryWeight is the weight for memory usage in load score (0-1).
	MemoryWeight float64 `json:"memory_weight,omitempty"`

	// SessionWeight is the weight for active sessions in load score (0-1).
	SessionWeight float64 `json:"session_weight,omitempty"`

	// OffloadThreshold is the load score threshold above which tasks should be offloaded (0-1).
	OffloadThreshold float64 `json:"offload_threshold,omitempty"`

	// RoutingRejectThreshold is the load score above which routing requests are rejected (0-1).
	// This provides hysteresis to prevent oscillation when load is near threshold.
	RoutingRejectThreshold float64 `json:"routing_reject_threshold,omitempty"`

	// RoutingAcceptThreshold is the load score below which routing requests are accepted (0-1).
	// Provides hysteresis - once rejected, load must drop below this to accept again.
	RoutingAcceptThreshold float64 `json:"routing_accept_threshold,omitempty"`

	// MaxMemoryBytes is the maximum memory to use for normalization (default: 1GB).
	MaxMemoryBytes uint64 `json:"max_memory_bytes,omitempty"`

	// MaxGoroutines is the maximum goroutine count for normalization (default: 1000).
	MaxGoroutines int `json:"max_goroutines,omitempty"`

	// MaxSessions is the maximum session count for normalization (default: 100).
	MaxSessions int `json:"max_sessions,omitempty"`
}

// LeaderElectionConfig contains configuration for leader election.
type LeaderElectionConfig struct {
	// Enabled enables leader election.
	Enabled bool `json:"enabled"`

	// LockTTL is how long the leader lock lives without renewal.
	LockTTL Duration `json:"lock_ttl,omitempty"`

	// RenewalInterval is how often the leader renews its lock.
	// Must be less than LockTTL.
	RenewalInterval Duration `json:"renewal_interval,omitempty"`
}

// CryptoConfig contains cryptographic settings for secure swarm communication.
type CryptoConfig struct {
	// SharedSecret is the HMAC secret for message signing (base64 encoded or raw string).
	// If empty, signing is disabled (NOT RECOMMENDED for production).
	SharedSecret string `json:"shared_secret,omitempty" env:"PICOCLAW_SWARM_SHARED_SECRET"`

	// EncryptionKey is the AES-256 key for encrypting session data (base64 encoded or raw string).
	// Must be 32 bytes when decoded. If empty, encryption is disabled.
	EncryptionKey string `json:"encryption_key,omitempty" env:"PICOCLAW_SWARM_ENCRYPTION_KEY"`

	// RequireAuth enables strict authentication - reject unsigned messages.
	RequireAuth bool `json:"require_auth"`

	// RequireEncryption enables strict encryption - reject unencrypted session data.
	RequireEncryption bool `json:"require_encryption"`
}

// MetricsConfig contains configuration for metrics collection.
type MetricsConfig struct {
	// Enabled enables metrics collection.
	Enabled bool `json:"enabled"`

	// ExportInterval is how often to export metrics.
	ExportInterval Duration `json:"export_interval,omitempty"`

	// PrometheusEnabled enables Prometheus format export.
	PrometheusEnabled bool `json:"prometheus_enabled"`

	// PrometheusEndpoint is the HTTP endpoint for Prometheus metrics.
	PrometheusEndpoint string `json:"prometheus_endpoint,omitempty"`
}

// Duration is a wrapper around time.Duration for JSON parsing.
type Duration struct {
	time.Duration
}

// UnmarshalJSON parses a duration from JSON.
// Supports:
//   - String: Go duration format, e.g. "5s", "100ms", "2m30s"
//   - Number: interpreted as seconds (e.g. 5 means 5s, 0.5 means 500ms)
func (d *Duration) UnmarshalJSON(b []byte) error {
	// Check if it's a string (quoted)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		var err error
		d.Duration, err = time.ParseDuration(s)
		return err
	}

	// Otherwise it's a number — interpret as seconds for human-friendly config
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	d.Duration = time.Duration(v * float64(time.Second))
	return nil
}

// MarshalJSON converts a duration to JSON.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

// DefaultConfig returns the default swarm configuration.
func DefaultConfig() *Config {
	return &Config{
		Enabled: false,
		NodeID:  "",
		Discovery: DiscoveryConfig{
			NATSURL:           DefaultNATSURL,
			HeartbeatInterval: Duration{DefaultHeartbeatInterval},
			MemberTTL:         Duration{DefaultMemberTTL},
			NodeTimeout:       Duration{DefaultNodeTimeout},
			DeadNodeTimeout:   Duration{DefaultDeadNodeTimeout},
		},
		Handoff: HandoffConfig{
			Enabled:           true,
			LoadThreshold:     DefaultLoadThreshold,
			Timeout:           Duration{DefaultHandoffTimeout},
			MaxRetries:        DefaultMaxHandoffRetries,
			RetryDelay:        Duration{DefaultHandoffRetryDelay},
			RequestTimeout:    Duration{DefaultHandoffRequestTimeout},
			MaxSessionHistory: DefaultMaxSessionHistory,
		},
		LoadMonitor: LoadMonitorConfig{
			Enabled:                true,
			Interval:               Duration{DefaultLoadSampleInterval},
			SampleSize:             DefaultLoadSampleSize,
			CPUWeight:              DefaultCPUWeight,
			MemoryWeight:           DefaultMemoryWeight,
			SessionWeight:          DefaultSessionWeight,
			OffloadThreshold:       DefaultOffloadThreshold,
			RoutingRejectThreshold: DefaultRoutingRejectThreshold,
			RoutingAcceptThreshold: DefaultRoutingAcceptThreshold,
			MaxMemoryBytes:         DefaultMaxMemoryBytes,
			MaxGoroutines:          DefaultMaxGoroutines,
			MaxSessions:            DefaultMaxSessions,
		},
		LeaderElection: LeaderElectionConfig{
			Enabled:         false,
			LockTTL:         Duration{DefaultLeaderLockTTL},
			RenewalInterval: Duration{DefaultLeaderRenewalInterval},
		},
		Metrics: MetricsConfig{
			Enabled:            false,
			ExportInterval:     Duration{10 * time.Second},
			PrometheusEnabled:  false,
			PrometheusEndpoint: "/metrics",
		},
	}
}
