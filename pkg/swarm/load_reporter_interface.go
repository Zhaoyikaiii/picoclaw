// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

// LoadReporter provides load information for routing decisions.
type LoadReporter interface {
	// GetLoadScore returns the current load score (0-1).
	GetLoadScore() float64

	// ShouldOffload returns true if the load is high enough to offload tasks.
	ShouldOffload() bool
}
