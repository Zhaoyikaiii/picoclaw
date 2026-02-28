// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

// NodeDiscovery provides node discovery and membership tracking.
//
// This is the minimal interface for discovering other nodes in the swarm.
// Implementations can use NATS KV, etcd, Consul, or any other coordination service.
type NodeDiscovery interface {
	// Start starts the discovery service.
	Start() error

	// Stop stops the discovery service.
	Stop() error

	// AliveNodes returns the current list of alive nodes.
	// This is a point-in-time snapshot.
	AliveNodes() []*NodeInfo

	// WatchNodes registers a callback for cluster view changes.
	// The callback receives the updated view and should return quickly.
	// Returns a function that can be called to cancel the watch.
	WatchNodes(callback func(View)) func()
}

// NodeInfoUpdater allows updating local node information.
// This is an optional interface that NodeDiscovery implementations may implement.
type NodeInfoUpdater interface {
	// UpdateLocalInfo updates the local node's information.
	UpdateLocalInfo(info *NodeInfo)

	// UpdateLoad updates the local node's load score.
	UpdateLoad(score float64)

	// UpdateCapabilities updates the local node's agent capabilities.
	UpdateCapabilities(caps map[string]string)
}
