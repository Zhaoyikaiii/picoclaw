// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import "errors"

var (
	// ErrNodeNotFound is returned when a node is not found in the cluster.
	ErrNodeNotFound = errors.New("node not found")

	// ErrNodeNotAvailable is returned when a node is not available for handoff.
	ErrNodeNotAvailable = errors.New("node not available")

	// ErrNoHealthyNodes is returned when no healthy nodes are available.
	ErrNoHealthyNodes = errors.New("no healthy nodes available")

	// ErrHandoffTimeout is returned when a handoff operation times out.
	ErrHandoffTimeout = errors.New("handoff timeout")

	// ErrHandoffRejected is returned when a handoff is rejected by the target node.
	ErrHandoffRejected = errors.New("handoff rejected")

	// ErrHandoffInProgress is returned when a handoff is already in progress.
	ErrHandoffInProgress = errors.New("handoff already in progress")

	// ErrHandoffNoResponders is returned when no nodes responded to a handoff request.
	ErrHandoffNoResponders = errors.New("no nodes responded to handoff request")

	// ErrInvalidNodeInfo is returned when node information is invalid.
	ErrInvalidNodeInfo = errors.New("invalid node information")

	// ErrDiscoveryDisabled is returned when discovery is disabled.
	ErrDiscoveryDisabled = errors.New("discovery disabled")

	// ErrSessionNotFound is returned when a session is not found.
	ErrSessionNotFound = errors.New("session not found")

	// ErrCapabilityNotSupported is returned when a required capability is not supported.
	ErrCapabilityNotSupported = errors.New("capability not supported")

	// ErrNATSNotConnected is returned when the NATS connection is not established.
	ErrNATSNotConnected = errors.New("NATS not connected")

	// ErrLeaderLockFailed is returned when leader lock acquisition fails.
	ErrLeaderLockFailed = errors.New("leader lock acquisition failed")

	// ErrLeaderLockLost is returned when the leader lock is lost (CAS renewal failed).
	ErrLeaderLockLost = errors.New("leader lock lost")

	// ErrKVBucketUnavailable is returned when a required KV bucket cannot be accessed.
	ErrKVBucketUnavailable = errors.New("KV bucket unavailable")
)

// IsBusinessRejection returns true if the error represents a legitimate business rejection
// (e.g., no nodes available, node overloaded) rather than a system failure.
// Business rejections should be communicated via HandoffResponse.Accepted=false.
// System failures should be returned as Go errors for proper handling.
func IsBusinessRejection(err error) bool {
	if err == nil {
		return false
	}
	return err == ErrNoHealthyNodes ||
		err == ErrNodeNotAvailable ||
		err == ErrHandoffRejected ||
		err == ErrHandoffInProgress ||
		err == ErrCapabilityNotSupported
}

// IsSystemError returns true if the error represents a system failure
// (e.g., NATS unavailable, timeout, serialization failure).
// System errors should propagate up the call stack as Go errors.
func IsSystemError(err error) bool {
	if err == nil {
		return false
	}
	return !IsBusinessRejection(err)
}
