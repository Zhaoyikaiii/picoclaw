// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import "time"

// LoadTrend represents the direction of load change over time.
type LoadTrend string

const (
	LoadTrendIncreasing LoadTrend = "increasing"
	LoadTrendDecreasing LoadTrend = "decreasing"
	LoadTrendStable     LoadTrend = "stable"
)

// NATS Subject namespace.
// All swarm communication uses these subject prefixes.
// ACL rule: only swarm node identities may publish/subscribe picoclaw.swarm.*
const (
	// SubjectPrefix is the root namespace for all swarm subjects.
	SubjectPrefix = "picoclaw.swarm"

	// SubjectHeartbeat is the prefix for heartbeat messages: picoclaw.swarm.heartbeat.<nodeID>
	SubjectHeartbeat = SubjectPrefix + ".heartbeat"

	// SubjectMetrics is the prefix for metrics messages: picoclaw.swarm.metrics.<nodeID>
	SubjectMetrics = SubjectPrefix + ".metrics"

	// SubjectNodeMsg is the prefix for direct node messages: picoclaw.swarm.node.<nodeID>.msg
	SubjectNodeMsg = SubjectPrefix + ".node"

	// SubjectHandoff is the prefix for targeted handoff: picoclaw.swarm.handoff.<targetNodeID>
	SubjectHandoff = SubjectPrefix + ".handoff"

	// SubjectLeader is the subject for leader election announcements.
	SubjectLeader = SubjectPrefix + ".leader"
)

// NATS JetStream KV Bucket names.
const (
	// KVBucketMembers stores node membership with TTL-based liveness.
	KVBucketMembers = "swarm_members"

	// KVBucketStatus stores detailed node status (CPU, memory, tasks, etc.).
	KVBucketStatus = "swarm_status"

	// KVBucketLeader stores the leader election CAS lock.
	KVBucketLeader = "swarm_leader"
)

// Default NATS connection settings.
const (
	// DefaultNATSURL is the default NATS server URL.
	DefaultNATSURL = "nats://localhost:4222"

	// DefaultNATSPrefix is the default prefix for NATS KV buckets.
	DefaultNATSPrefix = "picoclaw_swarm"
)

// Default intervals for NATS-based operations.
const (
	// DefaultHeartbeatInterval is how often a node renews its KV entry.
	DefaultHeartbeatInterval = 5 * time.Second

	// DefaultMemberTTL is the TTL for member entries in the KV bucket.
	DefaultMemberTTL = 15 * time.Second

	// DefaultDetailedStatusInterval is the interval for publishing detailed status.
	DefaultDetailedStatusInterval = 10 * time.Second

	// DefaultDetailedStatusTTL is the TTL for detailed status entries.
	DefaultDetailedStatusTTL = 30 * time.Second

	// DefaultLeaderLockTTL is how long the leader lock lives without renewal.
	DefaultLeaderLockTTL = 10 * time.Second

	// DefaultLeaderRenewalInterval is how often the leader renews its lock.
	// Must be less than DefaultLeaderLockTTL.
	DefaultLeaderRenewalInterval = 3 * time.Second

	// DefaultHandoffRequestTimeout is the timeout for a single NATS handoff request-reply.
	DefaultHandoffRequestTimeout = 10 * time.Second
)

// Default timeouts for node health.
const (
	// DefaultNodeTimeout is the timeout before marking a node as suspect.
	DefaultNodeTimeout = 5 * time.Second

	// DefaultDeadNodeTimeout is the timeout before removing a dead node.
	DefaultDeadNodeTimeout = 30 * time.Second
)

// Default handoff settings.
const (
	// DefaultHandoffTimeout is the default timeout for a handoff operation.
	DefaultHandoffTimeout = 30 * time.Second

	// DefaultHandoffRetryDelay is the default delay between handoff retries.
	DefaultHandoffRetryDelay = 5 * time.Second

	// DefaultMaxHandoffRetries is the default maximum number of handoff retries.
	DefaultMaxHandoffRetries = 3

	// DefaultMaxHandoffPayloadBytes is the max size of a handoff NATS message.
	// NATS default max_payload is 1MB; we leave headroom for envelope overhead.
	DefaultMaxHandoffPayloadBytes = 900 * 1024 // 900KB
)

// Default load monitor settings.
const (
	// DefaultLoadSampleInterval is the default interval between load samples.
	DefaultLoadSampleInterval = 5 * time.Second

	// DefaultLoadSampleSize is the default number of load samples to keep.
	DefaultLoadSampleSize = 60
)

// Load thresholds and limits.
const (
	// DefaultLoadThreshold is the default load score threshold for handoff.
	DefaultLoadThreshold = 0.8

	// DefaultAvailableLoadThreshold is the threshold below which a node is considered available (0-1).
	DefaultAvailableLoadThreshold = 0.9

	// DefaultOffloadThreshold is the default threshold above which tasks should be offloaded (0-1).
	DefaultOffloadThreshold = 0.8

	// DefaultRoutingRejectThreshold is the load threshold above which routing requests are rejected.
	// Provides hysteresis to prevent oscillation. Default: 0.9
	DefaultRoutingRejectThreshold = 0.9

	// DefaultRoutingAcceptThreshold is the load threshold below which routing requests are accepted.
	// Once rejected, load must drop below this to accept again. Default: 0.75
	DefaultRoutingAcceptThreshold = 0.75

	// DefaultMaxMemoryBytes is the default max memory for normalization (1GB).
	DefaultMaxMemoryBytes = 1024 * 1024 * 1024

	// DefaultMaxGoroutines is the default max goroutine count for normalization.
	DefaultMaxGoroutines = 1000

	// DefaultMaxSessions is the default max session count for normalization.
	DefaultMaxSessions = 100

	// DefaultMaxSessionHistory is the default number of recent messages to include in handoff.
	DefaultMaxSessionHistory = 10
)

// Load score weights.
const (
	// DefaultCPUWeight is the default weight for CPU in load score calculation.
	DefaultCPUWeight = 0.3

	// DefaultMemoryWeight is the default weight for memory in load score calculation.
	DefaultMemoryWeight = 0.3

	// DefaultSessionWeight is the default weight for sessions in load score calculation.
	DefaultSessionWeight = 0.4
)

// Node action constants for action-based node requests.
// These are lightweight operations that don't trigger LLM processing.
const (
	// NodeActionStatus requests node status information.
	NodeActionStatus = "status"

	// NodeActionPing checks if the node is responsive.
	NodeActionPing = "ping"

	// NodeActionHealth checks node health metrics.
	NodeActionHealth = "health"

	// NodeActionLeader queries current leader in leader election.
	NodeActionLeader = "leader"

	// NodeActionMetrics requests detailed metrics.
	NodeActionMetrics = "metrics"
)

// Default timeout for action-based requests (shorter than message processing).
const (
	// DefaultNodeActionTimeout is the timeout for lightweight node actions.
	DefaultNodeActionTimeout = 10 * time.Second
)

// Trend analysis thresholds.
const (
	// TrendIncreasingThreshold is the slope threshold for detecting increasing trend.
	TrendIncreasingThreshold = 0.01

	// TrendDecreasingThreshold is the slope threshold for detecting decreasing trend.
	TrendDecreasingThreshold = -0.01
)
