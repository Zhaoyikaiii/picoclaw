package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/swarm"
)

// FormatClusterStatus returns a human-readable cluster status string.
// Used by the /nodes command handler.
// Now uses detailed status from NATS KV for fast queries without sending requests to other nodes.
func FormatClusterStatus(
	discovery swarm.Discovery,
	load *swarm.LoadMonitor,
	localID string,
	verbose bool,
) string {
	if discovery == nil {
		return "Swarm mode is not enabled."
	}

	members := discovery.Members()
	if len(members) == 0 {
		return "No nodes found in the swarm cluster."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Swarm Cluster Status (%d node%s):\n\n", len(members), plural(len(members))))

	// Sort nodes: local first, then by ID
	sortedMembers := make([]*swarm.NodeWithState, len(members))
	copy(sortedMembers, members)
	sort.Slice(sortedMembers, func(i, j int) bool {
		// Local node always comes first
		if sortedMembers[i].Node.ID == localID {
			return true
		}
		if sortedMembers[j].Node.ID == localID {
			return false
		}
		return sortedMembers[i].Node.ID < sortedMembers[j].Node.ID
	})

	// Try to get detailed status from discovery (if it supports it)
	var detailedStatuses map[string]*swarm.NodeDetailedInfo
	if provider, ok := discovery.(swarm.DetailedStatusProvider); ok {
		statusRegistry := provider.GetDetailedStatus()
		if statusRegistry != nil {
			detailedStatuses = make(map[string]*swarm.NodeDetailedInfo)
			for _, status := range statusRegistry.GetAll() {
				detailedStatuses[status.ID] = status
			}
		}
	}

	for _, m := range sortedMembers {
		node := m.Node
		state := m.State

		localMark := " "
		if node.ID == localID {
			localMark = "*"
		}

		sb.WriteString(fmt.Sprintf("%s **%s**", localMark, node.ID))

		// Check if we have detailed status from KV
		detailed := detailedStatuses[node.ID]
		if detailed != nil {
			// Use detailed status from KV (fast path)
			sb.WriteString(formatDetailedNodeStatus(detailed, verbose))
		} else {
			// Fallback to basic status
			sb.WriteString(formatBasicNodeStatus(node, state, verbose))
		}

		sb.WriteString("\n")
	}

	sb.WriteString("* = this node\n")
	sb.WriteString("\nTip: Use `@node-id: message` to route a request to a specific node.")

	return sb.String()
}

// formatDetailedNodeStatus formats node status from detailed info (from KV).
func formatDetailedNodeStatus(node *swarm.NodeDetailedInfo, verbose bool) string {
	var result strings.Builder

	// Add uptime
	uptime := time.Duration(node.Uptime) * time.Second
	result.WriteString(fmt.Sprintf(" (uptime: %s)", formatDuration(uptime)))

	if node.Status != "" {
		result.WriteString(fmt.Sprintf(" [%s]", node.Status))
	}
	result.WriteString("\n")

	if verbose {
		result.WriteString(fmt.Sprintf("   Address: %s:%d (HTTP: %d)\n", node.Addr, node.Port, node.HTTPPort))

		// Load metrics with emoji
		result.WriteString(fmt.Sprintf("   Load: %.1f%%", node.LoadScore*100))
		if node.LoadScore < 0.5 {
			result.WriteString(" 🟩")
		} else if node.LoadScore < 0.8 {
			result.WriteString(" 🟨")
		} else {
			result.WriteString(" 🟥")
		}
		result.WriteString(fmt.Sprintf(" | CPU: %.1f%% | Mem: %.1f%% (%.1f MB)\n",
			node.CPUUsage*100, node.MemoryUsage*100, float64(node.MemoryBytes)/(1024*1024)))

		// Active sessions and goroutines
		result.WriteString(fmt.Sprintf("   Sessions: %d | Goroutines: %d\n", node.ActiveSessions, node.Goroutines))

		// Todo list
		if len(node.TodoList) > 0 {
			result.WriteString("   Todo:\n")
			for i, todo := range node.TodoList {
				if i >= 5 { // Limit to 5 items
					result.WriteString(fmt.Sprintf("     ... and %d more\n", len(node.TodoList)-5))
					break
				}
				result.WriteString(fmt.Sprintf("     - %s\n", todo))
			}
		}

		// Active tasks
		if node.ActiveTasks > 0 {
			result.WriteString(fmt.Sprintf("   Active Tasks: %d\n", node.ActiveTasks))
		}

		// Disk usage
		if node.DiskUsagePercent > 0 {
			result.WriteString(fmt.Sprintf("   Disk: %.1f%% used", node.DiskUsagePercent))
			if node.DiskUsedBytes > 0 && node.DiskTotalBytes > 0 {
				result.WriteString(fmt.Sprintf(" (%.1f GB / %.1f GB)",
					float64(node.DiskUsedBytes)/(1024*1024*1024),
					float64(node.DiskTotalBytes)/(1024*1024*1024)))
			}
			result.WriteString("\n")
		}

		// Last error
		if node.LastError != "" {
			result.WriteString(fmt.Sprintf("   Last Error: %s\n", node.LastError))
		}

		// Version info
		if node.Version != "" {
			result.WriteString(fmt.Sprintf("   Version: %s (%s/%s)\n", node.Version, node.OS, node.Arch))
		}
	} else {
		// Compact format
		result.WriteString(fmt.Sprintf(" — Load: %.0f%% | Mem: %.0f%%",
			node.LoadScore*100, node.MemoryUsage*100))

		if len(node.TodoList) > 0 {
			result.WriteString(fmt.Sprintf(" | Todo: %d", len(node.TodoList)))
		}

		if node.ActiveTasks > 0 {
			result.WriteString(fmt.Sprintf(" | Tasks: %d", node.ActiveTasks))
		}

		result.WriteString("\n")
	}

	return result.String()
}

// formatBasicNodeStatus formats node status from basic info (fallback).
func formatBasicNodeStatus(node *swarm.NodeInfo, state *swarm.NodeState, verbose bool) string {
	var result strings.Builder

	if state != nil {
		writeStatusIndicator(&result, state.Status)
		result.WriteString(fmt.Sprintf(" Load: %.0f%%", node.LoadScore*100))
	}

	result.WriteString(fmt.Sprintf(" @ %s:%d\n", node.Addr, node.Port))

	if verbose && state != nil {
		result.WriteString(fmt.Sprintf("   Status: %s\n", state.Status))

		loadPercent := int(node.LoadScore * 100)
		result.WriteString(fmt.Sprintf("   Load: %.2f%% ", node.LoadScore*100))
		if loadPercent < 50 {
			result.WriteString("🟩")
		} else if loadPercent < 80 {
			result.WriteString("🟨")
		} else {
			result.WriteString("🟥")
		}
		result.WriteString("\n")

		lastSeen := time.Unix(0, state.LastSeen)
		age := time.Since(lastSeen)
		result.WriteString(fmt.Sprintf("   Last seen: %s ago\n", formatDuration(age)))

		if len(node.AgentCaps) > 0 {
			result.WriteString("   Capabilities:\n")
			for k, v := range node.AgentCaps {
				result.WriteString(fmt.Sprintf("     - %s: %s\n", k, v))
			}
		}

		if len(node.Labels) > 0 {
			result.WriteString("   Labels:\n")
			for k, v := range node.Labels {
				result.WriteString(fmt.Sprintf("     - %s: %s\n", k, v))
			}
		}
	}

	return result.String()
}

// writeStatusIndicator writes a status indicator to the builder.
func writeStatusIndicator(sb *strings.Builder, status swarm.NodeStatus) {
	switch status {
	case swarm.NodeStatusAlive:
		sb.WriteString(" 🟢")
	case swarm.NodeStatusSuspect:
		sb.WriteString(" 🟡")
	case swarm.NodeStatusDead:
		sb.WriteString(" 🔴")
	case swarm.NodeStatusLeft:
		sb.WriteString(" ⚪")
	}
}

// SwarmNodesTool returns current swarm cluster node status.
type SwarmNodesTool struct {
	discovery swarm.Discovery
	load      *swarm.LoadMonitor
	localID   string
}

// NewSwarmNodesTool creates a new swarm nodes status tool.
func NewSwarmNodesTool(
	discovery swarm.Discovery,
	load *swarm.LoadMonitor,
	localID string,
) *SwarmNodesTool {
	return &SwarmNodesTool{
		discovery: discovery,
		load:      load,
		localID:   localID,
	}
}

// Name returns the tool name.
func (t *SwarmNodesTool) Name() string {
	return "swarm_nodes"
}

// Description returns the tool description.
func (t *SwarmNodesTool) Description() string {
	//nolint:gosmopolitan // Intentional Chinese examples for bilingual LLM tool descriptions
	return "List ALL swarm cluster nodes and their detailed status in ONE call (fast, parallel, reads from local cache). " +
		"IMPORTANT: This already returns status for ALL nodes - do NOT call swarm_route for each node individually. " +
		"Use for questions like '查看所有节点状态', 'cluster status', '节点列表', etc."
}

// Parameters returns the tool parameters schema.
func (t *SwarmNodesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"verbose": map[string]any{
				"type":        "boolean",
				"description": "Whether to include detailed status for each node",
			},
		},
	}
}

// Execute executes the swarm nodes tool.
func (t *SwarmNodesTool) Execute(_ context.Context, args map[string]any) *ToolResult {
	if t.discovery == nil {
		return ErrorResult("Swarm mode is not enabled")
	}

	verbose := false
	if rawVerbose, ok := args["verbose"]; ok {
		switch v := rawVerbose.(type) {
		case bool:
			verbose = v
		case string:
			lower := strings.ToLower(strings.TrimSpace(v))
			verbose = lower == "true" || lower == "1" || lower == "yes" || lower == "y"
		}
	}

	status := FormatClusterStatus(t.discovery, t.load, t.localID, verbose)
	return &ToolResult{
		ForLLM:  status,
		ForUser: status,
		IsError: false,
	}
}

// SwarmTool provides a generalized swarm entrypoint for status/query and routing.
type SwarmTool struct {
	discovery     swarm.Discovery
	load          *swarm.LoadMonitor
	localID       string
	sendMessageFn func(ctx context.Context, targetNodeID, content, channel, chatID, senderID string) (string, error)
}

// NewSwarmTool creates a new generalized swarm tool.
func NewSwarmTool(
	discovery swarm.Discovery,
	load *swarm.LoadMonitor,
	localID string,
) *SwarmTool {
	return &SwarmTool{
		discovery: discovery,
		load:      load,
		localID:   localID,
	}
}

// SetSendMessageFn sets the function used to send messages to other nodes.
func (t *SwarmTool) SetSendMessageFn(
	fn func(ctx context.Context, targetNodeID, content, channel, chatID, senderID string) (string, error),
) {
	t.sendMessageFn = fn
}

// Name returns the tool name.
func (t *SwarmTool) Name() string {
	return "swarm_tool"
}

// Description returns the tool description.
func (t *SwarmTool) Description() string {
	return "General swarm tool: list swarm nodes/status and route a message to a specific node."
}

// Parameters returns the tool parameters schema.
func (t *SwarmTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list_nodes", "route_message"},
				"description": "Operation type: list_nodes to query node status, route_message to send to another node",
			},
			"verbose": map[string]any{
				"type":        "boolean",
				"description": "When action is list_nodes, include detailed fields",
			},
			"node_id": map[string]any{
				"type":        "string",
				"description": "When action is route_message, target node ID",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "When action is route_message, message content to send",
			},
			"channel": map[string]any{
				"type":        "string",
				"description": "Source channel identifier for routing context (optional, auto-filled from context when available)",
			},
			"chat_id": map[string]any{
				"type":        "string",
				"description": "Source chat/conversation ID for continuity (optional, auto-filled from context when available)",
			},
			"sender_id": map[string]any{
				"type":        "string",
				"description": "Sender identity for audit and reply routing (optional, auto-filled from context when available)",
			},
			"trace_id": map[string]any{
				"type":        "string",
				"description": "Distributed trace ID for cross-node observability (optional)",
			},
		},
		"required": []string{"action"},
		"oneOf": []any{
			map[string]any{
				"properties": map[string]any{
					"action": map[string]any{"const": "list_nodes"},
				},
			},
			map[string]any{
				"properties": map[string]any{
					"action": map[string]any{"const": "route_message"},
				},
				"required": []string{"node_id", "message"},
			},
		},
	}
}

// Execute executes the generalized swarm tool.
func (t *SwarmTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.discovery == nil {
		return ErrorResult("Swarm mode is not enabled")
	}

	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))

	// Fallback inference when model omits action.
	if action == "" {
		if nodeID, _ := args["node_id"].(string); strings.TrimSpace(nodeID) != "" {
			action = "route_message"
		} else {
			action = "list_nodes"
		}
	}

	switch action {
	case "list_nodes":
		verbose := false
		if rawVerbose, ok := args["verbose"]; ok {
			switch v := rawVerbose.(type) {
			case bool:
				verbose = v
			case string:
				lower := strings.ToLower(strings.TrimSpace(v))
				verbose = lower == "true" || lower == "1" || lower == "yes" || lower == "y"
			}
		}
		status := FormatClusterStatus(t.discovery, t.load, t.localID, verbose)
		return &ToolResult{
			ForLLM:  status,
			ForUser: status,
			IsError: false,
		}

	case "route_message":
		nodeID, _ := args["node_id"].(string)
		nodeID = strings.TrimSpace(nodeID)
		message, _ := args["message"].(string)

		if nodeID == "" {
			return ErrorResult("node_id is required when action=route_message")
		}
		if message == "" {
			return ErrorResult("message is required when action=route_message")
		}
		if nodeID == t.localID {
			return &ToolResult{
				ForLLM: fmt.Sprintf(
					"Target node %s is this node. Process locally instead of routing.",
					nodeID,
				),
				//nolint:gosmopolitan // Intentional Chinese user-facing message
				ForUser: fmt.Sprintf("目标节点 %s 就是当前节点，已建议本地处理。", nodeID),
				IsError: false,
			}
		}
		if t.sendMessageFn == nil {
			return ErrorResult("inter-node messaging is not available")
		}

		// Extract optional context parameters for cross-node traceability.
		channel, _ := args["channel"].(string)
		chatID, _ := args["chat_id"].(string)
		senderID, _ := args["sender_id"].(string)

		response, err := t.sendMessageFn(ctx, nodeID, message, channel, chatID, senderID)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to send message to node %s: %v", nodeID, err))
		}
		return &ToolResult{
			ForLLM:  response,
			ForUser: response,
			IsError: false,
		}
	default:
		return ErrorResult("invalid action, supported: list_nodes, route_message")
	}
}

// SwarmRouteTool routes a request to a specific node in the swarm.
type SwarmRouteTool struct {
	discovery     swarm.Discovery
	handoff       *swarm.HandoffCoordinator
	localID       string
	sendMessageFn func(ctx context.Context, targetNodeID, content, channel, chatID, senderID string) (string, error)
	sendActionFn  func(ctx context.Context, targetNodeID, action string) (string, error)
}

// NewSwarmRouteTool creates a new swarm route tool.
func NewSwarmRouteTool(
	discovery swarm.Discovery,
	handoff *swarm.HandoffCoordinator,
	localID string,
) *SwarmRouteTool {
	return &SwarmRouteTool{
		discovery: discovery,
		handoff:   handoff,
		localID:   localID,
	}
}

// SetSendMessageFn sets the function to send messages to other nodes.
func (t *SwarmRouteTool) SetSendMessageFn(
	fn func(ctx context.Context, targetNodeID, content, channel, chatID, senderID string) (string, error),
) {
	t.sendMessageFn = fn
}

// SetSendActionFn sets the function to send actions to other nodes.
func (t *SwarmRouteTool) SetSendActionFn(
	fn func(ctx context.Context, targetNodeID, action string) (string, error),
) {
	t.sendActionFn = fn
}

// Name returns the tool name.
func (t *SwarmRouteTool) Name() string {
	return "swarm_route"
}

// Description returns the tool description.
func (t *SwarmRouteTool) Description() string {
	return "Send a request to a SINGLE node. " +
		"WARNING: Use swarm_batch or swarm_nodes FIRST for multi-node queries - they are MUCH FASTER. " +
		"Only use this when you need ONE specific node to perform a task. " +
		"For status, use action='status' (<10ms); for LLM tasks, use action='message' with a message."
}

// Parameters returns the tool parameters schema.
func (t *SwarmRouteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node_id": map[string]any{
				"type":        "string",
				"description": "The target node ID to route the request to",
			},
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"status", "message"},
				"default":     "status",
				"description": "Action type: 'status' (default, fast, <10ms) returns structured data; 'message' sends to LLM for processing (slow, ~60s+)",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "The message to send to the target node (only for action='message')",
			},
			"channel": map[string]any{
				"type":        "string",
				"description": "Source channel identifier for routing context (optional)",
			},
			"chat_id": map[string]any{
				"type":        "string",
				"description": "Source chat/conversation ID for continuity (optional)",
			},
			"sender_id": map[string]any{
				"type":        "string",
				"description": "Sender identity for audit and reply routing (optional)",
			},
			"trace_id": map[string]any{
				"type":        "string",
				"description": "Distributed trace ID for cross-node observability (optional)",
			},
		},
		"required": []string{"node_id"},
		"oneOf": []any{
			map[string]any{
				"properties": map[string]any{
					"action": map[string]any{"const": "status"},
				},
			},
			map[string]any{
				"properties": map[string]any{
					"action": map[string]any{"const": "message"},
				},
				"required": []string{"message"},
			},
		},
	}
}

// Execute executes the swarm route tool.
func (t *SwarmRouteTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.discovery == nil {
		return ErrorResult("Swarm mode is not enabled")
	}

	nodeID, _ := args["node_id"].(string)
	action, _ := args["action"].(string)
	message, _ := args["message"].(string)

	// Smart action inference:
	// - If action is explicitly set, use it
	// - If message is provided, use "message"
	// - Otherwise, default to "status" (fast path)
	if action == "" {
		if message != "" {
			action = "message"
		} else {
			action = "status"
		}
	}

	if nodeID == "" {
		return ErrorResult("node_id is required")
	}

	// For "message" action, content is required
	if action == "message" && message == "" {
		return ErrorResult(
			"message is required when action='message'. For status queries, use action='status' instead.",
		)
	}

	// Check if target is this node
	if nodeID == t.localID {
		return &ToolResult{
			ForLLM:  fmt.Sprintf("Target node %s is this node. Processing locally.", nodeID),
			ForUser: fmt.Sprintf("This request is already on node %s.", nodeID),
			IsError: false,
		}
	}

	// Find target node
	members := t.discovery.Members()
	var target *swarm.NodeWithState
	for _, m := range members {
		if m.Node.ID == nodeID {
			target = m
			break
		}
	}

	if target == nil {
		return ErrorResult(fmt.Sprintf("Node %s not found in cluster", nodeID))
	}

	// Check if target is available
	if target.State.Status != swarm.NodeStatusAlive {
		return ErrorResult(fmt.Sprintf("Node %s is not alive (status: %s)", nodeID, target.State.Status))
	}

	if target.Node.LoadScore > swarm.DefaultAvailableLoadThreshold {
		return ErrorResult(fmt.Sprintf("Node %s is overloaded (load: %.0f%%)", nodeID, target.Node.LoadScore*100))
	}

	// Fast path: "status" action returns structured data directly
	if action == "status" {
		return t.executeStatusRequest(ctx, nodeID, target)
	}

	if t.sendMessageFn == nil {
		return ErrorResult("inter-node messaging is not available")
	}

	// Extract optional context parameters for cross-node traceability.
	channel, _ := args["channel"].(string)
	chatID, _ := args["chat_id"].(string)
	senderID, _ := args["sender_id"].(string)

	logger.InfoCF("swarm", "Sending message to node", map[string]any{
		"target":  nodeID,
		"message": message,
	})

	response, err := t.sendMessageFn(ctx, nodeID, message, channel, chatID, senderID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to send message to node %s: %v", nodeID, err))
	}

	return &ToolResult{
		ForLLM:  response,
		ForUser: response,
		IsError: false,
	}
}

// executeStatusRequest sends a status action request to a node.
func (t *SwarmRouteTool) executeStatusRequest(
	ctx context.Context,
	nodeID string,
	target *swarm.NodeWithState,
) *ToolResult {
	if t.sendActionFn == nil {
		return ErrorResult("inter-node actions not available")
	}

	logger.InfoCF("swarm", "Requesting status from node", map[string]any{
		"target": nodeID,
	})

	response, err := t.sendActionFn(ctx, nodeID, "status")
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to get status from node %s: %v", nodeID, err))
	}

	return &ToolResult{
		ForLLM:  fmt.Sprintf("Node %s status:\n%s", nodeID, response),
		ForUser: fmt.Sprintf("Node %s status:\n%s", nodeID, response),
		IsError: false,
	}
}

// ParseNodeMention extracts node ID from a message like "@node-id: message"
func ParseNodeMention(message string) (nodeID, content string) {
	message = strings.TrimSpace(message)

	// Check for @node-id: pattern
	if strings.HasPrefix(message, "@") {
		rest := message[1:]
		idx := strings.IndexAny(rest, ": \n")
		if idx > 0 && rest[idx] == ':' {
			nodeID = rest[:idx]
			content = strings.TrimSpace(rest[idx+1:])
			return nodeID, content
		}
	}

	return "", message
}

// Helper functions

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.1fm", d.Minutes())
	}
	return fmt.Sprintf("%.1fh", d.Hours())
}
