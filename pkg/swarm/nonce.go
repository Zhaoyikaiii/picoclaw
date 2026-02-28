// PicoClaw - Ultra-lightweight personal AI agent
// Swarm nonce cache for replay attack prevention
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"sync"
	"time"
)

// NonceCache tracks seen nonces for replay attack prevention.
// Uses a simple ring buffer with TTL-based expiration.
type NonceCache struct {
	mu       sync.RWMutex
	entries  map[string]*nonceEntry
	ring     []*nonceEntry
	ringIdx  int
	ringSize int
	ttl      time.Duration
}

type nonceEntry struct {
	nonce     string
	timestamp time.Time
	nodeID    string
}

// NewNonceCache creates a new nonce cache.
func NewNonceCache(ttl time.Duration, ringSize int) *NonceCache {
	if ringSize <= 0 {
		ringSize = 1000 // Default ring size
	}
	return &NonceCache{
		entries:  make(map[string]*nonceEntry),
		ring:     make([]*nonceEntry, ringSize),
		ringSize: ringSize,
		ringIdx:  0,
		ttl:      ttl,
	}
}

// CheckAndAdd returns true if the nonce is new (adds it), false if already seen.
func (nc *NonceCache) CheckAndAdd(nonce, nodeID string) bool {
	if nonce == "" {
		return false // Empty nonce is invalid
	}

	key := nodeID + ":" + nonce

	// Fast path: check map
	nc.mu.RLock()
	if _, exists := nc.entries[key]; exists {
		nc.mu.RUnlock()
		return false
	}
	nc.mu.RUnlock()

	// Slow path: add to cache
	nc.mu.Lock()
	defer nc.mu.Unlock()

	// Double-check after acquiring write lock
	if _, exists := nc.entries[key]; exists {
		return false
	}

	// Clean up old entry at current ring position
	if oldEntry := nc.ring[nc.ringIdx]; oldEntry != nil {
		oldKey := oldEntry.nodeID + ":" + oldEntry.nonce
		delete(nc.entries, oldKey)
	}

	// Add new entry
	entry := &nonceEntry{
		nonce:     nonce,
		timestamp: time.Now(),
		nodeID:    nodeID,
	}
	nc.entries[key] = entry
	nc.ring[nc.ringIdx] = entry
	nc.ringIdx = (nc.ringIdx + 1) % nc.ringSize

	return true
}

// Cleanup removes expired entries older than TTL.
// Should be called periodically (e.g., every minute).
func (nc *NonceCache) Cleanup() {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	cutoff := time.Now().Add(-nc.ttl)
	for key, entry := range nc.entries {
		if entry.timestamp.Before(cutoff) {
			delete(nc.entries, key)
		}
	}
}

// Stats returns cache statistics.
func (nc *NonceCache) Stats() map[string]any {
	nc.mu.RLock()
	defer nc.mu.RUnlock()

	return map[string]any{
		"size":        len(nc.entries),
		"ring_size":   nc.ringSize,
		"ttl_seconds": nc.ttl.Seconds(),
	}
}
