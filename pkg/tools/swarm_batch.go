package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/swarm"
)

// SwarmBatchTool queries multiple nodes in parallel.
type SwarmBatchTool struct {
	discovery    swarm.Discovery
	localID      string
	sendActionFn func(ctx context.Context, targetNodeID, action string) (string, error)
}

// NewSwarmBatchTool creates a new swarm batch query tool.
func NewSwarmBatchTool(
	discovery swarm.Discovery,
	localID string,
) *SwarmBatchTool {
	return &SwarmBatchTool{
		discovery: discovery,
		localID:   localID,
	}
}

// SetSendActionFn sets the function to send actions to other nodes.
func (t *SwarmBatchTool) SetSendActionFn(
	fn func(ctx context.Context, targetNodeID, action string) (string, error),
) {
	t.sendActionFn = fn
}

// Name returns the tool name.
func (t *SwarmBatchTool) Name() string {
	return "swarm_batch"
}

// Description returns the tool description.
func (t *SwarmBatchTool) Description() string {
	//nolint:gosmopolitan // Intentional Chinese examples for bilingual LLM tool descriptions
	return "Broadcast query to ALL nodes in parallel (fast, <10ms). " +
		"IMPORTANT: Use this FIRST when asking about multiple nodes (e.g., '所有节点状态', 'node-01和node-02的状态'). " +
		"DO NOT use swarm_route for each node - use this ONCE to get all data. " +
		"For single node specific tasks, use swarm_route with action='message'."
}

// Parameters returns the tool parameters schema.
func (t *SwarmBatchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node_ids": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "List of node IDs to query (optional, defaults to all nodes)",
			},
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"status", "message"},
				"default":     "status",
				"description": "Action type: 'status' (default, fast) returns structured data; 'message' requires a message parameter",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Message to send (only for action='message')",
			},
			"verbose": map[string]any{
				"type":        "boolean",
				"description": "Include detailed information",
			},
		},
	}
}

// Execute executes the batch query in parallel.
func (t *SwarmBatchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.discovery == nil {
		return ErrorResult("Swarm mode is not enabled")
	}

	action, _ := args["action"].(string)
	if action == "" {
		action = "status"
	}
	message, _ := args["message"].(string)
	verbose := false
	if v, ok := args["verbose"].(bool); ok {
		verbose = v
	}

	// Get target nodes
	var targetIDs []string
	if nodeIDsRaw, ok := args["node_ids"].([]any); ok {
		for _, id := range nodeIDsRaw {
			if idStr, ok := id.(string); ok {
				targetIDs = append(targetIDs, idStr)
			}
		}
	}

	// If no nodes specified, query all nodes except local
	if len(targetIDs) == 0 {
		members := t.discovery.Members()
		for _, m := range members {
			if m.Node.ID != t.localID {
				targetIDs = append(targetIDs, m.Node.ID)
			}
		}
	}

	if len(targetIDs) == 0 {
		return ErrorResult("No nodes to query")
	}

	// For status action with detailed status available, use fast path
	if action == "status" {
		if provider, ok := t.discovery.(swarm.DetailedStatusProvider); ok {
			if statusRegistry := provider.GetDetailedStatus(); statusRegistry != nil {
				return t.executeFastStatusQuery(statusRegistry, targetIDs, verbose)
			}
		}
	}

	// Otherwise, use parallel action queries
	return t.executeParallelQueries(ctx, targetIDs, action, message)
}

// executeFastStatusQuery queries all nodes from local status cache (fastest).
func (t *SwarmBatchTool) executeFastStatusQuery(
	statusRegistry *swarm.NodeDetailedStatus,
	nodeIDs []string,
	verbose bool,
) *ToolResult {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 Batch Status Query (%d nodes):\n\n", len(nodeIDs)))

	for _, nodeID := range nodeIDs {
		status, ok := statusRegistry.Get(nodeID)
		if !ok {
			sb.WriteString(fmt.Sprintf("**%s**: ❌ No status data available\n", nodeID))
			continue
		}

		sb.WriteString(fmt.Sprintf("**%s**", nodeID))

		// Format uptime
		uptime := fmt.Sprintf("%.1fm", float64(status.Uptime)/60)
		sb.WriteString(fmt.Sprintf(" (up: %s)", uptime))

		if verbose {
			sb.WriteString(fmt.Sprintf("\n   Address: %s:%d\n", status.Addr, status.HTTPPort))
			sb.WriteString(fmt.Sprintf("   Load: %.1f%% | CPU: %.1f%% | Mem: %.1f%% (%.1f MB)\n",
				status.LoadScore*100, status.CPUUsage*100, status.MemoryUsage*100,
				float64(status.MemoryBytes)/(1024*1024)))
			sb.WriteString(fmt.Sprintf("   Sessions: %d | Goroutines: %d\n",
				status.ActiveSessions, status.Goroutines))

			if len(status.TodoList) > 0 {
				sb.WriteString("   Todo:\n")
				for i, todo := range status.TodoList {
					if i >= 3 {
						sb.WriteString(fmt.Sprintf("     ... and %d more\n", len(status.TodoList)-3))
						break
					}
					sb.WriteString(fmt.Sprintf("     - %s\n", todo))
				}
			}

			if status.ActiveTasks > 0 {
				sb.WriteString(fmt.Sprintf("   Active Tasks: %d\n", status.ActiveTasks))
			}
		} else {
			sb.WriteString(fmt.Sprintf(" — Load: %.0f%% | Mem: %.0f%%",
				status.LoadScore*100, status.MemoryUsage*100))

			if len(status.TodoList) > 0 {
				sb.WriteString(fmt.Sprintf(" | Todo: %d", len(status.TodoList)))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	return &ToolResult{
		ForLLM:  sb.String(),
		ForUser: sb.String(),
		IsError: false,
	}
}

// executeParallelQueries sends parallel requests to all nodes.
func (t *SwarmBatchTool) executeParallelQueries(
	ctx context.Context,
	nodeIDs []string,
	action, message string,
) *ToolResult {
	if t.sendActionFn == nil {
		return ErrorResult("Inter-node communication not available")
	}

	var wg sync.WaitGroup
	results := make(map[string]string)
	errors := make(map[string]string)
	var mu sync.Mutex

	members := t.discovery.Members()
	nodeMap := make(map[string]*swarm.NodeWithState)
	for _, m := range members {
		nodeMap[m.Node.ID] = m
	}

	for _, nodeID := range nodeIDs {
		wg.Add(1)
		go func(nid string) {
			defer wg.Done()

			target := nodeMap[nid]
			if target == nil {
				mu.Lock()
				errors[nid] = "Node not found"
				mu.Unlock()
				return
			}

			if target.State.Status != swarm.NodeStatusAlive {
				mu.Lock()
				errors[nid] = fmt.Sprintf("Node not alive (status: %s)", target.State.Status)
				mu.Unlock()
				return
			}

			result, err := t.sendActionFn(ctx, nid, action)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errors[nid] = err.Error()
			} else {
				results[nid] = result
			}
		}(nodeID)
	}

	wg.Wait()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 Batch Query Results (%d nodes):\n\n", len(nodeIDs)))

	for _, nodeID := range nodeIDs {
		sb.WriteString(fmt.Sprintf("**%s**:\n", nodeID))
		if result, ok := results[nodeID]; ok {
			sb.WriteString(result)
		} else if err, ok := errors[nodeID]; ok {
			sb.WriteString(fmt.Sprintf("❌ Error: %s\n", err))
		}
		sb.WriteString("\n")
	}

	return &ToolResult{
		ForLLM:  sb.String(),
		ForUser: sb.String(),
		IsError: len(errors) == 0,
	}
}
