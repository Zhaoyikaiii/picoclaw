// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) icoClaw contributors

package swarm

import (
	"context"
	"fmt"
	"sync"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// SwarmManager manages the swarm cluster by coordinating discovery,
// leadership, routing, load monitoring, and handoff.
//
// This is the main entry point for swarm mode. It composes the various
// swarm subsystems and provides a unified API.
type SwarmManager struct {
	config *Config

	// Core components
	discovery   NodeDiscovery
	rawDS       *DiscoveryService // Keep reference to access BucketSet
	coordinator Coordinator
	router      Router
	loadMonitor LoadReporter
	handoff     *HandoffCoordinator

	// Local node info
	localNode *NodeInfo

	// Current view (cached for fast access)
	currentView View
	viewMu      sync.RWMutex

	// Lifecycle
	running bool
	mu      sync.RWMutex
	stopCh  chan struct{}

	// Callbacks
	onViewChange []func(View)
}

// NewSwarmManager creates a new swarm manager.
func NewSwarmManager(cfg *Config) (*SwarmManager, error) {
	if !cfg.Enabled {
		return nil, ErrDiscoveryDisabled
	}

	sm := &SwarmManager{
		config:       cfg,
		currentView:  View{LocalNodeID: cfg.NodeID},
		stopCh:       make(chan struct{}),
		onViewChange: make([]func(View), 0),
	}

	// Create NATS discovery service
	rawDS, err := NewDiscoveryService(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery service: %w", err)
	}
	sm.rawDS = rawDS

	// Wrap in adapter to implement NodeDiscovery interface
	ds := NewDiscoveryAdapter(rawDS)
	sm.discovery = ds
	sm.localNode = rawDS.LocalNode()
	sm.currentView.LocalNodeID = sm.localNode.ID

	// Create router
	sm.router = NewDefaultRouter(cfg.Handoff)

	// Create load monitor
	lm := NewLoadMonitor(&cfg.LoadMonitor)
	sm.loadMonitor = lm

	// Create handoff coordinator
	sm.handoff = NewHandoffCoordinator(rawDS, cfg.Handoff)
	if err := sm.handoff.SetCryptoConfig(cfg.Handoff.Crypto); err != nil {
		logger.WarnCF("swarm", "Handoff crypto configuration warning", map[string]any{"error": err})
	}

	return sm, nil
}

// Start starts the swarm manager and all its subsystems.
func (sm *SwarmManager) Start() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.running {
		return nil
	}

	// Start discovery (this also creates buckets)
	if err := sm.discovery.Start(); err != nil {
		return fmt.Errorf("failed to start discovery: %w", err)
	}

	// Create leader election coordinator (if enabled) - must be after discovery starts
	if sm.config.LeaderElection.Enabled {
		buckets := sm.rawDS.Buckets()
		if buckets != nil {
			coordinator, err := NewCoordinatorAdapter(buckets, sm.config.NodeID, sm.config.LeaderElection)
			if err != nil {
				sm.discovery.Stop()
				return fmt.Errorf("failed to create coordinator: %w", err)
			}
			sm.coordinator = coordinator

			// Start coordinator
			if err := sm.coordinator.Start(); err != nil {
				sm.discovery.Stop()
				return fmt.Errorf("failed to start coordinator: %w", err)
			}
		}
	}

	// Start load monitor
	if lm, ok := sm.loadMonitor.(*LoadMonitor); ok {
		lm.Start()
	}

	// Start handoff coordinator
	if sm.handoff != nil {
		if err := sm.handoff.Start(); err != nil {
			logger.WarnCF("swarm", "Failed to start handoff coordinator", map[string]any{"error": err})
		}
	}

	// Watch for node changes
	cancelWatch := sm.discovery.WatchNodes(sm.onViewUpdate)
	go func() {
		<-sm.stopCh
		cancelWatch()
	}()

	sm.running = true
	logger.InfoCF("swarm", "Swarm manager started", map[string]any{
		"node_id": sm.localNode.ID,
	})

	return nil
}

// Stop stops the swarm manager and all its subsystems.
func (sm *SwarmManager) Stop() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if !sm.running {
		return nil
	}

	close(sm.stopCh)

	// Stop handoff
	if sm.handoff != nil {
		_ = sm.handoff.Close()
	}

	// Stop load monitor
	if lm, ok := sm.loadMonitor.(*LoadMonitor); ok {
		lm.Stop()
	}

	// Stop coordinator
	if sm.coordinator != nil {
		sm.coordinator.Stop()
	}

	// Stop discovery
	_ = sm.discovery.Stop()

	sm.running = false
	logger.InfoCF("swarm", "Swarm manager stopped", nil)

	return nil
}

// onViewUpdate is called when the cluster view changes.
func (sm *SwarmManager) onViewUpdate(view View) {
	sm.viewMu.Lock()
	sm.currentView = view
	sm.viewMu.Unlock()

	// Notify callbacks
	for _, cb := range sm.onViewChange {
		go func(f func(View)) {
			defer func() {
				if r := recover(); r != nil {
					logger.ErrorCF("swarm", "View change callback panic", map[string]any{"panic": r})
				}
			}()
			f(view)
		}(cb)
	}

	logger.DebugCF("swarm", "Cluster view updated", map[string]any{
		"node_count":  len(view.Nodes),
		"alive_count": len(view.AliveNodes()),
	})
}

// GetView returns the current cluster view.
func (sm *SwarmManager) GetView() View {
	sm.viewMu.RLock()
	defer sm.viewMu.RUnlock()
	return sm.currentView
}

// AliveNodes returns the current list of alive nodes.
func (sm *SwarmManager) AliveNodes() []*NodeInfo {
	return sm.discovery.AliveNodes()
}

// LocalNode returns the local node info.
func (sm *SwarmManager) LocalNode() *NodeInfo {
	return sm.localNode
}

// IsLeader returns true if this node is the leader.
func (sm *SwarmManager) IsLeader() bool {
	if sm.coordinator == nil {
		return false
	}
	return sm.coordinator.IsLeader()
}

// LeaderID returns the current leader ID.
func (sm *SwarmManager) LeaderID() string {
	if sm.coordinator == nil {
		return ""
	}
	return sm.coordinator.LeaderID()
}

// PickNode selects a node for the given task.
func (sm *SwarmManager) PickNode(task Task) RoutingDecision {
	view := sm.GetView()
	return sm.router.PickNode(task, view)
}

// CanHandleLocally checks if the local node can handle the given task.
func (sm *SwarmManager) CanHandleLocally(task Task) bool {
	return sm.router.CanHandleLocally(task, sm.loadMonitor.GetLoadScore())
}

// GetLoad returns the current load score.
func (sm *SwarmManager) GetLoad() float64 {
	return sm.loadMonitor.GetLoadScore()
}

// ShouldOffload returns true if the load is high enough to offload tasks.
func (sm *SwarmManager) ShouldOffload() bool {
	return sm.loadMonitor.ShouldOffload()
}

// UpdateLoad updates the local node's load score.
func (sm *SwarmManager) UpdateLoad(score float64) {
	if updater, ok := sm.discovery.(NodeInfoUpdater); ok {
		updater.UpdateLoad(score)
	}
}

// UpdateCapabilities updates the local node's agent capabilities.
func (sm *SwarmManager) UpdateCapabilities(caps map[string]string) {
	if updater, ok := sm.discovery.(NodeInfoUpdater); ok {
		updater.UpdateCapabilities(caps)
	}
}

// Handoff initiates a handoff to another node.
func (sm *SwarmManager) Handoff(ctx context.Context, req *HandoffRequest) (*HandoffResponse, error) {
	if sm.handoff == nil {
		return nil, fmt.Errorf("handoff not enabled")
	}
	return sm.handoff.InitiateHandoff(ctx, req)
}

// OnViewChange registers a callback for cluster view changes.
func (sm *SwarmManager) OnViewChange(callback func(View)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onViewChange = append(sm.onViewChange, callback)
}

// OnLeaderChange registers a callback for leader changes.
func (sm *SwarmManager) OnLeaderChange(callback func(leaderID string)) {
	if sm.coordinator == nil {
		return
	}

	ch := sm.coordinator.LeaderChanges()
	go func() {
		for leaderID := range ch {
			callback(leaderID)
		}
	}()
}

// OnLoadThreshold registers a callback when load threshold is exceeded.
func (sm *SwarmManager) OnLoadThreshold(callback func(float64)) {
	if lm, ok := sm.loadMonitor.(*LoadMonitor); ok {
		lm.OnThreshold(callback)
	}
}

// GetDiscovery returns the underlying discovery service (for compatibility).
func (sm *SwarmManager) GetDiscovery() NodeDiscovery {
	return sm.discovery
}

// GetHandoffCoordinator returns the handoff coordinator.
func (sm *SwarmManager) GetHandoffCoordinator() *HandoffCoordinator {
	return sm.handoff
}

// GetLoadMonitor returns the load monitor.
func (sm *SwarmManager) GetLoadMonitor() *LoadMonitor {
	if lm, ok := sm.loadMonitor.(*LoadMonitor); ok {
		return lm
	}
	return nil
}

// GetBuckets returns the bucket set.
func (sm *SwarmManager) GetBuckets() *BucketSet {
	return sm.rawDS.Buckets()
}
