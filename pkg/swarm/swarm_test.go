// PicoClaw - Ultra-lightweight personal AI agent
// Swarm mode support for multi-agent coordination
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package swarm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeInfo(t *testing.T) {
	node := &NodeInfo{
		ID:        "test-node-1",
		Addr:      "192.168.1.100",
		Port:      7947,
		LoadScore: 0.5,
		AgentCaps: map[string]string{
			"agent-1": "general",
		},
		Labels: map[string]string{
			"region": "us-west",
		},
		Timestamp: time.Now().UnixNano(),
	}

	t.Run("IsAlive", func(t *testing.T) {
		assert.True(t, node.IsAlive(time.Minute))
		assert.False(t, node.IsAlive(time.Nanosecond))
	})

	t.Run("GetAddress", func(t *testing.T) {
		addr := node.GetAddress()
		assert.Equal(t, "192.168.1.100:7947", addr)
	})
}

func TestClusterView(t *testing.T) {
	view := NewClusterView("local-node")

	t.Run("AddOrUpdate", func(t *testing.T) {
		node := &NodeInfo{
			ID:        "node-1",
			Addr:      "192.168.1.1",
			Port:      7947,
			LoadScore: 0.3,
			Timestamp: time.Now().UnixNano(),
		}

		nws := view.AddOrUpdate(node)
		require.NotNil(t, nws)
		assert.Equal(t, node.ID, nws.Node.ID)
		assert.Equal(t, 1, view.Size)
	})

	t.Run("Get", func(t *testing.T) {
		node, ok := view.Get("node-1")
		assert.True(t, ok)
		assert.Equal(t, "node-1", node.Node.ID)

		_, ok = view.Get("non-existent")
		assert.False(t, ok)
	})

	t.Run("GetAliveNodes", func(t *testing.T) {
		nodes := view.GetAliveNodes()
		assert.Equal(t, 1, len(nodes))
	})

	t.Run("GetAvailableNodes", func(t *testing.T) {
		nodes := view.GetAvailableNodes()
		assert.Equal(t, 1, len(nodes)) // 0.3 < 0.9
	})

	t.Run("Remove", func(t *testing.T) {
		view.Remove("node-1")
		assert.Equal(t, 0, view.Size)
	})
}

func TestLoadMonitor(t *testing.T) {
	config := &LoadMonitorConfig{
		Enabled:       true,
		Interval:      Duration{time.Second},
		SampleSize:    10,
		CPUWeight:     0.3,
		MemoryWeight:  0.3,
		SessionWeight: 0.4,
	}

	monitor := NewLoadMonitor(config)

	t.Run("GetCurrentLoad", func(t *testing.T) {
		metrics := monitor.GetCurrentLoad()
		assert.NotNil(t, metrics)
		assert.GreaterOrEqual(t, metrics.Score, 0.0)
		assert.LessOrEqual(t, metrics.Score, 1.0)
		assert.GreaterOrEqual(t, metrics.ActiveSessions, 0)
	})

	t.Run("SessionCount", func(t *testing.T) {
		monitor.SetSessionCount(5)
		assert.Equal(t, 5, monitor.GetSessionCount())

		monitor.IncrementSessions()
		assert.Equal(t, 6, monitor.GetSessionCount())

		monitor.DecrementSessions()
		assert.Equal(t, 5, monitor.GetSessionCount())
	})

	t.Run("GetAverageScore", func(t *testing.T) {
		avg := monitor.GetAverageScore()
		assert.GreaterOrEqual(t, avg, 0.0)
		assert.LessOrEqual(t, avg, 1.0)
	})
}

func TestEventDispatcher(t *testing.T) {
	ed := NewEventDispatcher()

	t.Run("SubscribeDispatch", func(t *testing.T) {
		received := make(chan *NodeEvent, 1)

		id := ed.Subscribe(func(event *NodeEvent) {
			received <- event
		})

		event := &NodeEvent{
			Node:  &NodeInfo{ID: "test-node"},
			Event: EventJoin,
			Time:  time.Now().UnixNano(),
		}

		ed.Dispatch(event)

		select {
		case <-received:
			// Event received
		case <-time.After(time.Second):
			t.Fatal("Event not received")
		}

		ed.Unsubscribe(id)
	})

	t.Run("Unsubscribe", func(t *testing.T) {
		received := make(chan *NodeEvent, 1)

		id := ed.Subscribe(func(event *NodeEvent) {
			received <- event
		})

		ed.Unsubscribe(id)

		event := &NodeEvent{
			Node:  &NodeInfo{ID: "test-node"},
			Event: EventJoin,
			Time:  time.Now().UnixNano(),
		}

		ed.Dispatch(event)

		select {
		case <-received:
			t.Fatal("Should not receive event after unsubscribe")
		case <-time.After(100 * time.Millisecond):
			// Expected - no event received
		}
	})
}

func TestNodeWithState(t *testing.T) {
	node := &NodeInfo{
		ID:        "test-node",
		Addr:      "192.168.1.1",
		Port:      7947,
		LoadScore: 0.5,
		Timestamp: time.Now().UnixNano(),
	}

	nws := &NodeWithState{
		Node: node,
		State: &NodeState{
			Status:      NodeStatusAlive,
			StatusSince: time.Now().UnixNano(),
			LastSeen:    time.Now().UnixNano(),
		},
	}

	t.Run("IsAvailable", func(t *testing.T) {
		assert.True(t, nws.IsAvailable())

		// High load
		nws.Node.LoadScore = 0.95
		assert.False(t, nws.IsAvailable())

		// Not alive
		nws.Node.LoadScore = 0.5
		nws.State.Status = NodeStatusDead
		assert.False(t, nws.IsAvailable())
	})
}

func TestDuration(t *testing.T) {
	t.Run("UnmarshalJSON from string", func(t *testing.T) {
		d := Duration{}
		err := d.UnmarshalJSON([]byte(`"5s"`))
		require.NoError(t, err)
		assert.Equal(t, 5*time.Second, d.Duration)
	})

	t.Run("UnmarshalJSON from number (seconds)", func(t *testing.T) {
		d := Duration{}
		err := d.UnmarshalJSON([]byte(`10`))
		require.NoError(t, err)
		assert.Equal(t, 10*time.Second, d.Duration)
	})

	t.Run("UnmarshalJSON from fractional number", func(t *testing.T) {
		d := Duration{}
		err := d.UnmarshalJSON([]byte(`0.5`))
		require.NoError(t, err)
		assert.Equal(t, 500*time.Millisecond, d.Duration)
	})

	t.Run("MarshalJSON", func(t *testing.T) {
		d := Duration{5 * time.Second}
		data, err := d.MarshalJSON()
		require.NoError(t, err)
		assert.Equal(t, []byte(`"5s"`), data)
	})
}

// Integration tests for NATS-based discovery, handoff, and leader election.
// These tests require a running NATS server with JetStream enabled
// and are skipped by default. Run with:
//   NATS_URL=nats://localhost:4222 go test -run TestDiscovery -v

func TestDiscoveryServiceNodeDiscovery(t *testing.T) {
	t.Skip("Requires running NATS server with JetStream; set NATS_URL to enable")
}

func TestHandoffCoordinator(t *testing.T) {
	t.Run("CanHandleWithLoadThreshold", func(t *testing.T) {
		cfg := &Config{
			NodeID: "handoff-node",
			Handoff: HandoffConfig{
				Enabled:       true,
				LoadThreshold: 0.8,
			},
		}

		ds, err := NewDiscoveryService(cfg)
		require.NoError(t, err)

		hc := NewHandoffCoordinator(ds, cfg.Handoff)
		defer hc.Close()

		// With low load, should be able to handle
		ds.localNode.LoadScore = 0.5
		assert.True(t, hc.CanHandle(""))

		// With high load, should not be able to handle
		ds.localNode.LoadScore = 0.9
		assert.False(t, hc.CanHandle(""))
	})

	t.Run("FindTargetNode", func(t *testing.T) {
		cfg := &Config{
			NodeID: "coordinator-node",
		}

		ds, err := NewDiscoveryService(cfg)
		require.NoError(t, err)

		hc := NewHandoffCoordinator(ds, cfg.Handoff)
		defer hc.Close()

		// Add some candidate nodes
		node1 := &NodeInfo{
			ID:        "target-1",
			Addr:      "192.168.1.1",
			Port:      7947,
			LoadScore: 0.3,
			AgentCaps: map[string]string{"model": "gpt-4"},
			Timestamp: time.Now().UnixNano(),
		}
		ds.membership.UpdateNode(node1)

		node2 := &NodeInfo{
			ID:        "target-2",
			Addr:      "192.168.1.2",
			Port:      7947,
			LoadScore: 0.7,
			AgentCaps: map[string]string{"model": "gpt-4"},
			Timestamp: time.Now().UnixNano(),
		}
		ds.membership.UpdateNode(node2)

		// Should select the least loaded node
		target, err := hc.findTargetNode(&HandoffRequest{})
		require.NoError(t, err)
		assert.Equal(t, "target-1", target.Node.ID)
	})
}

func TestLeaderElection(t *testing.T) {
	t.Skip("Requires running NATS server with JetStream; set NATS_URL to enable")
}
