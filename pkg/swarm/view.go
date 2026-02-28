// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import "time"

// View represents a snapshot of the cluster state.
// It is an immutable snapshot that can be safely passed between components.
type View struct {
	Nodes       []*NodeInfo `json:"nodes"`
	LocalNodeID string      `json:"local_node_id"`
	Version     int64       `json:"version"`   // View version for change detection
	Timestamp   int64       `json:"timestamp"` // When this view was captured (Unix nano)
}

// NodeInfoInContext extends NodeInfo with runtime state from the view.
type NodeInfoInContext struct {
	Node      *NodeInfo
	IsAlive   bool
	IsLocal   bool
	LastSeen  time.Time
	LoadScore float64
}

// Get returns node info by ID from the view.
func (v *View) Get(nodeID string) *NodeInfoInContext {
	for _, n := range v.Nodes {
		if n.ID == nodeID {
			return &NodeInfoInContext{
				Node:      n,
				IsAlive:   n.Status == "alive" || n.Status == "",
				IsLocal:   n.ID == v.LocalNodeID,
				LastSeen:  n.GetLastSeen(),
				LoadScore: n.LoadScore,
			}
		}
	}
	return nil
}

// AliveNodes returns all alive nodes excluding the local node.
func (v *View) AliveNodes() []*NodeInfo {
	result := make([]*NodeInfo, 0)
	for _, n := range v.Nodes {
		if n.ID != v.LocalNodeID && (n.Status == "alive" || n.Status == "") {
			result = append(result, n)
		}
	}
	return result
}

// AvailableNodes returns all alive nodes with available capacity.
func (v *View) AvailableNodes(loadThreshold float64) []*NodeInfo {
	result := make([]*NodeInfo, 0)
	for _, n := range v.Nodes {
		if n.ID != v.LocalNodeID && (n.Status == "alive" || n.Status == "") {
			if n.LoadScore < loadThreshold {
				result = append(result, n)
			}
		}
	}
	return result
}

// NodeCount returns the total number of nodes in the view.
func (v *View) NodeCount() int {
	return len(v.Nodes)
}

// AliveCount returns the number of alive nodes in the view.
func (v *View) AliveCount() int {
	count := 0
	for _, n := range v.Nodes {
		if n.Status == "alive" || n.Status == "" {
			count++
		}
	}
	return count
}

// HasNode returns true if the view contains the given node ID.
func (v *View) HasNode(nodeID string) bool {
	for _, n := range v.Nodes {
		if n.ID == nodeID {
			return true
		}
	}
	return false
}

// NodeChange represents a change in the cluster view.
type NodeChange struct {
	Node    *NodeInfo
	Join    bool // true if node joined, false if left/updated
	Updated bool // true if node was updated (status changed, etc.)
}
