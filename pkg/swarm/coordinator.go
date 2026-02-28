// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"fmt"
)

// CoordinatorAdapter adapts LeaderElection to the Coordinator interface.
type CoordinatorAdapter struct {
	le *LeaderElection
}

// NewCoordinatorAdapter creates a coordinator adapter from a BucketSet.
func NewCoordinatorAdapter(
	buckets *BucketSet,
	nodeID string,
	config LeaderElectionConfig,
) (*CoordinatorAdapter, error) {
	if buckets == nil {
		return nil, fmt.Errorf("buckets cannot be nil")
	}

	le, err := NewLeaderElection(nodeID, buckets.Leader, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create leader election: %w", err)
	}

	return &CoordinatorAdapter{le: le}, nil
}

// Start starts the coordinator.
func (ca *CoordinatorAdapter) Start() error {
	return ca.le.Start()
}

// Stop stops the coordinator.
func (ca *CoordinatorAdapter) Stop() {
	ca.le.Stop()
}

// IsLeader returns true if this node is the current leader.
func (ca *CoordinatorAdapter) IsLeader() bool {
	return ca.le.IsLeader()
}

// LeaderID returns the current leader's node ID.
func (ca *CoordinatorAdapter) LeaderID() string {
	return ca.le.GetLeader()
}

// LeaderChanges returns a channel that receives leader ID changes.
func (ca *CoordinatorAdapter) LeaderChanges() <-chan string {
	return ca.le.LeaderChanges()
}

// GetLeaderElection returns the underlying LeaderElection for advanced usage.
func (ca *CoordinatorAdapter) GetLeaderElection() *LeaderElection {
	return ca.le
}
