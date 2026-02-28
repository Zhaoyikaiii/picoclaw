// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"encoding/json"
	"runtime"
	"time"

	"github.com/sipeed/picoclaw/pkg/kv"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// detailedStatusLoop periodically publishes detailed node status.
func (ds *DiscoveryService) detailedStatusLoop() {
	interval := DefaultDetailedStatusInterval
	hbInterval := ds.config.Discovery.HeartbeatInterval.Duration
	if hbInterval > 0 {
		interval = hbInterval * 2
		if interval < DefaultDetailedStatusInterval {
			interval = DefaultDetailedStatusInterval
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial publish
	ds.publishDetailedStatus()

	for {
		select {
		case <-ds.stopChan:
			return
		case <-ticker.C:
			ds.publishDetailedStatus()
		}
	}
}

// publishDetailedStatus publishes the detailed node status to NATS KV.
func (ds *DiscoveryService) publishDetailedStatus() {
	if ds.buckets == nil {
		return
	}

	var loadMetrics *LoadMetrics
	if ds.loadMonitor != nil {
		loadMetrics = ds.loadMonitor.GetCurrentLoad()
	} else {
		loadMetrics = &LoadMetrics{
			CPUUsage:       0,
			MemoryUsage:    0,
			MemoryBytes:    0,
			Goroutines:     runtime.NumGoroutine(),
			ActiveSessions: 0,
			Score:          0,
		}
	}

	status := BuildDetailedStatus(
		ds.localNode.ID,
		ds.localNode.Addr,
		ds.localNode.Port,
		0, // No HTTP port; all communication via NATS
		ds.localNode.LoadScore,
		loadMetrics,
		ds.getTodoList(),
		ds.getActiveTasks(),
		ds.startTime,
		ds.getLastError(),
		ds.version,
	)

	// Update local registry
	ds.detailedStatus.Update(status)

	// Publish to NATS KV
	data, err := json.Marshal(status)
	if err != nil {
		logger.ErrorCF("swarm", "failed to marshal detailed status", map[string]any{"error": err})
		return
	}

	if err := ds.buckets.Status.Put(status.ID, data); err != nil {
		logger.ErrorCF("swarm", "failed to publish detailed status", map[string]any{"error": err})
	}
}

// watchDetailedStatus watches for detailed status updates from other nodes.
func (ds *DiscoveryService) watchDetailedStatus() {
	if ds.buckets == nil {
		return
	}

	watcher, err := ds.buckets.Status.WatchAll()
	if err != nil {
		logger.ErrorCF("swarm", "failed to watch status bucket", map[string]any{"error": err})
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

			// Bucket isolation: swarm_status bucket only stores status keys
			// Key format is nodeID directly (no prefix needed since bucket is isolated)
			nodeID := entry.Key

			// Skip our own entry
			if nodeID == ds.localNode.ID {
				continue
			}

			if entry.Operation == kv.EntryOperationDelete {
				ds.detailedStatus.Remove(nodeID)
				continue
			}

			var status NodeDetailedInfo
			if err := json.Unmarshal(entry.Value, &status); err != nil {
				continue
			}

			ds.detailedStatus.Update(&status)
		}
	}
}

// SetLoadMonitor sets the load monitor for detailed status reporting.
func (ds *DiscoveryService) SetLoadMonitor(lm *LoadMonitor) {
	ds.loadMonitor = lm
}

// SetVersion sets the version string for detailed status.
func (ds *DiscoveryService) SetVersion(version string) {
	ds.mu.Lock()
	ds.version = version
	ds.mu.Unlock()
}

// GetDetailedStatus returns the detailed status registry.
func (ds *DiscoveryService) GetDetailedStatus() *NodeDetailedStatus {
	return ds.detailedStatus
}
