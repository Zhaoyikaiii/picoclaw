// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

// Coordinator provides leader election functionality.
//
// The coordinator ensures exactly one node in the cluster acts as leader
// for operations that require cluster-wide coordination.
type Coordinator interface {
	// Start starts the coordinator.
	Start() error

	// Stop stops the coordinator.
	Stop()

	// IsLeader returns true if this node is the current leader.
	IsLeader() bool

	// LeaderID returns the current leader's node ID.
	// Returns empty string if no leader is elected.
	LeaderID() string

	// LeaderChanges returns a channel that receives leader ID changes.
	// The channel is closed when Stop() is called.
	LeaderChanges() <-chan string
}
