// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/sipeed/picoclaw/pkg/kv"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// DiscoveryService implements node discovery using NATS JetStream KeyValue store.
// This is the sole discovery implementation — all swarm communication goes through NATS.
type DiscoveryService struct {
	config       *Config
	localNode    *NodeInfo
	membership   *MembershipManager
	eventHandler *EventDispatcher
	nc           *nats.Conn
	js           nats.JetStreamContext
	buckets      *BucketSet

	detailedStatus *NodeDetailedStatus
	loadMonitor    *LoadMonitor

	mu       sync.RWMutex
	running  bool
	stopChan chan struct{}
	once     sync.Once

	seqNum      uint64
	startTime   time.Time
	todoList    []string
	activeTasks int
	lastError   string
	version     string
}

// generateStableNodeID generates a stable node ID that persists across restarts.
// It uses hostname as the base, optionally loading a previously generated ID from a file.
func generateStableNodeID() string {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "picoclaw"
	}

	// Try to load a previously generated stable ID from a file
	// This ensures the same ID is used across restarts even if hostname changes
	dataDir := os.Getenv("PICOCLAW_DATA_DIR")
	if dataDir == "" {
		// Default to current directory
		dataDir = "."
	}
	nodeIDFile := fmt.Sprintf("%s/.swarm_node_id", dataDir)

	if idBytes, err := os.ReadFile(nodeIDFile); err == nil {
		storedID := strings.TrimSpace(string(idBytes))
		if storedID != "" {
			logger.InfoCF("swarm", "Loaded stable node ID from file", map[string]any{"node_id": storedID})
			return storedID
		}
	}

	// Generate a stable ID based on hostname
	// Using just hostname ensures stability across restarts
	// For multiple instances on the same host, users should explicitly set PICOCLAW_SWARM_NODE_ID
	stableID := hostname

	// Optionally persist this ID for future use
	_ = os.WriteFile(nodeIDFile, []byte(stableID), 0o644)

	logger.InfoCF("swarm", "Generated stable node ID", map[string]any{
		"node_id":      stableID,
		"node_id_file": nodeIDFile,
		"env_override": "PICOCLAW_SWARM_NODE_ID",
	})
	return stableID
}

// getLocalIP returns the local IP address.
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return ""
}

// NewDiscoveryService creates a new NATS-based discovery service.
func NewDiscoveryService(cfg *Config) (*DiscoveryService, error) {
	if cfg.NodeID == "" {
		cfg.NodeID = generateStableNodeID()
		logger.WarnCF(
			"swarm",
			"NodeID not configured, using auto-generated stable ID. For production, explicitly set PICOCLAW_SWARM_NODE_ID environment variable",
			map[string]any{
				"node_id": cfg.NodeID,
			},
		)
	}

	// Determine advertise address
	advAddr := getLocalIP()
	if advAddr == "" {
		advAddr = "127.0.0.1"
	}

	localNode := &NodeInfo{
		ID:        cfg.NodeID,
		Addr:      advAddr,
		Port:      0, // No direct RPC port; all communication via NATS
		AgentCaps: make(map[string]string),
		LoadScore: 0,
		Labels:    make(map[string]string),
		Timestamp: time.Now().UnixNano(),
		Version:   "1.0.0",
	}

	ds := &DiscoveryService{
		config:         cfg,
		localNode:      localNode,
		eventHandler:   NewEventDispatcher(),
		stopChan:       make(chan struct{}),
		detailedStatus: NewNodeDetailedStatus(),
		startTime:      time.Now(),
		todoList:       make([]string, 0),
		activeTasks:    0,
		version:        "1.0.0",
	}

	// Initialize membership manager
	ds.membership = NewMembershipManager(ds, cfg.Discovery)

	return ds, nil
}

// buildNATSOptions constructs NATS connection options from config.
func buildNATSOptions(cfg DiscoveryConfig, nodeID string) []nats.Option {
	var opts []nats.Option

	opts = append(opts, nats.Name("picoclaw-"+nodeID))

	// NKeys / JWT credentials
	if cfg.NATSCredsFile != "" {
		opts = append(opts, nats.UserCredentials(cfg.NATSCredsFile))
	}

	// mTLS client certificate
	if cfg.NATSTLSCert != "" && cfg.NATSTLSKey != "" {
		opts = append(opts, nats.ClientCert(cfg.NATSTLSCert, cfg.NATSTLSKey))
	}

	// CA certificate for server verification
	if cfg.NATSTLSCACert != "" {
		opts = append(opts, nats.RootCAs(cfg.NATSTLSCACert))
	}

	// Auto-reconnect
	opts = append(opts, nats.MaxReconnects(-1))
	opts = append(opts, nats.ReconnectWait(2*time.Second))

	return opts
}

// Start starts the NATS discovery service.
func (ds *DiscoveryService) Start() error {
	ds.mu.Lock()
	if ds.running {
		ds.mu.Unlock()
		return nil
	}
	ds.running = true
	ds.mu.Unlock()

	// Get NATS URL from config or use default
	natsURL := ds.config.Discovery.NATSURL
	if natsURL == "" {
		natsURL = DefaultNATSURL
	}

	// Build connection options (TLS, creds, reconnect)
	opts := buildNATSOptions(ds.config.Discovery, ds.config.NodeID)

	// Connect to NATS
	var err error
	ds.nc, err = nats.Connect(natsURL, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Get JetStream context
	ds.js, err = ds.nc.JetStream()
	if err != nil {
		ds.nc.Close()
		return fmt.Errorf("failed to get JetStream context: %w", err)
	}

	// Create bucket factory and initialize buckets
	factory := kv.NewNATSBucketFactory(ds.js)
	ds.buckets, err = NewBucketSet(factory)
	if err != nil {
		ds.nc.Close()
		return fmt.Errorf("failed to create buckets: %w", err)
	}

	logger.InfoCF("swarm", "NATS discovery service started", map[string]any{
		"nats_url": natsURL,
		"node_id":  ds.localNode.ID,
	})

	// Start heartbeat routine
	go ds.heartbeatLoop()

	// Start detailed status publisher
	go ds.detailedStatusLoop()

	// Start watch for other nodes
	go ds.watchNodes()

	// Start watch for detailed status from other nodes
	go ds.watchDetailedStatus()

	// Add self to membership
	ds.membership.UpdateNode(ds.localNode)

	return nil
}

// Stop stops the NATS discovery service.
func (ds *DiscoveryService) Stop() error {
	ds.once.Do(func() {
		ds.mu.Lock()
		ds.running = false
		ds.mu.Unlock()

		if ds.stopChan != nil {
			close(ds.stopChan)
		}

		// Delete our entry from members bucket
		if ds.buckets != nil && ds.localNode != nil {
			_ = ds.buckets.Members.Delete(ds.localNode.ID)
		}

		// Close buckets
		if ds.buckets != nil {
			_ = ds.buckets.Close()
		}

		if ds.nc != nil {
			ds.nc.Close()
		}
	})
	return nil
}

// LocalNode returns the local node info.
func (ds *DiscoveryService) LocalNode() *NodeInfo {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return ds.localNode
}

// UpdateLocalInfo updates the local node's information.
func (ds *DiscoveryService) UpdateLocalInfo(info *NodeInfo) {
	ds.mu.Lock()
	ds.localNode = info
	ds.localNode.Timestamp = time.Now().UnixNano()
	ds.seqNum++
	info = ds.localNode
	ds.mu.Unlock()

	ds.membership.UpdateNode(info)
	ds.publishNode()
}

// UpdateLoad updates the local node's load score.
func (ds *DiscoveryService) UpdateLoad(score float64) {
	ds.mu.Lock()
	ds.localNode.LoadScore = score
	ds.localNode.Timestamp = time.Now().UnixNano()
	ds.seqNum++
	info := ds.localNode
	ds.mu.Unlock()

	ds.membership.UpdateNode(info)
	ds.publishNode()
}

// UpdateCapabilities updates the local node's agent capabilities.
func (ds *DiscoveryService) UpdateCapabilities(caps map[string]string) {
	ds.mu.Lock()
	ds.localNode.AgentCaps = caps
	ds.localNode.Timestamp = time.Now().UnixNano()
	ds.seqNum++
	info := ds.localNode
	ds.mu.Unlock()

	ds.membership.UpdateNode(info)
	ds.publishNode()
}

// Members returns all known members.
func (ds *DiscoveryService) Members() []*NodeWithState {
	return ds.membership.GetMembers()
}

// GetNode returns a node by ID.
func (ds *DiscoveryService) GetNode(nodeID string) (*NodeWithState, bool) {
	return ds.membership.GetNode(nodeID)
}

// Subscribe registers a handler for node events.
func (ds *DiscoveryService) Subscribe(handler EventHandler) EventHandlerID {
	return ds.eventHandler.Subscribe(handler)
}

// Unsubscribe removes a node event handler.
func (ds *DiscoveryService) Unsubscribe(id EventHandlerID) {
	ds.eventHandler.Unsubscribe(id)
}

// PublishLocalUpdate publishes local node state.
func (ds *DiscoveryService) PublishLocalUpdate() {
	ds.publishNode()
}

// DispatchEvent dispatches a node event to registered handlers.
func (ds *DiscoveryService) DispatchEvent(event *NodeEvent) {
	ds.eventHandler.Dispatch(event)
}

// GetMembershipManager returns the membership manager.
func (ds *DiscoveryService) GetMembershipManager() *MembershipManager {
	return ds.membership
}

// NATSConn returns the underlying NATS connection.
func (ds *DiscoveryService) NATSConn() *nats.Conn {
	return ds.nc
}

// JetStream returns the JetStream context.
func (ds *DiscoveryService) JetStream() nats.JetStreamContext {
	return ds.js
}

// Buckets returns the bucket set.
func (ds *DiscoveryService) Buckets() *BucketSet {
	return ds.buckets
}

// Helper methods for todo list, active tasks, and error tracking.

// getTodoList returns the current todo list.
func (ds *DiscoveryService) getTodoList() []string {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return ds.todoList
}

// SetTodoList sets the todo list for detailed status reporting.
func (ds *DiscoveryService) SetTodoList(todos []string) {
	ds.mu.Lock()
	ds.todoList = todos
	ds.mu.Unlock()
}

// AddTodo adds a todo item to the todo list.
func (ds *DiscoveryService) AddTodo(todo string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.todoList = append(ds.todoList, todo)
}

// RemoveTodo removes a todo item from the todo list.
func (ds *DiscoveryService) RemoveTodo(todo string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	for i, t := range ds.todoList {
		if t == todo {
			ds.todoList = append(ds.todoList[:i], ds.todoList[i+1:]...)
			break
		}
	}
}

// ClearTodoList clears the todo list.
func (ds *DiscoveryService) ClearTodoList() {
	ds.mu.Lock()
	ds.todoList = make([]string, 0)
	ds.mu.Unlock()
}

// getActiveTasks returns the current active tasks count.
func (ds *DiscoveryService) getActiveTasks() int {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return ds.activeTasks
}

// SetActiveTasks sets the active tasks count.
func (ds *DiscoveryService) SetActiveTasks(count int) {
	ds.mu.Lock()
	ds.activeTasks = count
	ds.mu.Unlock()
}

// IncrementActiveTasks increments the active tasks count.
func (ds *DiscoveryService) IncrementActiveTasks() {
	ds.mu.Lock()
	ds.activeTasks++
	ds.mu.Unlock()
}

// DecrementActiveTasks decrements the active tasks count.
func (ds *DiscoveryService) DecrementActiveTasks() {
	ds.mu.Lock()
	if ds.activeTasks > 0 {
		ds.activeTasks--
	}
	ds.mu.Unlock()
}

// getLastError returns the last error.
func (ds *DiscoveryService) getLastError() string {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return ds.lastError
}

// SetLastError sets the last error for detailed status reporting.
func (ds *DiscoveryService) SetLastError(err string) {
	ds.mu.Lock()
	ds.lastError = err
	ds.mu.Unlock()
}
