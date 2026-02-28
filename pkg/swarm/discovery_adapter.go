// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"sync"
	"time"
)

// DiscoveryAdapter adapts the existing DiscoveryService to the NodeDiscovery interface.
type DiscoveryAdapter struct {
	ds         Discovery
	watchers   []func(View)
	watcherIDs []int // Track watcher IDs for removal
	nextID     int
	mu         sync.RWMutex
	cancel     chan struct{}
}

// NewDiscoveryAdapter creates a new discovery adapter.
func NewDiscoveryAdapter(ds Discovery) *DiscoveryAdapter {
	da := &DiscoveryAdapter{
		ds:         ds,
		watchers:   make([]func(View), 0),
		watcherIDs: make([]int, 0),
		nextID:     1,
		cancel:     make(chan struct{}),
	}

	// Subscribe to node events to update watchers
	ds.Subscribe(func(event *NodeEvent) {
		da.notifyWatchers()
	})

	return da
}

// Start starts the discovery service.
func (da *DiscoveryAdapter) Start() error {
	return da.ds.Start()
}

// Stop stops the discovery service.
func (da *DiscoveryAdapter) Stop() error {
	close(da.cancel)
	return da.ds.Stop()
}

// AliveNodes returns the current list of alive nodes.
func (da *DiscoveryAdapter) AliveNodes() []*NodeInfo {
	members := da.ds.Members()
	result := make([]*NodeInfo, 0)
	for _, m := range members {
		if m.State.Status == NodeStatusAlive && m.Node.ID != da.ds.LocalNode().ID {
			result = append(result, m.Node)
		}
	}
	return result
}

// WatchNodes registers a callback for cluster view changes.
func (da *DiscoveryAdapter) WatchNodes(callback func(View)) func() {
	da.mu.Lock()
	id := da.nextID
	da.nextID++
	da.watchers = append(da.watchers, callback)
	da.watcherIDs = append(da.watcherIDs, id)
	da.mu.Unlock()

	// Send initial view
	callback(da.buildView())

	// Return cancel function
	closed := false
	return func() {
		if closed {
			return
		}
		closed = true

		da.mu.Lock()
		defer da.mu.Unlock()

		// Find and remove the watcher
		for i, watcherID := range da.watcherIDs {
			if watcherID == id {
				// Remove by swapping with last element
				lastIdx := len(da.watchers) - 1
				da.watchers[i] = da.watchers[lastIdx]
				da.watcherIDs[i] = da.watcherIDs[lastIdx]
				da.watchers = da.watchers[:lastIdx]
				da.watcherIDs = da.watcherIDs[:lastIdx]
				break
			}
		}
	}
}

// notifyWatchers notifies all registered watchers of view changes.
func (da *DiscoveryAdapter) notifyWatchers() {
	view := da.buildView()

	da.mu.RLock()
	watchers := make([]func(View), len(da.watchers))
	copy(watchers, da.watchers)
	da.mu.RUnlock()

	for _, w := range watchers {
		go func(cb func(View)) {
			defer func() {
				if r := recover(); r != nil {
					// Log panic but don't crash
				}
			}()
			cb(view)
		}(w)
	}
}

// buildView builds a View from the current membership state.
func (da *DiscoveryAdapter) buildView() View {
	members := da.ds.Members()
	nodes := make([]*NodeInfo, 0, len(members))
	for _, m := range members {
		nodes = append(nodes, m.Node)
	}

	return View{
		Nodes:       nodes,
		LocalNodeID: da.ds.LocalNode().ID,
		Version:     time.Now().UnixNano(),
		Timestamp:   time.Now().UnixNano(),
	}
}

// GetDiscovery returns the underlying Discovery service.
func (da *DiscoveryAdapter) GetDiscovery() Discovery {
	return da.ds
}
