// PicoClaw - Ultra-lightweight personal AI agent
// Swarm failure mode policies for graceful degradation
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"sync"
	"time"
)

// FailurePolicy defines how the system behaves when failures occur.
type FailurePolicy struct {
	// TimeoutStrategy defines what to do on request-reply timeout.
	TimeoutStrategy TimeoutStrategy `json:"timeout_strategy"`

	// OverloadCooldown is how long to avoid a node after it's overloaded.
	OverloadCooldown Duration `json:"overload_cooldown"`

	// MaxConsecutiveFailures before circuit breaker opens.
	MaxConsecutiveFailures int `json:"max_consecutive_failures"`

	// CircuitBreakerCooldown is how long to wait before retrying a failed node.
	CircuitBreakerCooldown Duration `json:"circuit_breaker_cooldown"`
}

// TimeoutStrategy defines timeout fallback behavior.
type TimeoutStrategy string

const (
	// TimeoutFallbackLocal: fall back to local processing on timeout.
	TimeoutFallbackLocal TimeoutStrategy = "fallback_local"
	// TimeoutRetry: retry with another node on timeout.
	TimeoutRetry TimeoutStrategy = "retry"
	// TimeoutFail: return error to user on timeout.
	TimeoutFail TimeoutStrategy = "fail"
)

// DefaultFailurePolicy returns the default failure policy.
func DefaultFailurePolicy() *FailurePolicy {
	return &FailurePolicy{
		TimeoutStrategy:        TimeoutFallbackLocal,
		OverloadCooldown:       Duration{30 * time.Second},
		MaxConsecutiveFailures: 3,
		CircuitBreakerCooldown: Duration{60 * time.Second},
	}
}

// CircuitBreaker tracks node health and opens/closes circuits.
type CircuitBreaker struct {
	mu       sync.RWMutex
	nodes    map[string]*nodeState
	cooldown time.Duration
	maxFails int
}

type nodeState struct {
	consecutiveFails int
	lastFailTime     time.Time
	state            CircuitState
}

// CircuitState represents the circuit breaker state.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"    // Normal operation
	CircuitOpen     CircuitState = "open"      // Failing, stop sending
	CircuitHalfOpen CircuitState = "half-open" // Testing if recovered
)

// NewCircuitBreaker creates a new circuit breaker.
func NewCircuitBreaker(maxFails int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		nodes:    make(map[string]*nodeState),
		cooldown: cooldown,
		maxFails: maxFails,
	}
}

// CanProceed returns true if requests can proceed to the node.
func (cb *CircuitBreaker) CanProceed(nodeID string) bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	state, exists := cb.nodes[nodeID]
	if !exists {
		return true
	}

	// Check if circuit should be half-open (cooldown expired)
	if state.state == CircuitOpen && time.Since(state.lastFailTime) > cb.cooldown {
		return true // Allow one request to test
	}

	return state.state != CircuitOpen
}

// RecordSuccess records a successful request.
func (cb *CircuitBreaker) RecordSuccess(nodeID string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if state, exists := cb.nodes[nodeID]; exists {
		state.consecutiveFails = 0
		if state.state == CircuitHalfOpen {
			state.state = CircuitClosed
		}
	}
}

// RecordFailure records a failed request.
func (cb *CircuitBreaker) RecordFailure(nodeID string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	state := cb.nodes[nodeID]
	if state == nil {
		state = &nodeState{}
		cb.nodes[nodeID] = state
	}

	state.consecutiveFails++
	state.lastFailTime = time.Now()

	if state.consecutiveFails >= cb.maxFails {
		state.state = CircuitOpen
	}
}

// GetState returns the current state of a node.
func (cb *CircuitBreaker) GetState(nodeID string) CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if state, exists := cb.nodes[nodeID]; exists {
		// Check if cooldown expired
		if state.state == CircuitOpen && time.Since(state.lastFailTime) > cb.cooldown {
			return CircuitHalfOpen
		}
		return state.state
	}
	return CircuitClosed
}

// NodeCooldown tracks overloaded nodes to prevent oscillation.
type NodeCooldown struct {
	mu     sync.RWMutex
	cooled map[string]time.Time
	ttl    time.Duration
}

// NewNodeCooldown creates a new cooldown tracker.
func NewNodeCooldown(ttl time.Duration) *NodeCooldown {
	return &NodeCooldown{
		cooled: make(map[string]time.Time),
		ttl:    ttl,
	}
}

// IsCooled returns true if the node is in cooldown period.
func (nc *NodeCooldown) IsCooled(nodeID string) bool {
	nc.mu.RLock()
	defer nc.mu.RUnlock()

	if t, exists := nc.cooled[nodeID]; exists {
		if time.Since(t) < nc.ttl {
			return true
		}
	}
	return false
}

// Add puts a node into cooldown.
func (nc *NodeCooldown) Add(nodeID string) {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	nc.cooled[nodeID] = time.Now()
}

// Remove removes a node from cooldown (e.g., after successful request).
func (nc *NodeCooldown) Remove(nodeID string) {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	delete(nc.cooled, nodeID)
}

// Cleanup removes expired entries.
func (nc *NodeCooldown) Cleanup() {
	nc.mu.Lock()
	defer nc.mu.Unlock()

	now := time.Now()
	for nodeID, t := range nc.cooled {
		if now.Sub(t) >= nc.ttl {
			delete(nc.cooled, nodeID)
		}
	}
}
