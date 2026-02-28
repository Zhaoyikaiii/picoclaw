// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"math/rand"
	"sync"
	"time"
)

// MembershipManager manages cluster membership.
type MembershipManager struct {
	discovery Discovery
	view      *ClusterView
	config    DiscoveryConfig
	mu        sync.RWMutex

	// Tracks nodes with pending dead-node removal goroutines
	pendingRemoval map[string]struct{}

	// Event callbacks
	onJoin   []func(*NodeInfo)
	onLeave  []func(*NodeInfo)
	onUpdate []func(*NodeInfo)
}

// NewMembershipManager creates a new membership manager.
func NewMembershipManager(ds Discovery, config DiscoveryConfig) *MembershipManager {
	localNodeID := ds.LocalNode().ID
	return &MembershipManager{
		discovery:      ds,
		view:           NewClusterView(localNodeID),
		config:         config,
		pendingRemoval: make(map[string]struct{}),
		onJoin:         make([]func(*NodeInfo), 0),
		onLeave:        make([]func(*NodeInfo), 0),
		onUpdate:       make([]func(*NodeInfo), 0),
	}
}

// GetNode retrieves a node by ID.
func (mm *MembershipManager) GetNode(nodeID string) (*NodeWithState, bool) {
	return mm.view.Get(nodeID)
}

// GetMembers returns all members.
func (mm *MembershipManager) GetMembers() []*NodeWithState {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.view.List()
}

// GetAliveMembers returns all alive members.
func (mm *MembershipManager) GetAliveMembers() []*NodeWithState {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	members := mm.view.GetAliveNodes()
	result := make([]*NodeWithState, 0, len(members))
	for _, m := range members {
		if m.Node.ID != mm.discovery.LocalNode().ID {
			result = append(result, m)
		}
	}
	return result
}

// GetAvailableMembers returns all available members (alive and not overloaded).
func (mm *MembershipManager) GetAvailableMembers() []*NodeWithState {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	members := mm.view.GetAvailableNodes()
	result := make([]*NodeWithState, 0, len(members))
	for _, m := range members {
		if m.Node.ID != mm.discovery.LocalNode().ID {
			result = append(result, m)
		}
	}
	return result
}

// UpdateNode updates or adds a node to the membership.
func (mm *MembershipManager) UpdateNode(node *NodeInfo) *NodeWithState {
	mm.mu.Lock()

	existing, existed := mm.view.Get(node.ID)
	nws := mm.view.AddOrUpdate(node)

	// Determine what event to fire and collect callbacks while under lock
	var eventType EventType
	var callbacks []func(*NodeInfo)

	if !existed {
		// New node joined
		nws.State.Status = NodeStatusAlive
		nws.State.StatusSince = time.Now().UnixNano()
		nws.State.LastSeen = time.Now().UnixNano()

		eventType = EventJoin
		callbacks = make([]func(*NodeInfo), len(mm.onJoin))
		copy(callbacks, mm.onJoin)
	} else if existing.Node.Timestamp < node.Timestamp {
		// Existing node updated
		nws.State.LastSeen = time.Now().UnixNano()

		// Mark as alive if was suspect/dead
		if nws.State.Status != NodeStatusAlive {
			nws.State.UpdateStatus(NodeStatusAlive)
			nws.State.PingFailure = 0
			nws.State.PingSuccess++
		}

		eventType = EventUpdate
		callbacks = make([]func(*NodeInfo), len(mm.onUpdate))
		copy(callbacks, mm.onUpdate)
	}

	// Copy node info for safe use outside lock
	nodeCopy := *node
	mm.mu.Unlock()

	// Fire callbacks and dispatch event outside the lock
	if eventType != "" {
		for _, cb := range callbacks {
			go cb(&nodeCopy)
		}
		mm.discovery.DispatchEvent(&NodeEvent{
			Node:  &nodeCopy,
			Event: eventType,
			Time:  time.Now().UnixNano(),
		})
	}

	return nws
}

// RemoveNode removes a node from the membership.
func (mm *MembershipManager) RemoveNode(nodeID string) {
	mm.mu.Lock()

	nws, exists := mm.view.Get(nodeID)
	if !exists {
		mm.mu.Unlock()
		return
	}

	// Copy node info before releasing lock
	nodeCopy := *nws.Node
	callbacks := make([]func(*NodeInfo), len(mm.onLeave))
	copy(callbacks, mm.onLeave)

	mm.view.Remove(nodeID)
	mm.mu.Unlock()

	// Notify callbacks and dispatch event outside the lock
	for _, cb := range callbacks {
		go cb(&nodeCopy)
	}

	mm.discovery.DispatchEvent(&NodeEvent{
		Node:  &nodeCopy,
		Event: EventLeave,
		Time:  time.Now().UnixNano(),
	})
}

// RecordHeartbeat records a heartbeat for a node.
func (mm *MembershipManager) RecordHeartbeat(nodeID string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	nws, exists := mm.view.Get(nodeID)
	if !exists {
		return
	}

	nws.State.LastPing = time.Now().UnixNano()
	nws.State.LastSeen = time.Now().UnixNano()

	// Reset failure count and increment success
	nws.State.PingFailure = 0
	nws.State.PingSuccess++

	// Mark as alive if was suspect
	if nws.State.Status != NodeStatusAlive {
		nws.State.UpdateStatus(NodeStatusAlive)
	}
}

// MarkSuspect marks a node as suspect (possibly dead).
func (mm *MembershipManager) MarkSuspect(nodeID string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	nws, exists := mm.view.Get(nodeID)
	if !exists {
		return
	}

	if nws.State.Status == NodeStatusAlive {
		nws.State.UpdateStatus(NodeStatusSuspect)
		nws.State.PingFailure++
	}
}

// MarkDead marks a node as dead.
func (mm *MembershipManager) MarkDead(nodeID string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	nws, exists := mm.view.Get(nodeID)
	if !exists {
		return
	}

	if nws.State.Status != NodeStatusDead {
		nws.State.UpdateStatus(NodeStatusDead)

		// Only spawn a removal goroutine if one isn't already pending
		if _, pending := mm.pendingRemoval[nodeID]; !pending {
			mm.pendingRemoval[nodeID] = struct{}{}
			go func() {
				time.Sleep(mm.config.DeadNodeTimeout.Duration)
				mm.mu.Lock()
				delete(mm.pendingRemoval, nodeID)
				mm.mu.Unlock()
				mm.RemoveNode(nodeID)
			}()
		}
	}
}

// CheckHealth checks the health of all members and marks dead nodes.
func (mm *MembershipManager) CheckHealth() {
	mm.mu.RLock()
	members := mm.view.List()
	nodeTimeout := mm.config.NodeTimeout.Duration
	deadTimeout := mm.config.DeadNodeTimeout.Duration
	localNodeID := mm.discovery.LocalNode().ID
	mm.mu.RUnlock()

	now := time.Now()

	for _, m := range members {
		// Skip local node
		if m.Node.ID == localNodeID {
			continue
		}

		lastSeen := time.Unix(0, m.State.LastSeen)
		age := now.Sub(lastSeen)

		switch m.State.Status {
		case NodeStatusAlive:
			if age > nodeTimeout {
				mm.MarkSuspect(m.Node.ID)
			}
		case NodeStatusSuspect:
			if age > deadTimeout {
				mm.MarkDead(m.Node.ID)
			}
		}
	}
}

// SelectByCapability selects members that have the required capabilities.
func (mm *MembershipManager) SelectByCapability(requiredCaps []string) []*NodeWithState {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	localNodeID := mm.discovery.LocalNode().ID
	members := mm.view.GetAvailableNodes()
	result := make([]*NodeWithState, 0)

	for _, m := range members {
		if m.Node.ID == localNodeID {
			continue
		}

		// If no capabilities required, include all available nodes
		if len(requiredCaps) == 0 {
			result = append(result, m)
			continue
		}

		// Check if node has all required capabilities
		hasAll := true
		for _, cap := range requiredCaps {
			found := false
			for _, nodeCap := range m.Node.AgentCaps {
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
			result = append(result, m)
		}
	}

	return result
}

// SelectLeastLoaded selects the member with the lowest load score.
func (mm *MembershipManager) SelectLeastLoaded() *NodeWithState {
	members := mm.GetAvailableMembers()
	if len(members) == 0 {
		return nil
	}

	least := members[0]
	for _, m := range members[1:] {
		if m.Node.LoadScore < least.Node.LoadScore {
			least = m
		}
	}

	return least
}

// SelectRandom selects a random available member.
func (mm *MembershipManager) SelectRandom() *NodeWithState {
	members := mm.GetAvailableMembers()
	if len(members) == 0 {
		return nil
	}

	// Use crypto/rand for better random distribution
	idx := rand.Intn(len(members))
	return members[idx]
}

// GetClusterSize returns the current cluster size.
func (mm *MembershipManager) GetClusterSize() int {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.view.Size
}

// OnJoin registers a callback for node join events.
func (mm *MembershipManager) OnJoin(callback func(*NodeInfo)) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.onJoin = append(mm.onJoin, callback)
}

// OnLeave registers a callback for node leave events.
func (mm *MembershipManager) OnLeave(callback func(*NodeInfo)) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.onLeave = append(mm.onLeave, callback)
}

// OnUpdate registers a callback for node update events.
func (mm *MembershipManager) OnUpdate(callback func(*NodeInfo)) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.onUpdate = append(mm.onUpdate, callback)
}

// StartHealthCheck starts the health check routine.
// Note: When using NATS discovery, health checks are driven by KV TTL
// and the stale-node checker in DiscoveryService. This method is kept
// for API compatibility but can be called optionally for additional checks.
func (mm *MembershipManager) StartHealthCheck(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			mm.CheckHealth()
		}
	}()
}
