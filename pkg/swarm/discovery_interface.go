// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import "github.com/nats-io/nats.go"

// Discovery is the interface for node discovery implementations.
// In the current architecture, NATS is the sole transport.
//
//nolint:interfacebloat // Single concrete implementation; splitting would add indirection without benefit.
type Discovery interface {
	// Start starts the discovery service.
	Start() error

	// Stop stops the discovery service.
	Stop() error

	// LocalNode returns the local node info.
	LocalNode() *NodeInfo

	// UpdateLocalInfo updates the local node's information.
	UpdateLocalInfo(info *NodeInfo)

	// UpdateLoad updates the local node's load score.
	UpdateLoad(score float64)

	// UpdateCapabilities updates the local node's agent capabilities.
	UpdateCapabilities(caps map[string]string)

	// Members returns all known members.
	Members() []*NodeWithState

	// GetNode returns a node by ID.
	GetNode(nodeID string) (*NodeWithState, bool)

	// Subscribe registers a handler for node events.
	Subscribe(handler EventHandler) EventHandlerID

	// Unsubscribe removes a node event handler.
	Unsubscribe(id EventHandlerID)

	// PublishLocalUpdate publishes local node state.
	PublishLocalUpdate()

	// DispatchEvent dispatches a node event to registered handlers.
	DispatchEvent(event *NodeEvent)

	// GetMembershipManager returns the membership manager.
	GetMembershipManager() *MembershipManager

	// NATSConn returns the underlying NATS connection.
	// Used by handoff, leader election, and messaging subsystems.
	NATSConn() *nats.Conn

	// JetStream returns the JetStream context.
	// Used for KV operations (membership, leader election).
	JetStream() nats.JetStreamContext

	// Buckets returns the KV bucket set.
	// Used for leader election and other distributed state operations.
	Buckets() *BucketSet
}

// DetailedStatusProvider is an optional capability interface for discovery
// implementations that maintain a local cache of detailed node status.
// Tools should use this interface via type assertion instead of depending
// on a concrete discovery type.
type DetailedStatusProvider interface {
	GetDetailedStatus() *NodeDetailedStatus
}
