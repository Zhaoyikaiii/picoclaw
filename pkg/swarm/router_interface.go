// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

// Task represents a task that needs to be routed to a node.
type Task struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	RequiredCaps []string          `json:"required_caps,omitempty"` // Required agent capabilities
	Priority     int               `json:"priority,omitempty"`
	Metadata     map[string]any    `json:"metadata,omitempty"`
	Context      map[string]string `json:"context,omitempty"`
}

// RoutingDecision is the result of routing a task.
type RoutingDecision struct {
	NodeID      string `json:"node_id"`                 // Selected node ID
	Reason      string `json:"reason,omitempty"`        // Why this node was selected
	Reject      bool   `json:"reject,omitempty"`        // true if request should be rejected (no nodes available)
	LoadTooHigh bool   `json:"load_too_high,omitempty"` // true if local load is too high to accept
}

// Router provides task routing decisions based on cluster view.
//
// The router selects the best node for a task based on load,
// capabilities, and other routing policies.
type Router interface {
	// PickNode selects a node for the given task.
	// Returns a RoutingDecision with the selected node and reason.
	PickNode(task Task, view View) RoutingDecision

	// CanHandleLocally checks if the local node can handle the given task.
	// Returns false if the local node is overloaded or missing capabilities.
	CanHandleLocally(task Task, localLoad float64) bool
}
