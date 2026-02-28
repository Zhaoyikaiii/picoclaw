// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// HandoffReason represents the reason for a handoff.
type HandoffReason string

const (
	ReasonOverloaded   HandoffReason = "overloaded"    // Load is too high
	ReasonNoCapability HandoffReason = "no_capability" // Missing capability
	ReasonUserRequest  HandoffReason = "user_request"  // User explicitly requested
	ReasonNodeLeave    HandoffReason = "node_leave"    // Node is leaving
	ReasonShutdown     HandoffReason = "shutdown"      // Graceful shutdown
)

// HandoffState represents the state of a handoff operation.
type HandoffState string

const (
	HandoffStatePending   HandoffState = "pending"
	HandoffStateAccepted  HandoffState = "accepted"
	HandoffStateRejected  HandoffState = "rejected"
	HandoffStateCompleted HandoffState = "completed"
	HandoffStateFailed    HandoffState = "failed"
	HandoffStateTimeout   HandoffState = "timeout"
	HandoffStateDuplicate HandoffState = "duplicate" // Replay attack detected
	HandoffStateExpired   HandoffState = "expired"   // Timestamp outside valid window
)

// Security constants for replay attack prevention.
const (
	// DefaultTimestampWindow is the acceptable timestamp skew (±60 seconds).
	DefaultTimestampWindow = 60 * time.Second

	// DefaultNonceTTL is how long nonces are kept in cache.
	DefaultNonceTTL = 5 * time.Minute

	// DefaultNonceCacheSize is the ring buffer size for nonce tracking.
	DefaultNonceCacheSize = 1000
)

// SessionMessage represents a message in a session (local type replacing picolib.SessionMessage).
type SessionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// HandoffRequest represents a request to hand off a session.
type HandoffRequest struct {
	// Core identification
	RequestID string `json:"request_id"` // Unique handoff ID for idempotency

	// Handoff details
	Reason          HandoffReason     `json:"reason"`
	SessionKey      string            `json:"session_key"`
	SessionMessages []SessionMessage  `json:"session_messages,omitempty"`
	Context         map[string]any    `json:"context,omitempty"`
	RequiredCap     string            `json:"required_cap,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`

	// Routing information
	FromNodeID   string `json:"from_node_id"`
	FromNodeAddr string `json:"from_node_addr"`
	TargetNodeID string `json:"target_node_id,omitempty"`

	// Timestamp for replay protection (Unix nanoseconds)
	Timestamp int64 `json:"timestamp"`

	// Security fields
	Nonce         string         `json:"nonce,omitempty"`     // Unique value for replay protection
	Signature     string         `json:"signature,omitempty"` // HMAC-SHA256 signature
	EncryptedData *EncryptedData `json:"encrypted,omitempty"` // AES-GCM encrypted session/context
}

// signingPayload returns the canonical payload for signature calculation.
// Covers all routing fields to prevent message redirection attacks.
func (r *HandoffRequest) signingPayload() map[string]any {
	// Include payload hash for large payloads
	payloadHash := ""
	if len(r.SessionMessages) > 0 || len(r.Context) > 0 {
		payloadData := map[string]any{
			"session_messages": r.SessionMessages,
			"context":          r.Context,
		}
		if j, err := json.Marshal(payloadData); err == nil {
			hash := sha256.Sum256(j)
			payloadHash = fmt.Sprintf("%x", hash)[:16]
		}
	}

	return map[string]any{
		"request_id":   r.RequestID,
		"nonce":        r.Nonce,
		"from_node_id": r.FromNodeID,
		"to_node_id":   r.TargetNodeID, // Prevents redirection attacks
		"session_key":  r.SessionKey,
		"required_cap": r.RequiredCap,
		"timestamp":    r.Timestamp,
		"payload_hash": payloadHash,
	}
}

// HandoffResponse represents the response to a handoff request.
type HandoffResponse struct {
	RequestID  string       `json:"request_id"`
	Accepted   bool         `json:"accepted"`
	NodeID     string       `json:"node_id"`
	Reason     string       `json:"reason,omitempty"`
	SessionKey string       `json:"session_key,omitempty"` // New session key on target
	Timestamp  int64        `json:"timestamp"`
	State      HandoffState `json:"state"`

	// TruncationInfo indicates if session history was truncated during handoff.
	TruncationInfo *TruncationInfo `json:"truncation_info,omitempty"`
}

// TruncationInfo contains details about message truncation.
type TruncationInfo struct {
	OriginalCount  int `json:"original_count"`
	RemainingCount int `json:"remaining_count"`
	BytesRemoved   int `json:"bytes_removed"`
}

// HandoffCoordinator coordinates handoff operations between nodes via NATS request-reply.
//
// Each node subscribes to its own targeted handoff subject:
//
//	picoclaw.swarm.handoff.<localNodeID>
//
// When initiating a handoff, the coordinator sends a NATS request to the target node's
// handoff subject and waits for a synchronous reply.
type HandoffCoordinator struct {
	discovery  Discovery
	membership *MembershipManager
	config     HandoffConfig
	nc         *nats.Conn

	pending map[string]*HandoffOperation // request_id -> operation
	mu      sync.RWMutex

	sub *nats.Subscription // Subscription for incoming handoff requests

	// Security
	signer          *Signer
	encryptor       *Encryptor
	cryptoConfig    CryptoConfig
	nonceCache      *NonceCache
	timestampWindow time.Duration

	// Idempotency: track processed handoff IDs
	processed   map[string]*HandoffResponse // request_id -> cached response
	processedMu sync.RWMutex

	// Failure policies
	circuitBreaker *CircuitBreaker
	nodeCooldown   *NodeCooldown
	failurePolicy  FailurePolicy

	// Accept/reject callbacks
	onHandoffRequest  func(*HandoffRequest) *HandoffResponse
	onHandoffComplete func(*HandoffRequest, *HandoffResponse)

	// Metrics
	metrics *HandoffMetrics
}

// HandoffOperation represents an ongoing handoff operation.
type HandoffOperation struct {
	Request    *HandoffRequest
	Response   *HandoffResponse
	State      HandoffState
	StartTime  time.Time
	LastUpdate time.Time
	RetryCount int
	TargetNode *NodeWithState
}

// NewHandoffCoordinator creates a new handoff coordinator.
func NewHandoffCoordinator(ds Discovery, config HandoffConfig) *HandoffCoordinator {
	policy := DefaultFailurePolicy()
	return &HandoffCoordinator{
		discovery:       ds,
		membership:      ds.GetMembershipManager(),
		config:          config,
		nc:              ds.NATSConn(),
		pending:         make(map[string]*HandoffOperation),
		processed:       make(map[string]*HandoffResponse),
		metrics:         NewHandoffMetrics(),
		nonceCache:      NewNonceCache(DefaultNonceTTL, DefaultNonceCacheSize),
		timestampWindow: DefaultTimestampWindow,
		circuitBreaker:  NewCircuitBreaker(policy.MaxConsecutiveFailures, policy.CircuitBreakerCooldown.Duration),
		nodeCooldown:    NewNodeCooldown(policy.OverloadCooldown.Duration),
		failurePolicy:   *policy,
	}
}

// SetCryptoConfig configures the cryptographic components for secure handoff.
// Returns error if RequireAuth or RequireEncryption is enabled without proper credentials.
func (hc *HandoffCoordinator) SetCryptoConfig(cfg CryptoConfig) error {
	hc.cryptoConfig = cfg
	hc.signer = NewSigner(cfg.SharedSecret)

	if cfg.EncryptionKey != "" {
		enc, err := NewEncryptor(cfg.EncryptionKey)
		if err != nil {
			return fmt.Errorf("failed to initialize encryptor: %w", err)
		}
		hc.encryptor = enc
	}

	// Strict validation: error out if auth/encryption required but credentials missing
	if cfg.RequireAuth && cfg.SharedSecret == "" {
		return fmt.Errorf("RequireAuth=true but SharedSecret is empty; authentication cannot be enforced")
	}

	if cfg.RequireEncryption && cfg.EncryptionKey == "" {
		return fmt.Errorf("RequireEncryption=true but EncryptionKey is empty; encryption cannot be enforced")
	}

	return nil
}

// Start begins listening for incoming handoff requests on the targeted NATS subject.
func (hc *HandoffCoordinator) Start() error {
	if hc.nc == nil {
		return ErrNATSNotConnected
	}

	localNodeID := hc.discovery.LocalNode().ID
	subject := SubjectHandoff + "." + localNodeID

	var err error
	hc.sub, err = hc.nc.Subscribe(subject, hc.handleIncomingNATSMsg)
	if err != nil {
		return fmt.Errorf("failed to subscribe to handoff subject %s: %w", subject, err)
	}

	logger.InfoCF("swarm", "Handoff coordinator started", map[string]any{
		"subject": subject,
	})

	return nil
}

// Close cleans up the handoff coordinator.
func (hc *HandoffCoordinator) Close() error {
	if hc.sub != nil {
		return hc.sub.Unsubscribe()
	}
	return nil
}

// handleIncomingNATSMsg handles a NATS message containing a HandoffRequest.
func (hc *HandoffCoordinator) handleIncomingNATSMsg(msg *nats.Msg) {
	var req HandoffRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		hc.metrics.RecordFailed()
		logger.ErrorCF("swarm", "Failed to decode handoff request", map[string]any{"error": err})
		hc.rejectHandoff(msg, "", "invalid_format", "")
		return
	}

	// Validate required fields
	if req.RequestID == "" || req.FromNodeID == "" {
		hc.metrics.RecordFailed()
		hc.rejectHandoff(msg, req.RequestID, "missing_required_fields", "")
		return
	}

	// Check idempotency: was this handoff already processed?
	hc.processedMu.RLock()
	if cachedResp, exists := hc.processed[req.RequestID]; exists {
		hc.processedMu.RUnlock()
		logger.InfoCF("swarm", "Returning cached handoff response (duplicate)", map[string]any{
			"request_id": req.RequestID,
			"from_node":  req.FromNodeID,
		})
		respData, _ := json.Marshal(cachedResp)
		msg.Respond(respData)
		return
	}
	hc.processedMu.RUnlock()

	now := time.Now()

	// Timestamp validation: check for replay attacks
	if req.Timestamp > 0 {
		reqTime := time.Unix(0, req.Timestamp)
		skew := reqTime.Sub(now)

		// Log clock skew for debugging (even if within window)
		if skew.Abs() > time.Second {
			logger.DebugCF("swarm", "Handoff timestamp skew", map[string]any{
				"skew_ms":   skew.Milliseconds(),
				"from_node": req.FromNodeID,
			})
		}

		if skew.Abs() > hc.timestampWindow {
			hc.metrics.RecordAuthFailure()
			logger.WarnCF("swarm", "Handoff request timestamp outside valid window", map[string]any{
				"skew_seconds":   skew.Seconds(),
				"window_seconds": hc.timestampWindow.Seconds(),
				"from_node":      req.FromNodeID,
				"request_id":     req.RequestID,
			})
			resp := &HandoffResponse{
				RequestID: req.RequestID,
				Accepted:  false,
				Reason:    "unauthorized",
				State:     HandoffStateExpired,
				Timestamp: now.UnixNano(),
			}
			hc.cacheAndRespond(msg, req.RequestID, resp)
			return
		}
	}

	// Nonce validation: check for replay attacks
	if req.Nonce != "" && hc.cryptoConfig.RequireAuth {
		if !hc.nonceCache.CheckAndAdd(req.Nonce, req.FromNodeID) {
			hc.metrics.RecordAuthFailure()
			logger.WarnCF("swarm", "Handoff request with duplicate nonce rejected (replay attack)", map[string]any{
				"from_node":  req.FromNodeID,
				"request_id": req.RequestID,
			})
			resp := &HandoffResponse{
				RequestID: req.RequestID,
				Accepted:  false,
				Reason:    "unauthorized",
				State:     HandoffStateDuplicate,
				Timestamp: now.UnixNano(),
			}
			hc.cacheAndRespond(msg, req.RequestID, resp)
			return
		}
	}

	// Verify signature if authentication is required
	if hc.signer != nil && hc.cryptoConfig.RequireAuth {
		if !hc.signer.Verify(req.signingPayload(), req.Signature) {
			hc.metrics.RecordAuthFailure()
			logger.WarnCF("swarm", "Handoff request signature verification failed", map[string]any{
				"from_node":  req.FromNodeID,
				"request_id": req.RequestID,
			})
			resp := &HandoffResponse{
				RequestID: req.RequestID,
				Accepted:  false,
				Reason:    "unauthorized",
				State:     HandoffStateRejected,
				Timestamp: now.UnixNano(),
			}
			hc.cacheAndRespond(msg, req.RequestID, resp)
			return
		}
	}

	// Don't self-handoff
	localNodeID := hc.discovery.LocalNode().ID
	if req.FromNodeID == localNodeID {
		return
	}

	// Decrypt session data if encrypted
	if req.EncryptedData != nil && hc.encryptor != nil {
		decrypted, err := hc.encryptor.Decrypt(req.EncryptedData)
		if err != nil {
			hc.metrics.RecordDecryptError()
			logger.WarnCF("swarm", "Handoff data decryption failed", map[string]any{
				"from_node":  req.FromNodeID,
				"request_id": req.RequestID,
			})
			resp := &HandoffResponse{
				RequestID: req.RequestID,
				Accepted:  false,
				Reason:    "unauthorized",
				State:     HandoffStateRejected,
				Timestamp: now.UnixNano(),
			}
			hc.cacheAndRespond(msg, req.RequestID, resp)
			return
		}

		// Unmarshal the decrypted data into a temporary struct to extract session/context
		var decryptedData struct {
			SessionMessages []SessionMessage `json:"session_messages"`
			Context         map[string]any   `json:"context"`
		}
		if err := json.Unmarshal(decrypted, &decryptedData); err == nil {
			req.SessionMessages = decryptedData.SessionMessages
			req.Context = decryptedData.Context
		}
	}

	// Process the request
	resp := hc.HandleIncomingHandoff(&req)
	resp.Timestamp = now.UnixNano()

	// Cache response for idempotency
	hc.cacheAndRespond(msg, req.RequestID, resp)
}

// rejectHandoff sends a generic rejection response without caching.
func (hc *HandoffCoordinator) rejectHandoff(msg *nats.Msg, requestID, reason, state string) {
	resp := &HandoffResponse{
		RequestID: requestID,
		Accepted:  false,
		Reason:    "unauthorized", // Always return generic reason to caller
		State:     HandoffStateRejected,
		Timestamp: time.Now().UnixNano(),
	}
	if state != "" {
		resp.State = HandoffState(state)
	}
	respData, _ := json.Marshal(resp)
	msg.Respond(respData)

	// Log the real reason internally
	logger.DebugCF("swarm", "Handoff rejected", map[string]any{
		"request_id": requestID,
		"reason":     reason,
	})
}

// cacheAndRespond caches the response and sends it.
func (hc *HandoffCoordinator) cacheAndRespond(msg *nats.Msg, requestID string, resp *HandoffResponse) {
	hc.processedMu.Lock()
	hc.processed[requestID] = resp
	hc.processedMu.Unlock()

	respData, err := json.Marshal(resp)
	if err != nil {
		logger.ErrorCF("swarm", "Failed to encode handoff response", map[string]any{"error": err})
		return
	}
	if err := msg.Respond(respData); err != nil {
		logger.ErrorCF("swarm", "Failed to send handoff response", map[string]any{"error": err})
	}
}

// CanHandle checks if the local node can handle a request.
func (hc *HandoffCoordinator) CanHandle(requiredCap string) bool {
	if !hc.config.Enabled {
		return false
	}

	// Check load
	localNode := hc.discovery.LocalNode()
	loadScore := localNode.LoadScore
	if loadScore > hc.config.LoadThreshold {
		return false
	}

	// Check capability
	if requiredCap != "" {
		hasCap := false
		for _, cap := range localNode.AgentCaps {
			if cap == requiredCap {
				hasCap = true
				break
			}
		}
		if !hasCap {
			return false
		}
	}

	return true
}

// InitiateHandoff initiates a handoff to another node via NATS request-reply.
func (hc *HandoffCoordinator) InitiateHandoff(ctx context.Context, req *HandoffRequest) (*HandoffResponse, error) {
	if hc.nc == nil {
		return nil, ErrNATSNotConnected
	}

	if req.RequestID == "" {
		req.RequestID = uuid.New().String()
	}

	localNode := hc.discovery.LocalNode()
	req.FromNodeID = localNode.ID
	req.FromNodeAddr = localNode.Addr
	req.Timestamp = time.Now().UnixNano()

	// Generate nonce for replay protection if auth is enabled
	if hc.signer != nil && hc.cryptoConfig.RequireAuth && req.Nonce == "" {
		req.Nonce = uuid.New().String()
	}

	// Find target node
	targetNode, findErr := hc.findTargetNode(req)
	if findErr != nil {
		//nolint:nilerr // Failure is communicated via HandoffResponse, not Go error
		return &HandoffResponse{
			RequestID: req.RequestID,
			Accepted:  false,
			Reason:    findErr.Error(),
			State:     HandoffStateFailed,
		}, nil
	}

	// Create operation
	op := &HandoffOperation{
		Request:    req,
		State:      HandoffStatePending,
		StartTime:  time.Now(),
		LastUpdate: time.Now(),
		TargetNode: targetNode,
	}

	hc.mu.Lock()
	hc.pending[req.RequestID] = op
	hc.mu.Unlock()

	// Send request via NATS request-reply to targeted subject
	req.TargetNodeID = targetNode.Node.ID
	resp, err := hc.sendHandoffRequest(ctx, targetNode.Node.ID, req)
	if err != nil {
		// System error - return immediately, don't retry
		hc.mu.Lock()
		delete(hc.pending, req.RequestID)
		hc.mu.Unlock()
		return nil, fmt.Errorf("handoff request failed: %w", err)
	}

	// Retry if needed
retryLoop:
	for !resp.Accepted && op.RetryCount < hc.config.MaxRetries {
		op.RetryCount++
		hc.metrics.RecordRetry()

		// Find new target
		newTarget, findErr := hc.findTargetNode(req)
		if findErr != nil {
			continue
		}
		op.TargetNode = newTarget

		// Delay before retry
		select {
		case <-ctx.Done():
			resp = &HandoffResponse{
				RequestID: req.RequestID,
				Accepted:  false,
				Reason:    "context canceled",
				State:     HandoffStateFailed,
			}

			break retryLoop
		case <-time.After(hc.config.RetryDelay.Duration):
		}

		req.TargetNodeID = newTarget.Node.ID
		var retryErr error
		resp, retryErr = hc.sendHandoffRequest(ctx, newTarget.Node.ID, req)
		if retryErr != nil {
			// System error during retry - abort
			hc.mu.Lock()
			delete(hc.pending, req.RequestID)
			hc.mu.Unlock()
			return nil, fmt.Errorf("handoff retry failed: %w", retryErr)
		}
		if resp.Accepted {
			break
		}
	}

	// Clean up
	hc.mu.Lock()
	delete(hc.pending, req.RequestID)
	hc.mu.Unlock()

	// Notify callback
	if hc.onHandoffComplete != nil {
		go hc.onHandoffComplete(req, resp)
	}

	return resp, nil
}

// sendHandoffRequest sends a handoff request to a specific target node via NATS request-reply.
// Returns error for system failures (NATS timeout, network error, serialization failure).
// Returns HandoffResponse for business rejections (target node rejected).
func (hc *HandoffCoordinator) sendHandoffRequest(
	ctx context.Context,
	targetNodeID string,
	req *HandoffRequest,
) (*HandoffResponse, error) {
	startTime := time.Now()
	hc.metrics.RecordRequest()

	subject := SubjectHandoff + "." + targetNodeID

	// Prepare the request for transmission
	transmitReq := *req
	var truncInfo *TruncationInfo

	// Data minimization: limit session history to configured max
	// This reduces payload size and avoids sending unnecessary historical data
	if hc.config.MaxSessionHistory > 0 && len(transmitReq.SessionMessages) > hc.config.MaxSessionHistory {
		originalCount := len(transmitReq.SessionMessages)

		// Preserve system/developer messages, then take most recent conversation messages
		var systemMessages []SessionMessage
		var conversationMessages []SessionMessage

		for _, msg := range transmitReq.SessionMessages {
			if msg.Role == "system" || msg.Role == "developer" {
				systemMessages = append(systemMessages, msg)
			} else {
				conversationMessages = append(conversationMessages, msg)
			}
		}

		// Keep all system messages, but limit conversation to MaxSessionHistory
		availableSlots := hc.config.MaxSessionHistory - len(systemMessages)
		if availableSlots < 0 {
			availableSlots = 0
		}

		if len(conversationMessages) > availableSlots {
			// Take the most recent messages (from the end)
			startIdx := len(conversationMessages) - availableSlots
			conversationMessages = conversationMessages[startIdx:]
		}

		transmitReq.SessionMessages = append(systemMessages, conversationMessages...)

		if len(transmitReq.SessionMessages) < originalCount {
			logger.InfoCF("swarm", "Applied data minimization for handoff", map[string]any{
				"original_count":    originalCount,
				"transmitted_count": len(transmitReq.SessionMessages),
				"max_history":       hc.config.MaxSessionHistory,
			})
		}
	}

	// Check message size against NATS max payload.
	// If too large, truncate session history with smart rules.
	data, err := json.Marshal(transmitReq)
	if err != nil {
		hc.metrics.RecordFailed()
		return nil, fmt.Errorf("failed to marshal handoff request: %w", err)
	}

	originalSize := len(data)
	originalCount := len(transmitReq.SessionMessages)

	if len(data) > DefaultMaxHandoffPayloadBytes && len(transmitReq.SessionMessages) > 0 {
		logger.WarnCF("swarm", "Handoff payload exceeds max size, truncating session history", map[string]any{
			"original_size":    originalSize,
			"max_size":         DefaultMaxHandoffPayloadBytes,
			"original_history": originalCount,
		})

		transmitReq, data, truncInfo = hc.truncateSessionData(transmitReq, data)
		hc.metrics.RecordTruncation()

		if data == nil {
			hc.metrics.RecordFailed()
			return nil, fmt.Errorf("handoff payload too large even after truncation")
		}

		logger.InfoCF("swarm", "Session history truncated for handoff", map[string]any{
			"original_count":  originalCount,
			"remaining_count": len(transmitReq.SessionMessages),
			"bytes_removed":   originalSize - len(data),
		})
	}

	// Encrypt session data if configured
	if hc.encryptor != nil && hc.cryptoConfig.RequireEncryption {
		sessionData := map[string]any{
			"session_messages": transmitReq.SessionMessages,
			"context":          transmitReq.Context,
		}
		plainData, _ := json.Marshal(sessionData)
		encrypted, encryptErr := hc.encryptor.Encrypt(plainData)
		if encryptErr == nil {
			transmitReq.EncryptedData = encrypted
			transmitReq.SessionMessages = nil // Clear plaintext
			transmitReq.Context = nil
		}
		data, err = json.Marshal(transmitReq)
		if err != nil {
			hc.metrics.RecordFailed()
			return nil, fmt.Errorf("failed to marshal handoff request after encryption: %w", err)
		}
	}

	// Sign the request
	if hc.signer != nil {
		signature, signErr := hc.signer.Sign(transmitReq.signingPayload())
		if signErr != nil {
			logger.WarnCF("swarm", "Failed to sign handoff request", map[string]any{"error": signErr})
		} else {
			transmitReq.Signature = signature
			data, _ = json.Marshal(transmitReq)
		}
	}

	// Use NATS request-reply (timeout controlled by parent context)
	msg, err := hc.nc.RequestWithContext(ctx, subject, data)
	if err != nil {
		hc.metrics.RecordTimeout()
		hc.circuitBreaker.RecordFailure(targetNodeID)
		// Return system error for network/timeout failures
		return nil, fmt.Errorf("NATS request failed: %w", err)
	}

	var resp HandoffResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		hc.metrics.RecordFailed()
		hc.circuitBreaker.RecordFailure(targetNodeID)
		// Return system error for data corruption
		return nil, fmt.Errorf("failed to decode handoff response: %w", err)
	}

	// Attach truncation info to response
	resp.TruncationInfo = truncInfo

	if resp.Accepted {
		hc.metrics.RecordAccepted()
		hc.circuitBreaker.RecordSuccess(targetNodeID)
		hc.nodeCooldown.Remove(targetNodeID)
	} else {
		hc.metrics.RecordRejected()
		hc.circuitBreaker.RecordFailure(targetNodeID)
		// If rejected due to overload, add to cooldown
		if resp.State == HandoffStateRejected && (resp.Reason == "cannot handle" || resp.Reason == "overloaded") {
			hc.nodeCooldown.Add(targetNodeID)
		}
	}

	hc.metrics.RecordLatency(time.Since(startTime))

	return &resp, nil
}

// truncateSessionData smartly truncates session history, preserving important messages.
func (hc *HandoffCoordinator) truncateSessionData(
	req HandoffRequest,
	data []byte,
) (HandoffRequest, []byte, *TruncationInfo) {
	truncated := req
	originalCount := len(truncated.SessionMessages)
	bytesRemoved := 0

	// First pass: Remove oldest user/assistant messages, preserve system messages
	var systemMessages []SessionMessage
	var conversationMessages []SessionMessage

	for _, msg := range truncated.SessionMessages {
		if msg.Role == "system" || msg.Role == "developer" {
			systemMessages = append(systemMessages, msg)
		} else {
			conversationMessages = append(conversationMessages, msg)
		}
	}

	// Keep system messages, remove from oldest conversation messages first
	for len(data) > DefaultMaxHandoffPayloadBytes && len(conversationMessages) > 0 {
		removed := conversationMessages[0]
		bytesRemoved += len(removed.Role) + len(removed.Content) + 20 // Approximate JSON overhead
		conversationMessages = conversationMessages[1:]

		truncated.SessionMessages = append(systemMessages, conversationMessages...)
		var err error
		data, err = json.Marshal(truncated)
		if err != nil {
			return req, nil, &TruncationInfo{
				OriginalCount:  originalCount,
				RemainingCount: len(truncated.SessionMessages),
				BytesRemoved:   bytesRemoved,
			}
		}
	}

	// Second pass: If still too large, start removing from the end (keep recent)
	systemMsgCount := len(systemMessages)
	for len(data) > DefaultMaxHandoffPayloadBytes && len(truncated.SessionMessages) > systemMsgCount {
		removed := truncated.SessionMessages[len(truncated.SessionMessages)-1]
		bytesRemoved += len(removed.Role) + len(removed.Content) + 20
		truncated.SessionMessages = truncated.SessionMessages[:len(truncated.SessionMessages)-1]

		var err error
		data, err = json.Marshal(truncated)
		if err != nil {
			return req, nil, &TruncationInfo{
				OriginalCount:  originalCount,
				RemainingCount: len(truncated.SessionMessages),
				BytesRemoved:   bytesRemoved,
			}
		}
	}

	if len(data) > DefaultMaxHandoffPayloadBytes {
		return req, nil, &TruncationInfo{
			OriginalCount:  originalCount,
			RemainingCount: len(truncated.SessionMessages),
			BytesRemoved:   bytesRemoved,
		}
	}

	return truncated, data, &TruncationInfo{
		OriginalCount:  originalCount,
		RemainingCount: len(truncated.SessionMessages),
		BytesRemoved:   bytesRemoved,
	}
}

// HandleIncomingHandoff handles a handoff request received from another node.
func (hc *HandoffCoordinator) HandleIncomingHandoff(req *HandoffRequest) *HandoffResponse {
	// Check if we can handle it
	accepted := hc.CanHandle(req.RequiredCap)
	localNode := hc.discovery.LocalNode()
	response := &HandoffResponse{
		RequestID: req.RequestID,
		Accepted:  accepted,
		NodeID:    localNode.ID,
		State:     HandoffStateAccepted,
		Timestamp: time.Now().UnixNano(),
	}

	if !accepted {
		response.Reason = "cannot handle (overloaded or missing capability)"
		response.State = HandoffStateRejected
	}

	// Call custom handler if set
	if hc.onHandoffRequest != nil {
		response = hc.onHandoffRequest(req)
	}

	return response
}

// findTargetNode finds a suitable target node for handoff.
func (hc *HandoffCoordinator) findTargetNode(req *HandoffRequest) (*NodeWithState, error) {
	var candidates []*NodeWithState

	if req.RequiredCap != "" {
		candidates = hc.membership.SelectByCapability([]string{req.RequiredCap})
	} else {
		candidates = hc.membership.GetAvailableMembers()
	}

	// Filter out nodes that are in circuit breaker open state or cooldown
	var healthyCandidates []*NodeWithState
	for _, c := range candidates {
		nodeID := c.Node.ID
		// Skip if circuit is open
		if hc.circuitBreaker.GetState(nodeID) == CircuitOpen {
			continue
		}
		// Skip if in overload cooldown
		if hc.nodeCooldown.IsCooled(nodeID) {
			continue
		}
		healthyCandidates = append(healthyCandidates, c)
	}

	if len(healthyCandidates) == 0 {
		return nil, ErrNoHealthyNodes
	}

	// Select least loaded node
	target := healthyCandidates[0]
	for _, c := range healthyCandidates[1:] {
		if c.Node.LoadScore < target.Node.LoadScore {
			target = c
		}
	}

	return target, nil
}

// SetRequestHandler sets a custom handler for handoff requests.
func (hc *HandoffCoordinator) SetRequestHandler(handler func(*HandoffRequest) *HandoffResponse) {
	hc.onHandoffRequest = handler
}

// SetCompleteHandler sets a callback for handoff completion.
func (hc *HandoffCoordinator) SetCompleteHandler(handler func(*HandoffRequest, *HandoffResponse)) {
	hc.onHandoffComplete = handler
}

// GetPending returns all pending handoff operations.
func (hc *HandoffCoordinator) GetPending() []*HandoffOperation {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	result := make([]*HandoffOperation, 0, len(hc.pending))
	for _, op := range hc.pending {
		result = append(result, op)
	}
	return result
}

// GetMetrics returns the current handoff metrics.
func (hc *HandoffCoordinator) GetMetrics() *HandoffMetrics {
	return hc.metrics
}

// ComponentHealth returns the health status of handoff components.
func (hc *HandoffCoordinator) ComponentHealth() map[string]bool {
	health := make(map[string]bool)
	health["coordinator"] = hc.sub != nil
	health["nats"] = hc.nc != nil
	health["signing"] = hc.signer != nil && hc.signer.enabled
	health["encryption"] = hc.encryptor != nil && hc.encryptor.enabled
	health["discovery"] = hc.discovery != nil
	health["membership"] = hc.membership != nil
	return health
}

// GetCircuitBreakerStatus returns the circuit breaker state for all tracked nodes.
func (hc *HandoffCoordinator) GetCircuitBreakerStatus() map[string]string {
	status := make(map[string]string)
	if hc.circuitBreaker == nil {
		return status
	}

	// Get all members
	if hc.membership != nil {
		members := hc.membership.GetAvailableMembers()
		for _, m := range members {
			nodeID := m.Node.ID
			state := hc.circuitBreaker.GetState(nodeID)
			if state != "" {
				status[nodeID] = string(state)
			}
		}
	}
	return status
}
