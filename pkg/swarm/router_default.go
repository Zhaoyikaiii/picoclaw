// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import "fmt"

// DefaultRouter implements the Router interface with load-based routing.
type DefaultRouter struct {
	config HandoffConfig
}

// NewDefaultRouter creates a new default router.
func NewDefaultRouter(config HandoffConfig) *DefaultRouter {
	return &DefaultRouter{config: config}
}

// PickNode selects a node for the given task.
func (r *DefaultRouter) PickNode(task Task, view View) RoutingDecision {
	// Filter available nodes based on capabilities
	var candidates []*NodeInfo
	if len(task.RequiredCaps) > 0 {
		// Need to filter by capabilities
		candidates = r.filterByCapability(view.AliveNodes(), task.RequiredCaps)
	} else {
		candidates = view.AliveNodes()
	}

	if len(candidates) == 0 {
		return RoutingDecision{
			Reject: true,
			Reason: "no available nodes with required capabilities",
		}
	}

	// Select least loaded node
	selected := candidates[0]
	for _, n := range candidates[1:] {
		if n.LoadScore < selected.LoadScore {
			selected = n
		}
	}

	return RoutingDecision{
		NodeID: selected.ID,
		Reason: fmt.Sprintf("least loaded node (load: %.2f)", selected.LoadScore),
	}
}

// CanHandleLocally checks if the local node can handle the given task.
func (r *DefaultRouter) CanHandleLocally(task Task, localLoad float64) bool {
	// Check load threshold
	if localLoad > r.config.LoadThreshold {
		return false
	}

	// For capability checking, we'd need to access local node caps
	// This is handled by the caller in most cases
	return true
}

// filterByCapability filters nodes that have all required capabilities.
func (r *DefaultRouter) filterByCapability(nodes []*NodeInfo, requiredCaps []string) []*NodeInfo {
	result := make([]*NodeInfo, 0)
	for _, n := range nodes {
		hasAll := true
		for _, cap := range requiredCaps {
			found := false
			for _, nodeCap := range n.AgentCaps {
				if nodeCap == cap {
					found = true
					break
				}
			}
			if !found {
				hasAll = false
				break
			}
		}
		if hasAll {
			result = append(result, n)
		}
	}
	return result
}
