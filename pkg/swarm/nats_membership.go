// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"encoding/json"
	"time"

	"github.com/sipeed/picoclaw/pkg/kv"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// heartbeatLoop periodically publishes heartbeat and checks for stale nodes.
func (ds *DiscoveryService) heartbeatLoop() {
	interval := ds.config.Discovery.HeartbeatInterval.Duration
	if interval <= 0 {
		interval = DefaultHeartbeatInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial publish
	ds.publishNode()

	// Start stale node checker
	go ds.staleNodeChecker(interval)

	for {
		select {
		case <-ds.stopChan:
			return
		case <-ticker.C:
			ds.publishNode()
		}
	}
}

// publishNode publishes the local node state to NATS KV.
func (ds *DiscoveryService) publishNode() {
	if ds.buckets == nil {
		return
	}

	ds.mu.Lock()
	now := time.Now().UnixNano()
	ds.localNode.LastHeartbeat = now
	ds.localNode.Timestamp = now
	if ds.localNode.FirstSeen == 0 {
		ds.localNode.FirstSeen = now
	}
	ds.localNode.Status = string(NodeStatusAlive)
	node := *ds.localNode // Copy to avoid holding lock
	ds.mu.Unlock()

	data, err := json.Marshal(node)
	if err != nil {
		logger.ErrorCF("swarm", "failed to marshal node info", map[string]any{"error": err})
		return
	}

	if err := ds.buckets.Members.Put(node.ID, data); err != nil {
		logger.ErrorCF("swarm", "failed to publish node info", map[string]any{"error": err})
	}
}

// staleNodeChecker actively checks for stale nodes by reading from KV.
func (ds *DiscoveryService) staleNodeChecker(interval time.Duration) {
	checkInterval := interval
	if checkInterval < 5*time.Second {
		checkInterval = 5 * time.Second
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ds.stopChan:
			return
		case <-ticker.C:
			ds.checkStaleNodes()
			ds.checkStaleStatusNodes()
		}
	}
}

// checkStaleNodes reads all nodes from KV and removes stale ones.
func (ds *DiscoveryService) checkStaleNodes() {
	if ds.buckets == nil {
		return
	}

	keys, err := ds.buckets.Members.Keys()
	if err != nil {
		return
	}

	now := time.Now()
	memberTTL := ds.config.Discovery.MemberTTL.Duration
	if memberTTL <= 0 {
		memberTTL = DefaultMemberTTL
	}
	timeout := memberTTL * 3 // Use 3x TTL as safety margin

	for _, nodeID := range keys {
		entry, err := ds.buckets.Members.Get(nodeID)
		if err != nil {
			continue
		}
		if entry == nil {
			if nws, ok := ds.membership.GetNode(nodeID); ok {
				ds.membership.RemoveNode(nodeID)
				ds.detailedStatus.Remove(nodeID)
				logger.InfoCF("swarm", "Node removed (stale entry)", map[string]any{"node_id": nodeID})
				ds.eventHandler.Dispatch(&NodeEvent{
					Node:  nws.Node,
					Event: EventLeave,
					Time:  now.UnixNano(),
				})
			}
			continue
		}

		var node NodeInfo
		if err := json.Unmarshal(entry.Value, &node); err != nil {
			continue
		}

		// Skip local node
		if node.ID == ds.localNode.ID {
			continue
		}

		lastSeen := node.LastHeartbeat
		if lastSeen == 0 {
			lastSeen = node.Timestamp
		}

		if lastSeen > 0 {
			age := now.Sub(time.Unix(0, lastSeen))
			if age > timeout {
				if nws, ok := ds.membership.GetNode(node.ID); ok {
					ds.membership.RemoveNode(node.ID)
					ds.detailedStatus.Remove(node.ID)
					logger.InfoCF("swarm", "Node marked stale (no heartbeat)", map[string]any{
						"node_id": node.ID,
						"age":     age.Seconds(),
					})
					ds.eventHandler.Dispatch(&NodeEvent{
						Node:  nws.Node,
						Event: EventLeave,
						Time:  now.UnixNano(),
					})
				}
			}
		}
	}
}

// checkStaleStatusNodes reads all status entries from KV and removes stale ones.
func (ds *DiscoveryService) checkStaleStatusNodes() {
	if ds.buckets == nil {
		return
	}

	keys, err := ds.buckets.Status.Keys()
	if err != nil {
		return
	}

	now := time.Now()
	timeout := DefaultDetailedStatusTTL * 2

	for _, nodeID := range keys {
		entry, err := ds.buckets.Status.Get(nodeID)
		if err != nil {
			continue
		}
		if entry == nil {
			ds.detailedStatus.Remove(nodeID)
			continue
		}

		var status NodeDetailedInfo
		if err := json.Unmarshal(entry.Value, &status); err != nil {
			continue
		}

		if status.Timestamp > 0 {
			age := now.Sub(time.Unix(0, status.Timestamp))
			if age > timeout {
				ds.detailedStatus.Remove(nodeID)
				logger.DebugCF("swarm", "Status entry removed (stale)", map[string]any{
					"node_id": nodeID,
					"age":     age.Seconds(),
				})
			}
		}
	}
}

// watchNodes watches for changes in the membership bucket.
func (ds *DiscoveryService) watchNodes() {
	watcher, err := ds.buckets.Members.WatchAll()
	if err != nil {
		logger.ErrorCF("swarm", "failed to watch members bucket", map[string]any{"error": err})
		return
	}
	defer watcher.Stop()

	for {
		select {
		case <-ds.stopChan:
			return
		case entry := <-watcher.Updates():
			if entry == nil {
				continue
			}

			// Bucket isolation: swarm_members bucket only stores node keys
			// Key format is nodeID directly (no prefix needed since bucket is isolated)
			nodeID := entry.Key

			// Skip our own entry
			if nodeID == ds.localNode.ID {
				continue
			}

			if entry.Operation == kv.EntryOperationDelete {
				if nws, ok := ds.membership.GetNode(nodeID); ok {
					ds.membership.RemoveNode(nodeID)
					logger.InfoCF("swarm", "Node left (member deleted)", map[string]any{"node_id": nodeID})
					ds.eventHandler.Dispatch(&NodeEvent{
						Node:  nws.Node,
						Event: EventLeave,
						Time:  time.Now().UnixNano(),
					})
				}
				continue
			}

			var node NodeInfo
			if err := json.Unmarshal(entry.Value, &node); err != nil {
				continue
			}

			ds.membership.UpdateNode(&node)
		}
	}
}
