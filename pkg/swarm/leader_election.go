// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/kv"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// LeaderLockValue represents the value stored in the leader KV lock.
type LeaderLockValue struct {
	NodeID    string `json:"node_id"`
	Timestamp int64  `json:"timestamp"` // Unix nano when lock was acquired/renewed
	TTLMs     int64  `json:"ttl_ms"`    // TTL in milliseconds
}

// LeaderKey is the key used for leader election in the leader bucket.
const LeaderKey = "leader"

// LeaderElection handles leader election using Bucket with CAS (Compare-And-Swap).
//
// Flow:
//   - On Start(), attempt to acquire the leader lock via bucket.Create (succeeds if key absent).
//   - If key exists, check if TTL expired; if so, attempt CAS update to claim.
//   - Leader renews the lock periodically (RenewalInterval < LockTTL).
//   - Followers watch the key for changes; on delete/expire, they attempt acquisition.
//   - On graceful shutdown, the leader deletes the key to trigger re-election.
type LeaderElection struct {
	localNodeID string
	config      LeaderElectionConfig
	bucket      kv.Bucket

	mu             sync.RWMutex
	currentLeader  string
	isLeader       bool
	lastRevision   uint64 // For CAS operations
	leaderChangeCh chan string
	stopCh         chan struct{}
}

// NewLeaderElection creates a new leader election instance using the leader bucket.
func NewLeaderElection(nodeID string, bucket kv.Bucket, config LeaderElectionConfig) (*LeaderElection, error) {
	if bucket == nil {
		return nil, fmt.Errorf("bucket cannot be nil")
	}

	le := &LeaderElection{
		localNodeID:    nodeID,
		config:         config,
		bucket:         bucket,
		leaderChangeCh: make(chan string, 10),
		stopCh:         make(chan struct{}),
	}

	return le, nil
}

// Start starts the leader election process.
func (le *LeaderElection) Start() error {
	// Attempt initial acquisition
	le.tryAcquire()

	// Start renewal or watch based on current role
	go le.mainLoop()

	return nil
}

// Stop stops the leader election process.
func (le *LeaderElection) Stop() {
	le.mu.Lock()
	wasLeader := le.isLeader
	le.mu.Unlock()

	// If we are leader, delete the key to trigger re-election
	if wasLeader && le.bucket != nil {
		if err := le.bucket.Delete(LeaderKey); err != nil {
			logger.WarnCF("swarm", "Failed to delete leader key on shutdown", map[string]any{"error": err})
		}
	}

	close(le.stopCh)
}

// IsLeader returns true if this node is the current leader.
func (le *LeaderElection) IsLeader() bool {
	le.mu.RLock()
	defer le.mu.RUnlock()
	return le.isLeader
}

// GetLeader returns the current leader ID.
func (le *LeaderElection) GetLeader() string {
	le.mu.RLock()
	defer le.mu.RUnlock()
	return le.currentLeader
}

// LeaderChanges returns a channel that receives leader ID changes.
func (le *LeaderElection) LeaderChanges() <-chan string {
	return le.leaderChangeCh
}

// ElectLeader triggers a new leader election by deleting the current lock.
func (le *LeaderElection) ElectLeader(ctx context.Context) (string, error) {
	// Delete current lock to force re-election
	if le.bucket != nil {
		_ = le.bucket.Delete(LeaderKey)
	}

	// Wait for new leader
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			le.mu.RLock()
			leader := le.currentLeader
			le.mu.RUnlock()
			if leader != "" {
				return leader, nil
			}
		}
	}
}

// tryAcquire attempts to acquire the leader lock.
func (le *LeaderElection) tryAcquire() {
	lockTTL := le.config.LockTTL.Duration
	if lockTTL <= 0 {
		lockTTL = DefaultLeaderLockTTL
	}

	value := LeaderLockValue{
		NodeID:    le.localNodeID,
		Timestamp: time.Now().UnixNano(),
		TTLMs:     lockTTL.Milliseconds(),
	}
	data, err := json.Marshal(value)
	if err != nil {
		logger.ErrorCF("swarm", "Failed to marshal leader lock value", map[string]any{"error": err})
		return
	}

	// Try bucket.Create — succeeds only if key does not exist
	rev, err := le.bucket.Create(LeaderKey, data)
	if err == nil {
		// Successfully acquired lock
		le.mu.Lock()
		le.lastRevision = rev
		le.becomeLeader()
		le.mu.Unlock()
		return
	}

	// Key exists — check if current leader's lock is expired
	entry, err := le.bucket.Get(LeaderKey)
	if err != nil {
		// Key might have been deleted between Create and Get
		// Try create again
		rev, err = le.bucket.Create(LeaderKey, data)
		if err == nil {
			le.mu.Lock()
			le.lastRevision = rev
			le.becomeLeader()
			le.mu.Unlock()
		}
		return
	}

	var existing LeaderLockValue
	if err = json.Unmarshal(entry.Value, &existing); err != nil {
		return
	}

	// Check if existing lock is expired
	lockAge := time.Since(time.Unix(0, existing.Timestamp))
	existingTTL := time.Duration(existing.TTLMs) * time.Millisecond
	if lockAge > existingTTL {
		// Lock expired — attempt CAS update to claim it
		rev, err = le.bucket.Update(LeaderKey, data, entry.Revision)
		if err == nil {
			le.mu.Lock()
			le.lastRevision = rev
			le.becomeLeader()
			le.mu.Unlock()
			return
		}
		// CAS failed — someone else claimed it
	}

	// We are a follower — update current leader info
	le.mu.Lock()
	le.setFollower(existing.NodeID)
	le.mu.Unlock()
}

// mainLoop runs the renewal (if leader) or watch (if follower) loop.
func (le *LeaderElection) mainLoop() {
	for {
		select {
		case <-le.stopCh:
			return
		default:
		}

		le.mu.RLock()
		amLeader := le.isLeader
		le.mu.RUnlock()

		if amLeader {
			le.renewalLoop()
		} else {
			le.watchLoop()
		}

		// Backoff before re-entering loop after a role transition or error.
		// Without this, a persistent issue would cause a tight spin.
		select {
		case <-le.stopCh:
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// renewalLoop periodically renews the leader lock.
func (le *LeaderElection) renewalLoop() {
	interval := le.config.RenewalInterval.Duration
	if interval <= 0 {
		interval = DefaultLeaderRenewalInterval
	}

	lockTTL := le.config.LockTTL.Duration
	if lockTTL <= 0 {
		lockTTL = DefaultLeaderLockTTL
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-le.stopCh:
			return
		case <-ticker.C:
			value := LeaderLockValue{
				NodeID:    le.localNodeID,
				Timestamp: time.Now().UnixNano(),
				TTLMs:     lockTTL.Milliseconds(),
			}
			data, err := json.Marshal(value)
			if err != nil {
				continue
			}

			le.mu.Lock()
			rev, err := le.bucket.Update(LeaderKey, data, le.lastRevision)
			if err != nil {
				// CAS failed — lost leadership
				logger.WarnCF("swarm", "Leader lock renewal failed, stepping down", map[string]any{
					"node_id": le.localNodeID,
					"error":   err,
				})
				le.isLeader = false
				le.currentLeader = ""
				le.mu.Unlock()
				return // Exit renewal loop, mainLoop will start watchLoop
			}
			le.lastRevision = rev
			le.mu.Unlock()
		}
	}
}

// watchLoop watches the leader key for changes and attempts acquisition on delete/expire.
func (le *LeaderElection) watchLoop() {
	watcher, err := le.bucket.Watch(LeaderKey)
	if err != nil {
		logger.ErrorCF("swarm", "Failed to watch leader key", map[string]any{"error": err})
		// Retry after delay
		select {
		case <-le.stopCh:
			return
		case <-time.After(2 * time.Second):
			return // mainLoop will retry
		}
	}
	defer watcher.Stop()

	for {
		select {
		case <-le.stopCh:
			return
		case entry := <-watcher.Updates():
			if entry == nil {
				continue
			}

			if entry.Operation == kv.EntryOperationDelete || entry.Operation == kv.EntryOperationPurge {
				// Leader key deleted or expired — try to acquire
				logger.InfoCF("swarm", "Leader key deleted/expired, attempting acquisition", nil)
				le.tryAcquire()

				le.mu.RLock()
				amLeader := le.isLeader
				le.mu.RUnlock()
				if amLeader {
					return // Exit watch loop, mainLoop will start renewalLoop
				}
				continue
			}

			// Leader key updated — check who the leader is
			var lockVal LeaderLockValue
			if err := json.Unmarshal(entry.Value, &lockVal); err != nil {
				continue
			}

			le.mu.Lock()
			if le.currentLeader != lockVal.NodeID {
				le.setFollower(lockVal.NodeID)
			}
			le.mu.Unlock()
		}
	}
}

// becomeLeader marks this node as the leader. Must be called with mu held.
func (le *LeaderElection) becomeLeader() {
	if !le.isLeader {
		le.isLeader = true
		le.currentLeader = le.localNodeID
		logger.InfoCF("swarm", "This node is now the leader", map[string]any{"node_id": le.localNodeID})

		select {
		case le.leaderChangeCh <- le.localNodeID:
		default:
			logger.WarnC("swarm", "Leader change notification dropped, channel full")
		}
	}
}

// setFollower marks this node as a follower with the given leader. Must be called with mu held.
func (le *LeaderElection) setFollower(leaderID string) {
	wasLeader := le.isLeader
	le.isLeader = false
	oldLeader := le.currentLeader
	le.currentLeader = leaderID

	if wasLeader || oldLeader != leaderID {
		if wasLeader {
			logger.InfoCF("swarm", "This node is now a follower", map[string]any{
				"node_id":    le.localNodeID,
				"new_leader": leaderID,
			})
		}

		select {
		case le.leaderChangeCh <- leaderID:
		default:
			logger.WarnC("swarm", "Leader change notification dropped, channel full")
		}
	}
}

// LeadershipState represents the current leadership state.
type LeadershipState struct {
	LeaderID   string `json:"leader_id"`
	IsLeader   bool   `json:"is_leader"`
	LastChange int64  `json:"last_change"` // Unix nano
}

// GetState returns the current leadership state.
func (le *LeaderElection) GetState() LeadershipState {
	le.mu.RLock()
	defer le.mu.RUnlock()

	return LeadershipState{
		LeaderID: le.currentLeader,
		IsLeader: le.isLeader,
	}
}
