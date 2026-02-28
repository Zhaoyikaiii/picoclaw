// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"encoding/json"
	"runtime"
	"sync"
	"time"
)

// NodeDetailedInfo represents detailed status information about a node.
// This is periodically written to NATS KV for fast status queries without
// needing to send requests to other nodes.
type NodeDetailedInfo struct {
	// Basic node info
	ID        string  `json:"id"`
	Addr      string  `json:"addr"`
	Port      int     `json:"port"`
	HTTPPort  int     `json:"http_port"`
	LoadScore float64 `json:"load_score"`
	Timestamp int64   `json:"timestamp"`

	// Load metrics
	CPUUsage       float64 `json:"cpu_usage"`
	MemoryUsage    float64 `json:"memory_usage"`
	MemoryBytes    uint64  `json:"memory_bytes"`
	Goroutines     int     `json:"goroutines"`
	ActiveSessions int     `json:"active_sessions"`

	// Task information
	TodoList    []string `json:"todo_list,omitempty"`
	ActiveTasks int      `json:"active_tasks"`

	// System information
	DiskUsagePercent float64 `json:"disk_usage_percent,omitempty"`
	DiskUsedBytes    uint64  `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes   uint64  `json:"disk_total_bytes,omitempty"`

	// Status
	LastError string `json:"last_error,omitempty"`
	Uptime    int64  `json:"uptime"`     // Uptime in seconds
	Status    string `json:"status"`     // "alive", "suspect", "dead"
	Version   string `json:"version"`    // PicoClaw version
	GoVersion string `json:"go_version"` // Go runtime version
	OS        string `json:"os"`         // Operating system
	Arch      string `json:"arch"`       // Architecture
}

// NodeDetailedStatus is a registry for node detailed status from NATS KV.
// It caches the latest status from all nodes for fast queries.
type NodeDetailedStatus struct {
	statusMap map[string]*NodeDetailedInfo // node_id -> status
	mu        sync.RWMutex
}

// NewNodeDetailedStatus creates a new node detailed status registry.
func NewNodeDetailedStatus() *NodeDetailedStatus {
	return &NodeDetailedStatus{
		statusMap: make(map[string]*NodeDetailedInfo),
	}
}

// Update updates the status for a node.
func (nds *NodeDetailedStatus) Update(status *NodeDetailedInfo) {
	nds.mu.Lock()
	defer nds.mu.Unlock()
	nds.statusMap[status.ID] = status
}

// Get returns the status for a node.
func (nds *NodeDetailedStatus) Get(nodeID string) (*NodeDetailedInfo, bool) {
	nds.mu.RLock()
	defer nds.mu.RUnlock()
	s, ok := nds.statusMap[nodeID]
	return s, ok
}

// GetAll returns all node statuses.
func (nds *NodeDetailedStatus) GetAll() []*NodeDetailedInfo {
	nds.mu.RLock()
	defer nds.mu.RUnlock()

	result := make([]*NodeDetailedInfo, 0, len(nds.statusMap))
	for _, s := range nds.statusMap {
		result = append(result, s)
	}
	return result
}

// Remove removes a node from the registry.
func (nds *NodeDetailedStatus) Remove(nodeID string) {
	nds.mu.Lock()
	defer nds.mu.Unlock()
	delete(nds.statusMap, nodeID)
}

// CleanExpired removes statuses older than the given TTL.
func (nds *NodeDetailedStatus) CleanExpired(ttl time.Duration) {
	nds.mu.Lock()
	defer nds.mu.Unlock()

	cutoff := time.Now().Add(-ttl).UnixNano()
	for id, s := range nds.statusMap {
		if s.Timestamp < cutoff {
			delete(nds.statusMap, id)
		}
	}
}

// AsMap returns the status map for external access.
func (nds *NodeDetailedStatus) AsMap() map[string]*NodeDetailedInfo {
	nds.mu.RLock()
	defer nds.mu.RUnlock()

	result := make(map[string]*NodeDetailedInfo, len(nds.statusMap))
	for k, v := range nds.statusMap {
		result[k] = v
	}
	return result
}

// MarshalJSON implements json.Marshaler.
func (nds *NodeDetailedStatus) MarshalJSON() ([]byte, error) {
	nds.mu.RLock()
	defer nds.mu.RUnlock()
	return json.Marshal(nds.statusMap)
}

// BuildDetailedStatus builds a NodeDetailedInfo from the current node state.
func BuildDetailedStatus(
	nodeID, addr string,
	port, httpPort int,
	loadScore float64,
	loadMetrics *LoadMetrics,
	todoList []string,
	activeTasks int,
	startTime time.Time,
	lastError string,
	version string,
) *NodeDetailedInfo {
	uptime := time.Since(startTime)

	status := &NodeDetailedInfo{
		ID:        nodeID,
		Addr:      addr,
		Port:      port,
		HTTPPort:  httpPort,
		LoadScore: loadScore,
		Timestamp: time.Now().UnixNano(),

		CPUUsage:       loadMetrics.CPUUsage,
		MemoryUsage:    loadMetrics.MemoryUsage,
		MemoryBytes:    loadMetrics.MemoryBytes,
		Goroutines:     loadMetrics.Goroutines,
		ActiveSessions: loadMetrics.ActiveSessions,

		TodoList:    todoList,
		ActiveTasks: activeTasks,

		LastError: lastError,
		Uptime:    int64(uptime.Seconds()),
		Status:    string(NodeStatusAlive),
		Version:   version,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}

	return status
}

// GetMemoryBytes returns the current memory allocation in bytes.
func (lm *LoadMetrics) GetMemoryBytes() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Alloc
}
