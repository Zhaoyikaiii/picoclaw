// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/swarm"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// handleNodeRouting handles routing a message to a specific node in the swarm via NATS.
func (al *AgentLoop) handleNodeRouting(
	ctx context.Context,
	msg bus.InboundMessage,
	targetNodeID, content string,
) (string, error) {
	// Check if target is this node
	if targetNodeID == al.swarmDiscovery.LocalNode().ID {
		logger.InfoCF("swarm", "Target is this node, processing locally", map[string]any{"node_id": targetNodeID})
		msg.Content = content
		return al.processMessage(ctx, msg)
	}

	// Find target node
	members := al.swarmDiscovery.Members()
	var target *swarm.NodeWithState
	for _, m := range members {
		if m.Node.ID == targetNodeID {
			target = m
			break
		}
	}

	if target == nil {
		return fmt.Sprintf("Node '%s' not found in cluster. Use /nodes to see available nodes.", targetNodeID), nil
	}

	// Check if target is available
	if target.State.Status != swarm.NodeStatusAlive {
		return fmt.Sprintf("Node '%s' is not alive (status: %s)", targetNodeID, target.State.Status), nil
	}

	// Check if target is overloaded using configurable threshold with hysteresis
	rejectThreshold := al.cfg.Swarm.LoadMonitor.RoutingRejectThreshold
	if rejectThreshold == 0 {
		rejectThreshold = swarm.DefaultRoutingRejectThreshold // Default to 0.9
	}
	if target.Node.LoadScore > rejectThreshold {
		return fmt.Sprintf("Node '%s' is overloaded (load: %.0f%%, threshold: %.0f%%)",
			targetNodeID, target.Node.LoadScore*100, rejectThreshold*100), nil
	}

	nc := al.swarmDiscovery.NATSConn()
	if nc == nil {
		return "Node-to-node communication not initialized (NATS not connected)", nil
	}

	// Send message via NATS to target node's message subject
	subject := swarm.SubjectNodeMsg + "." + targetNodeID + ".msg"

	payload := map[string]any{
		"content":   content,
		"channel":   msg.Channel,
		"chat_id":   msg.ChatID,
		"sender_id": msg.SenderID,
		"from_node": al.swarmDiscovery.LocalNode().ID,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("Failed to marshal message: %v", err), nil
	}

	logger.InfoCF("swarm", "Sending message to remote node via NATS", map[string]any{
		"target":  targetNodeID,
		"subject": subject,
		"load":    target.Node.LoadScore,
	})

	// Use NATS request-reply for synchronous response
	reply, err := nc.RequestWithContext(ctx, subject, data)
	if err != nil {
		return fmt.Sprintf("Failed to send message to node '%s': %v", targetNodeID, err), nil
	}

	// Parse response
	var response map[string]any
	if err := json.Unmarshal(reply.Data, &response); err != nil {
		return string(reply.Data), nil //nolint:nilerr // Fallback to raw response on parse failure
	}

	if errMsg, ok := response["error"].(string); ok && errMsg != "" {
		return fmt.Sprintf("Node '%s' error: %s", targetNodeID, errMsg), nil
	}

	if resp, ok := response["response"].(string); ok {
		return resp, nil
	}

	return string(reply.Data), nil
}

// initSwarm initializes the swarm mode components.
func (al *AgentLoop) initSwarm() {
	logger.InfoC("swarm", "Initializing swarm mode")

	// Convert config and create discovery service directly (NATS-only)
	swarmConfig := al.convertToSwarmConfig(al.cfg.Swarm)
	discovery, err := swarm.NewDiscoveryService(swarmConfig)
	if err != nil {
		logger.ErrorCF(
			"swarm",
			"SWARM INIT FAILED: discovery service creation failed — swarm mode will be disabled",
			map[string]any{"error": err.Error()},
		)
		al.swarmInitError = fmt.Errorf("discovery service creation failed: %w", err)
		return
	}
	if err := discovery.Start(); err != nil {
		logger.ErrorCF(
			"swarm",
			"SWARM INIT FAILED: discovery service start failed — swarm mode will be disabled",
			map[string]any{"error": err.Error()},
		)
		al.swarmInitError = fmt.Errorf("discovery service start failed: %w", err)
		return
	}
	al.swarmDiscovery = discovery

	// Create handoff coordinator using config from convertToSwarmConfig (with default duration protection)
	al.swarmHandoff = swarm.NewHandoffCoordinator(discovery, swarmConfig.Handoff)

	// Configure crypto settings if provided
	if al.cfg.Swarm.Handoff.SharedSecret != "" || al.cfg.Swarm.Handoff.EncryptionKey != "" {
		cryptoCfg := swarm.CryptoConfig{
			SharedSecret:      al.cfg.Swarm.Handoff.SharedSecret,
			EncryptionKey:     al.cfg.Swarm.Handoff.EncryptionKey,
			RequireAuth:       al.cfg.Swarm.Handoff.RequireAuth,
			RequireEncryption: al.cfg.Swarm.Handoff.RequireEncryption,
		}
		if err := al.swarmHandoff.SetCryptoConfig(cryptoCfg); err != nil {
			logger.ErrorCF(
				"swarm",
				"Failed to configure handoff crypto — handoff disabled",
				map[string]any{"error": err.Error()},
			)
			al.swarmHandoff.Close()
			al.swarmHandoff = nil
		} else {
			logger.InfoCF("swarm", "Handoff crypto configured", map[string]any{
				"auth_enabled":       al.cfg.Swarm.Handoff.SharedSecret != "",
				"encryption_enabled": al.cfg.Swarm.Handoff.EncryptionKey != "",
			})
		}
	}

	// Initialize signer for node-to-node message authentication
	// Uses the same SharedSecret as handoff for consistency
	if al.cfg.Swarm.Handoff.SharedSecret != "" {
		al.swarmSigner = swarm.NewSigner(al.cfg.Swarm.Handoff.SharedSecret)
		logger.InfoCF("swarm", "Node message signing enabled", map[string]any{
			"require_auth": al.cfg.Swarm.Handoff.RequireAuth,
		})
	} else if al.cfg.Swarm.Handoff.RequireAuth {
		logger.WarnCF(
			"swarm",
			"RequireAuth is set but no SharedSecret provided - node messages will not be signed or verified",
			nil,
		)
	}

	if al.swarmHandoff != nil {
		if err := al.swarmHandoff.Start(); err != nil {
			logger.ErrorCF("swarm", "Failed to start handoff coordinator", map[string]any{"error": err.Error()})
		}
	}

	// Create and start load monitor using config from convertToSwarmConfig (with default duration protection)
	al.swarmLoad = swarm.NewLoadMonitor(&swarmConfig.LoadMonitor)
	if al.cfg.Swarm.LoadMonitor.Enabled {
		al.swarmLoad.Start()
		al.swarmLoad.OnThreshold(func(score float64) {
			discovery.UpdateLoad(score)
		})
	}

	// Wire load monitor to discovery service for detailed status publishing
	discovery.SetLoadMonitor(al.swarmLoad)

	al.swarmEnabled = true

	// Initialize leader election if enabled
	if al.cfg.Swarm.LeaderElection.Enabled {
		al.initSwarmLeaderElection(discovery)
	}

	// Set up NATS subscription for incoming directed node messages
	al.setupNodeMessageSubscription(discovery)

	// Register swarm tools
	al.registerSwarmTools(discovery)

	// Subscribe to node events for logging
	al.subscribeSwarmEvents(discovery)

	logger.InfoCF("swarm", "Swarm mode initialized", map[string]any{
		"node_id":  discovery.LocalNode().ID,
		"nats_url": al.cfg.Swarm.Discovery.NATSURL,
		"handoff":  al.cfg.Swarm.Handoff.Enabled,
	})
}

// setupNodeMessageSubscription subscribes to incoming directed messages via NATS.
func (al *AgentLoop) setupNodeMessageSubscription(discovery swarm.Discovery) {
	nc := discovery.NATSConn()
	if nc == nil {
		logger.WarnC("swarm", "NATS connection not available, node message subscription skipped")
		return
	}

	localNodeID := discovery.LocalNode().ID
	subject := swarm.SubjectNodeMsg + "." + localNodeID + ".msg"

	_, err := nc.Subscribe(subject, func(msg *nats.Msg) {
		al.handleIncomingNATSNodeMessage(msg)
	})
	if err != nil {
		logger.ErrorCF("swarm", "Failed to subscribe to node message subject", map[string]any{
			"subject": subject,
			"error":   err.Error(),
		})
		return
	}

	logger.InfoCF("swarm", "Subscribed to node messages", map[string]any{"subject": subject})
}

// handleIncomingNATSNodeMessage handles a message received from another node via NATS.
func (al *AgentLoop) handleIncomingNATSNodeMessage(msg *nats.Msg) {
	var payload map[string]any
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		logger.ErrorCF("swarm", "Failed to decode incoming node message", map[string]any{"error": err.Error()})
		al.sendNodeReply(msg, "invalid message format", nil)
		return
	}

	// Verify sender is a known swarm member (routing authorization)
	fromNode, _ := payload["from_node"].(string)
	if fromNode != "" && al.swarmDiscovery != nil {
		members := al.swarmDiscovery.Members()
		isKnownMember := false
		for _, m := range members {
			if m.Node.ID == fromNode {
				isKnownMember = true
				break
			}
		}
		if !isKnownMember {
			logger.WarnCF("swarm", "Rejected node message from unknown member", map[string]any{
				"from_node":     fromNode,
				"known_members": len(members),
			})
			al.sendNodeReply(msg, "unknown node", nil)
			return
		}
	}

	// Verify message signature if signer is configured
	signature, hasSignature := payload["signature"].(string)
	if hasSignature && al.swarmSigner != nil {
		// Create a copy of payload without signature for verification
		payloadToVerify := make(map[string]any)
		for k, v := range payload {
			if k != "signature" {
				payloadToVerify[k] = v
			}
		}
		if !al.swarmSigner.Verify(payloadToVerify, signature) {
			logger.WarnCF("swarm", "Rejected node message with invalid signature", map[string]any{
				"from_node": fromNode,
			})
			al.sendNodeReply(msg, "invalid signature", nil)
			return
		}
	} else if al.cfg.Swarm.Handoff.RequireAuth && al.swarmSigner == nil {
		// RequireAuth is enabled but no signer configured - reject for safety
		logger.WarnCF(
			"swarm",
			"Rejected node message - RequireAuth enabled but no signature verification configured",
			map[string]any{
				"from_node": fromNode,
			},
		)
		al.sendNodeReply(msg, "signature required but not configured", nil)
		return
	} else if al.cfg.Swarm.Handoff.RequireAuth && !hasSignature {
		// RequireAuth is enabled but message has no signature
		logger.WarnCF("swarm", "Rejected unsigned node message - RequireAuth enabled", map[string]any{
			"from_node": fromNode,
		})
		al.sendNodeReply(msg, "signature required", nil)
		return
	}

	// Check for action-based request (lightweight, no LLM processing)
	action, hasAction := payload["action"].(string)
	if hasAction {
		al.handleNodeActionRequest(msg, action, payload, fromNode)
		return
	}

	// Content-based message (full LLM processing)
	content, _ := payload["content"].(string)
	channel, _ := payload["channel"].(string)
	chatID, _ := payload["chat_id"].(string)
	senderID, _ := payload["sender_id"].(string)

	// Require content for message processing
	if content == "" {
		al.sendNodeReply(msg, "empty content", nil)
		return
	}

	logger.InfoCF("swarm", "Processing incoming node message", map[string]any{
		"from":    fromNode,
		"content": content[:min(50, len(content))],
	})

	// Create an inbound message and process with a timeout context
	inboundMsg := bus.InboundMessage{
		Content:  content,
		Channel:  channel,
		ChatID:   chatID,
		SenderID: senderID,
	}

	// Use parent context from AgentLoop for proper cancellation propagation
	// If al.ctx is not set (e.g., during initialization), fall back to background context
	const nodeMessageTimeout = 120 * time.Second
	parentCtx := al.ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, nodeMessageTimeout)
	defer cancel()

	response, err := al.processMessage(ctx, inboundMsg)

	// Send reply if request-reply pattern
	if msg.Reply != "" {
		var respData []byte
		if err != nil {
			respData, _ = json.Marshal(map[string]any{"error": err.Error()})
		} else {
			respData, _ = json.Marshal(map[string]any{"response": response})
		}
		msg.Respond(respData)
	}
}

// handleNodeActionRequest handles lightweight action requests (status, ping, health, etc.)
// These actions bypass LLM processing and return structured responses directly.
func (al *AgentLoop) handleNodeActionRequest(msg *nats.Msg, action string, payload map[string]any, fromNode string) {
	logger.InfoCF("swarm", "Processing node action request", map[string]any{
		"from":   fromNode,
		"action": action,
	})

	var response any
	var err error

	switch action {
	case swarm.NodeActionPing:
		response = map[string]any{
			"status":  "pong",
			"node_id": al.swarmDiscovery.LocalNode().ID,
		}

	case swarm.NodeActionStatus:
		response = al.GetSwarmStatus()

	case swarm.NodeActionHealth:
		response = map[string]any{
			"status":  "healthy",
			"node_id": al.swarmDiscovery.LocalNode().ID,
		}

	case swarm.NodeActionLeader:
		if al.swarmLeaderElection != nil {
			response = map[string]any{
				"leader_id": al.swarmLeaderElection.GetLeader(),
				"is_leader": al.swarmLeaderElection.IsLeader(),
			}
		} else {
			response = map[string]any{
				"leader_id": nil,
				"message":   "leader election not enabled",
			}
		}

	case swarm.NodeActionMetrics:
		response = al.GetSwarmStatus()

	default:
		err = fmt.Errorf("unknown action: %s", action)
	}

	// Send reply
	if msg.Reply != "" {
		respData, _ := json.Marshal(map[string]any{
			"response": response,
			"error":    err,
		})
		msg.Respond(respData)
	}
}

// sendNodeReply sends a standardized error reply to a node message.
func (al *AgentLoop) sendNodeReply(msg *nats.Msg, errMsg string, payload map[string]any) {
	if msg.Reply == "" {
		return
	}
	reply := map[string]any{"error": errMsg}
	if payload != nil {
		for k, v := range payload {
			reply[k] = v
		}
	}
	respData, _ := json.Marshal(reply)
	msg.Respond(respData)
}

// initSwarmLeaderElection initializes the leader election module using NATS KV CAS lock.
func (al *AgentLoop) initSwarmLeaderElection(discovery swarm.Discovery) {
	js := discovery.JetStream()
	if js == nil {
		logger.WarnCF("swarm", "JetStream not available, leader election disabled", nil)
		return
	}

	// Convert config
	leaderElectionConfig := swarm.LeaderElectionConfig{
		Enabled: al.cfg.Swarm.LeaderElection.Enabled,
		LockTTL: swarm.Duration{
			Duration: time.Duration(al.cfg.Swarm.LeaderElection.LockTTL) * time.Second,
		},
		RenewalInterval: swarm.Duration{
			Duration: time.Duration(al.cfg.Swarm.LeaderElection.RenewalInterval) * time.Second,
		},
	}

	// Create leader election instance with JetStream
	buckets := discovery.Buckets()
	if buckets == nil || buckets.Leader == nil {
		logger.WarnCF("swarm", "Leader bucket not available, leader election disabled", nil)
		return
	}
	leaderElection, err := swarm.NewLeaderElection(
		discovery.LocalNode().ID,
		buckets.Leader,
		leaderElectionConfig,
	)
	if err != nil {
		logger.ErrorCF("swarm", "Failed to create leader election", map[string]any{"error": err.Error()})
		return
	}

	// Start leader election
	if err := leaderElection.Start(); err != nil {
		logger.ErrorCF("swarm", "Failed to start leader election", map[string]any{"error": err.Error()})
		return
	}
	al.swarmLeaderElection = leaderElection

	logger.InfoCF("swarm", "Leader election initialized (KV CAS lock)", map[string]any{
		"node_id": discovery.LocalNode().ID,
		"enabled": al.cfg.Swarm.LeaderElection.Enabled,
	})
}

// sendMessageToNode sends a message to a target node via NATS.
func (al *AgentLoop) sendMessageToNode(
	ctx context.Context,
	targetNodeID, content, channel, chatID, senderID string,
) (string, error) {
	nc := al.swarmDiscovery.NATSConn()
	if nc == nil {
		return "", fmt.Errorf("NATS connection not available")
	}

	subject := swarm.SubjectNodeMsg + "." + targetNodeID + ".msg"

	payload := map[string]any{
		"content":   content,
		"channel":   channel,
		"chat_id":   chatID,
		"sender_id": senderID,
		"from_node": al.swarmDiscovery.LocalNode().ID,
		"timestamp": time.Now().UnixNano(),
	}

	// Add signature if signer is configured
	if al.swarmSigner != nil {
		signature, err := al.swarmSigner.Sign(payload)
		if err != nil {
			return "", fmt.Errorf("failed to sign message: %w", err)
		}
		payload["signature"] = signature
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal message: %w", err)
	}

	reply, err := nc.RequestWithContext(ctx, subject, data)
	if err != nil {
		return "", fmt.Errorf("failed to send message to node %s: %w", targetNodeID, err)
	}

	var response map[string]any
	if err := json.Unmarshal(reply.Data, &response); err != nil {
		return string(reply.Data), nil //nolint:nilerr // Fallback to raw response on parse failure
	}

	if errMsg, ok := response["error"].(string); ok && errMsg != "" {
		return "", fmt.Errorf("node %s error: %s", targetNodeID, errMsg)
	}

	if resp, ok := response["response"].(string); ok {
		return resp, nil
	}

	return string(reply.Data), nil
}

// sendActionToNode sends an action-based request to a target node via NATS.
// Used for fast operations like 'status' that don't require LLM processing.
func (al *AgentLoop) sendActionToNode(
	ctx context.Context,
	targetNodeID, action string,
) (string, error) {
	nc := al.swarmDiscovery.NATSConn()
	if nc == nil {
		return "", fmt.Errorf("NATS connection not available")
	}

	subject := swarm.SubjectNodeMsg + "." + targetNodeID + ".msg"

	payload := map[string]any{
		"action":    action,
		"from_node": al.swarmDiscovery.LocalNode().ID,
		"timestamp": time.Now().UnixNano(),
	}

	// Add signature if signer is configured
	if al.swarmSigner != nil {
		signature, err := al.swarmSigner.Sign(payload)
		if err != nil {
			return "", fmt.Errorf("failed to sign action: %w", err)
		}
		payload["signature"] = signature
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal action: %w", err)
	}

	reply, err := nc.RequestWithContext(ctx, subject, data)
	if err != nil {
		return "", fmt.Errorf("failed to send action to node %s: %w", targetNodeID, err)
	}

	var response map[string]any
	if err := json.Unmarshal(reply.Data, &response); err != nil {
		return string(reply.Data), nil //nolint:nilerr // Fallback to raw response on parse failure
	}

	if errMsg, ok := response["error"].(string); ok && errMsg != "" {
		return "", fmt.Errorf("node error: %s", errMsg)
	}

	if resp, ok := response["response"].(string); ok {
		return resp, nil
	}

	return string(reply.Data), nil
}

// registerSwarmTools registers swarm-related tools (handoff, routing).
func (al *AgentLoop) registerSwarmTools(discovery swarm.Discovery) {
	localNodeID := discovery.LocalNode().ID

	swarmTool := tools.NewSwarmTool(discovery, al.swarmLoad, localNodeID)
	swarmTool.SetSendMessageFn(al.sendMessageToNode)
	al.RegisterTool(swarmTool)

	nodesTool := tools.NewSwarmNodesTool(discovery, al.swarmLoad, localNodeID)
	al.RegisterTool(nodesTool)

	routeTool := tools.NewSwarmRouteTool(discovery, al.swarmHandoff, localNodeID)
	routeTool.SetSendMessageFn(al.sendMessageToNode)
	routeTool.SetSendActionFn(al.sendActionToNode)
	al.RegisterTool(routeTool)

	// Register batch query tool for parallel node queries
	batchTool := tools.NewSwarmBatchTool(discovery, localNodeID)
	batchTool.SetSendActionFn(al.sendActionToNode)
	al.RegisterTool(batchTool)
}

// subscribeSwarmEvents subscribes to node join/leave events.
func (al *AgentLoop) subscribeSwarmEvents(discovery swarm.Discovery) {
	_ = discovery.Subscribe(func(event *swarm.NodeEvent) {
		switch event.Event {
		case swarm.EventJoin:
			logger.InfoCF("swarm", "Node joined", map[string]any{"node_id": event.Node.ID})
		case swarm.EventLeave:
			logger.InfoCF("swarm", "Node left", map[string]any{"node_id": event.Node.ID})
		}
	})
}

// registerSwarmCommands registers swarm slash commands on the channel command registry.
func (al *AgentLoop) registerSwarmCommands() {
	if al.commandRegistry == nil {
		return
	}

	// If swarm init failed, register a /nodes command that shows the error.
	if al.swarmDiscovery == nil {
		if al.swarmInitError != nil {
			al.commandRegistry.Register("nodes", "List swarm cluster nodes", func(
				ctx context.Context, args string, msg bus.InboundMessage,
			) (string, error) {
				return fmt.Sprintf("Swarm mode failed to initialize: %s", al.swarmInitError), nil
			})
		}
		return
	}

	localNodeID := al.swarmDiscovery.LocalNode().ID

	al.commandRegistry.Register("nodes", "List swarm cluster nodes", func(
		ctx context.Context, args string, msg bus.InboundMessage,
	) (string, error) {
		verbose := strings.Contains(args, "verbose") || strings.Contains(args, "-v")
		return tools.FormatClusterStatus(al.swarmDiscovery, al.swarmLoad, localNodeID, verbose), nil
	})
}

// convertToSwarmConfig converts the config.SwarmConfig to swarm.Config.
func (al *AgentLoop) convertToSwarmConfig(cfg config.SwarmConfig) *swarm.Config {
	applyDurationDefault := func(val int, defaultVal time.Duration) time.Duration {
		if val <= 0 {
			return defaultVal
		}
		return time.Duration(val) * time.Second
	}

	return &swarm.Config{
		Enabled: cfg.Enabled,
		NodeID:  cfg.NodeID,
		Discovery: swarm.DiscoveryConfig{
			NATSURL:       cfg.Discovery.NATSURL,
			NATSCredsFile: cfg.Discovery.NATSCredsFile,
			NATSTLSCert:   cfg.Discovery.NATSTLSCert,
			NATSTLSKey:    cfg.Discovery.NATSTLSKey,
			NATSTLSCACert: cfg.Discovery.NATSTLSCACert,
			HeartbeatInterval: swarm.Duration{
				Duration: applyDurationDefault(cfg.Discovery.HeartbeatInterval, swarm.DefaultHeartbeatInterval),
			},
			MemberTTL: swarm.Duration{
				Duration: applyDurationDefault(cfg.Discovery.MemberTTL, swarm.DefaultMemberTTL),
			},
			SubjectPrefix: cfg.Discovery.SubjectPrefix,
			NodeTimeout: swarm.Duration{
				Duration: applyDurationDefault(cfg.Discovery.NodeTimeout, swarm.DefaultNodeTimeout),
			},
			DeadNodeTimeout: swarm.Duration{
				Duration: applyDurationDefault(cfg.Discovery.DeadNodeTimeout, swarm.DefaultDeadNodeTimeout),
			},
		},
		Handoff: swarm.HandoffConfig{
			Enabled:       cfg.Handoff.Enabled,
			LoadThreshold: cfg.Handoff.LoadThreshold,
			Timeout: swarm.Duration{
				Duration: applyDurationDefault(cfg.Handoff.Timeout, swarm.DefaultHandoffTimeout),
			},
			MaxRetries: cfg.Handoff.MaxRetries,
			RetryDelay: swarm.Duration{
				Duration: applyDurationDefault(cfg.Handoff.RetryDelay, swarm.DefaultHandoffRetryDelay),
			},
			RequestTimeout: swarm.Duration{
				Duration: applyDurationDefault(cfg.Handoff.RequestTimeout, swarm.DefaultHandoffRequestTimeout),
			},
		},
		LoadMonitor: swarm.LoadMonitorConfig{
			Enabled: cfg.LoadMonitor.Enabled,
			Interval: swarm.Duration{
				Duration: applyDurationDefault(cfg.LoadMonitor.Interval, swarm.DefaultLoadSampleInterval),
			},
			SampleSize:    cfg.LoadMonitor.SampleSize,
			CPUWeight:     cfg.LoadMonitor.CPUWeight,
			MemoryWeight:  cfg.LoadMonitor.MemoryWeight,
			SessionWeight: cfg.LoadMonitor.SessionWeight,
		},
		LeaderElection: swarm.LeaderElectionConfig{
			Enabled: cfg.LeaderElection.Enabled,
			LockTTL: swarm.Duration{
				Duration: applyDurationDefault(cfg.LeaderElection.LockTTL, swarm.DefaultLeaderLockTTL),
			},
			RenewalInterval: swarm.Duration{
				Duration: applyDurationDefault(cfg.LeaderElection.RenewalInterval, swarm.DefaultLeaderRenewalInterval),
			},
		},
	}
}

// shouldHandoff determines if the current request should be handed off to another node.
func (al *AgentLoop) shouldHandoff(agent *AgentInstance, opts processOptions) bool {
	if !al.swarmEnabled || al.swarmHandoff == nil {
		return false
	}

	if al.swarmLoad != nil && al.swarmLoad.ShouldOffload() {
		logger.InfoCF("swarm", "Load threshold exceeded, considering handoff", map[string]any{
			"load_score": al.swarmLoad.GetCurrentLoad().Score,
		})
		return true
	}

	return false
}

// UpdateSwarmLoad updates the current load score reported to the swarm.
func (al *AgentLoop) UpdateSwarmLoad(sessionCount int) {
	if al.swarmLoad != nil {
		al.swarmLoad.SetSessionCount(sessionCount)
	}
}

// IncrementSwarmSessions increments the active session count.
func (al *AgentLoop) IncrementSwarmSessions() {
	if al.swarmLoad != nil {
		al.swarmLoad.IncrementSessions()
	}
}

// DecrementSwarmSessions decrements the active session count.
func (al *AgentLoop) DecrementSwarmSessions() {
	if al.swarmLoad != nil {
		al.swarmLoad.DecrementSessions()
	}
}

// GetSwarmStatus returns the current swarm status with metrics and component health.
func (al *AgentLoop) GetSwarmStatus() map[string]any {
	if !al.swarmEnabled {
		if al.swarmInitError != nil {
			return map[string]any{
				"enabled": false,
				"error":   al.swarmInitError.Error(),
			}
		}
		return map[string]any{"enabled": false}
	}

	status := map[string]any{
		"enabled": true,
		"node_id": al.swarmDiscovery.LocalNode().ID,
		"handoff": al.cfg.Swarm.Handoff.Enabled,
	}

	// Component health status
	health := map[string]bool{
		"discovery":       al.swarmDiscovery != nil,
		"handoff":         al.swarmHandoff != nil,
		"load_monitor":    al.swarmLoad != nil,
		"leader_election": al.swarmLeaderElection != nil,
	}
	if al.swarmHandoff != nil {
		for k, v := range al.swarmHandoff.ComponentHealth() {
			health[k] = v
		}
		circuitBreakerStatus := al.swarmHandoff.GetCircuitBreakerStatus()
		if len(circuitBreakerStatus) > 0 {
			status["circuit_breaker"] = circuitBreakerStatus
		}
	}
	status["components"] = health

	if al.swarmLoad != nil {
		metrics := al.swarmLoad.GetCurrentLoad()
		status["load"] = map[string]any{
			"score":           metrics.Score,
			"cpu_usage":       metrics.CPUUsage,
			"memory_usage":    metrics.MemoryUsage,
			"active_sessions": metrics.ActiveSessions,
			"goroutines":      metrics.Goroutines,
			"trend":           al.swarmLoad.GetTrend(),
		}
	}

	if al.swarmDiscovery != nil {
		members := al.swarmDiscovery.Members()
		status["members"] = len(members)
	}

	if al.swarmHandoff != nil {
		status["handoff_metrics"] = al.swarmHandoff.GetMetrics().Snapshot()
	}

	return status
}

// ShutdownSwarm gracefully shuts down the swarm components.
func (al *AgentLoop) ShutdownSwarm() error {
	if !al.swarmEnabled {
		return nil
	}

	var errs []string

	if al.swarmLoad != nil {
		al.swarmLoad.Stop()
	}

	if al.swarmLeaderElection != nil {
		al.swarmLeaderElection.Stop()
	}

	if al.swarmHandoff != nil {
		if err := al.swarmHandoff.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("handoff: %v", err))
		}
	}

	if al.swarmDiscovery != nil {
		if err := al.swarmDiscovery.Stop(); err != nil {
			errs = append(errs, fmt.Sprintf("discovery: %v", err))
		}
	}

	al.swarmEnabled = false

	if len(errs) > 0 {
		return fmt.Errorf("swarm shutdown errors: %s", strings.Join(errs, ", "))
	}
	return nil
}

// initiateSwarmHandoff initiates a handoff to another node.
func (al *AgentLoop) initiateSwarmHandoff(
	ctx context.Context,
	agent *AgentInstance,
	sessionKey string,
	msg bus.InboundMessage,
) (*swarm.HandoffResponse, error) {
	if al.swarmHandoff == nil {
		return nil, swarm.ErrDiscoveryDisabled
	}

	// Build session history for handoff
	sessionMessages := make([]swarm.SessionMessage, 0)
	history := agent.Sessions.GetHistory(sessionKey)

	for _, m := range history {
		if m.Role == "user" || m.Role == "assistant" {
			sessionMessages = append(sessionMessages, swarm.SessionMessage{
				Role:    m.Role,
				Content: m.Content,
			})
		}
	}

	// Create handoff request
	req := &swarm.HandoffRequest{
		Reason:          swarm.ReasonOverloaded,
		SessionKey:      sessionKey,
		SessionMessages: sessionMessages,
		Context: map[string]any{
			"channel":  msg.Channel,
			"chat_id":  msg.ChatID,
			"sender":   msg.SenderID,
			"agent_id": agent.ID,
		},
		Metadata: map[string]string{
			"original_channel": msg.Channel,
			"original_chat_id": msg.ChatID,
		},
	}

	logger.InfoCF("swarm", "Initiating handoff", map[string]any{
		"session_key": sessionKey,
		"reason":      req.Reason,
		"history_len": len(sessionMessages),
	})

	resp, err := al.swarmHandoff.InitiateHandoff(ctx, req)

	if resp != nil {
		logger.InfoCF("swarm", "Handoff response received", map[string]any{
			"accepted": resp.Accepted,
			"node_id":  resp.NodeID,
			"state":    resp.State,
		})
	}

	return resp, err
}
