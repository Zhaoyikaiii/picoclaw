# PicoClaw Swarm Mode Architecture

## Overview

PicoClaw Swarm Mode enables multiple PicoClaw instances to work together as a distributed system, providing:
- **Node Discovery**: Automatic peer discovery via NATS JetStream KV with TTL-based liveness
- **Health Monitoring**: Heartbeat renewal with configurable TTL and failure detection
- **Load Balancing**: Intelligent task distribution based on node load scoring
- **Handoff Mechanism**: Dynamic task delegation via NATS request-reply
- **Leader Election**: Distributed leader election via KV CAS (Compare-And-Swap) lock

**Transport**: All swarm communication uses NATS as the sole transport layer. There is no UDP gossip, no custom RPC, and no unencrypted channels.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        PicoClaw Swarm                           │
│                     (NATS as sole transport)                    │
├─────────────────────────────────────────────────────────────────┤
│  Control Plane                   │  Data Plane                  │
│  ├─ Node Discovery (KV + TTL)   │  ├─ Task Execution           │
│  ├─ Membership Management       │  ├─ Session Transfer          │
│  ├─ Health Monitoring            │  │   (NATS request-reply)     │
│  ├─ Load Monitoring              │  └─ Message Routing           │
│  └─ Leader Election (KV CAS)    │      (targeted subjects)      │
└─────────────────────────────────────────────────────────────────┘
```

## NATS Subject Namespace

All swarm messages are published under a common subject prefix. NATS ACL rules should restrict `picoclaw.swarm.>` to authenticated swarm node identities only.

| Subject Pattern | Purpose |
|-----------------|---------|
| `picoclaw.swarm.heartbeat.<nodeID>` | Heartbeat / KV renewal |
| `picoclaw.swarm.node.<nodeID>.msg` | Direct node messaging (request-reply) |
| `picoclaw.swarm.handoff.<nodeID>` | Targeted handoff requests (request-reply) |
| `picoclaw.swarm.leader` | Leader election announcements |
| `picoclaw.swarm.metrics.<nodeID>` | Metrics publishing |

## JetStream KV Buckets

| Bucket | Key Pattern | TTL | Purpose |
|--------|-------------|-----|---------|
| `swarm_members` | `node.<nodeID>` | 15s (configurable) | Membership liveness via TTL expiry |
| `swarm_status` | `status.<nodeID>` | 30s | Detailed node status cache (CPU, mem, tasks) |
| `swarm_leader` | `leader` | 10s | Leader election CAS lock |

## Control Plane

### 1. Node Discovery

Nodes discover each other by writing entries to the `swarm_members` KV bucket and watching for changes. Liveness is determined by TTL — if a node stops renewing its entry, the key expires and the node is considered dead.

```mermaid
sequenceDiagram
    participant Node1
    participant NATS as NATS JetStream KV
    participant Node2

    Note over Node1: New node starts
    Node1->>NATS: KV Put (node.node-1, NodeInfo, TTL=15s)
    Node1->>NATS: KV Watch (swarm_members.>)
    Node2->>NATS: KV Put (node.node-2, NodeInfo, TTL=15s)
    NATS-->>Node1: Watch event: node.node-2 created
    NATS-->>Node2: Watch event: node.node-1 exists
    Note over Node1,Node2: Cluster formed

    loop Every heartbeat_interval (5s)
        Node1->>NATS: KV Put (renew TTL)
        Node2->>NATS: KV Put (renew TTL)
    end

    Note over Node1: Node1 crashes
    NATS-->>Node2: Watch event: node.node-1 expired (TTL)
    Note over Node2: Mark Node1 as dead
```

**Key Parameters:**

| Parameter | Default | Description |
|-----------|---------|-------------|
| `heartbeat_interval` | 5s | Frequency of KV entry renewal |
| `member_ttl` | 15s | TTL for member entries (must be > heartbeat_interval) |
| `node_timeout` | 5s | Time before marking node as suspect |
| `dead_node_timeout` | 30s | Time before removing dead node from view |

### 2. Membership Management

Each node maintains a local `ClusterView` populated by KV watch events:

```go
type ClusterView struct {
    sync.RWMutex
    localNode  *NodeInfo
    members    map[string]*NodeWithState  // node_id -> NodeWithState
    Size       int
}
```

**Node State Machine:**

```mermaid
stateDiagram-v2
    [*] --> Alive: KV entry created
    Alive --> Suspect: Heartbeat not renewed within node_timeout
    Suspect --> Alive: KV entry renewed
    Suspect --> Dead: KV entry expired (TTL)
    Dead --> [*]: Removed from view
    Alive --> Left: Node gracefully stops (deletes KV entry)
    Left --> [*]
```

**Node Information:**

```go
type NodeInfo struct {
    ID        string            // Unique node identifier
    Addr      string            // IP address
    Port      int               // Service port
    AgentCaps map[string]string // Capabilities (models, tools)
    LoadScore float64           // Current load (0.0-1.0)
    Labels    map[string]string // Custom labels
    Timestamp int64             // Last update time (UnixNano)
    Version   string            // Protocol version
}
```

### 3. Health Monitoring

Health is driven by KV TTL — no separate probe/ping mechanism is needed:

```mermaid
sequenceDiagram
    participant Node as Node
    participant KV as NATS KV (swarm_members)
    participant Watcher as Other Nodes (KV Watch)

    loop Every heartbeat_interval
        Node->>KV: Put (node.<id>, NodeInfo, TTL=15s)
        KV-->>Watcher: Watch event: key updated
        Watcher->>Watcher: Update LastSeen, mark Alive
    end

    Note over Node: Node stops renewing
    KV->>KV: TTL expires, key deleted
    KV-->>Watcher: Watch event: key deleted
    Watcher->>Watcher: Mark as Dead, dispatch NodeLeft event
```

### 4. Load Monitoring

Each node continuously monitors its resource usage (transport-agnostic):

```mermaid
graph TB
    subgraph Load Monitor
        A[CPU Sample] --> D[Score Calculator]
        B[Memory Sample] --> D
        C[Session Count] --> D
        D --> E[Load Score]
    end

    subgraph Weights
        A -.->|0.3| D
        B -.->|0.3| D
        C -.->|0.4| D
    end

    E --> F{Threshold Check}
    F -->|< 0.8| G[Normal Mode]
    F -->|>= 0.8| H[Overloaded - Trigger Handoff]
```

**Load Score Formula:**

```
LoadScore = (CPUUsage * cpu_weight) +
            (MemoryUsage * memory_weight) +
            (SessionRatio * session_weight)

Where:
- CPUUsage = current CPU usage (0.0-1.0)
- MemoryUsage = current memory usage (0.0-1.0)
- SessionRatio = current_sessions / max_sessions
- Default weights: cpu=0.3, memory=0.3, session=0.4
```

### 5. Leader Election

Leader election uses a CAS (Compare-And-Swap) lock on the `swarm_leader` KV bucket:

```mermaid
sequenceDiagram
    participant NodeA
    participant KV as NATS KV (swarm_leader)
    participant NodeB

    NodeA->>KV: Create("leader", {nodeID: A}, TTL=10s)
    KV-->>NodeA: OK (revision=1)
    Note over NodeA: Becomes leader

    NodeB->>KV: Create("leader", {nodeID: B}, TTL=10s)
    KV-->>NodeB: Key exists (conflict)
    NodeB->>KV: Watch("leader")
    Note over NodeB: Becomes follower, watches for changes

    loop Every renewal_interval (3s)
        NodeA->>KV: Update("leader", {nodeID: A}, revision=N)
        KV-->>NodeA: OK (revision=N+1)
    end

    Note over NodeA: NodeA crashes, stops renewing
    KV->>KV: TTL expires, key deleted
    KV-->>NodeB: Watch event: "leader" deleted
    NodeB->>KV: Create("leader", {nodeID: B}, TTL=10s)
    KV-->>NodeB: OK
    Note over NodeB: Becomes new leader
```

## Data Plane

### 1. Request Routing

Inter-node messages use NATS request-reply on targeted subjects:

```mermaid
sequenceDiagram
    participant User
    participant N1 as Node 1
    participant NATS
    participant N2 as Node 2

    User->>N1: Message
    N1->>N1: Check local load

    alt Load OK
        N1->>N1: Process locally
        N1->>User: Response
    else Overloaded
        N1->>NATS: Request on picoclaw.swarm.node.N2.msg
        NATS->>N2: Deliver request
        N2->>NATS: Reply with response
        NATS->>N1: Deliver reply
        N1->>User: Response (from N2)
    end
```

### 2. Handoff Mechanism

**Handoff Decision Flow:**

```mermaid
flowchart TD
    A[Receive Request] --> B{Should Handoff?}
    B -->|Local load >= threshold| C[Select Target Node]
    B -->|Local load < threshold| D[Process Locally]

    C --> E{Target Available?}
    E -->|Yes| F[NATS Request to target]
    E -->|No| G[Retry or Fail]

    F --> H[Target validates & processes]
    H --> I{Reply received?}
    I -->|Yes, accepted| J[Return target's response]
    I -->|Timeout| K[Retry next node or fail]
```

**Handoff Protocol (NATS request-reply):**

```mermaid
sequenceDiagram
    participant Source as Overloaded Node
    participant NATS
    participant Target as Selected Node

    Source->>Source: Check load threshold
    Source->>NATS: Request on picoclaw.swarm.handoff.<targetID>
    NATS->>Target: Deliver HandoffRequest

    Target->>Target: Validate (load OK? can handle?)
    alt Accepted
        Target->>Target: Process request
        Target->>NATS: Reply: HandoffResponse{accepted, result}
        NATS->>Source: Deliver reply
        Source->>Source: Return result to caller
    else Rejected
        Target->>NATS: Reply: HandoffResponse{rejected, reason}
        NATS->>Source: Deliver rejection
        Source->>Source: Try next node or process locally
    end
```

### 3. Direct Node Messaging

Nodes can send arbitrary messages to specific peers using NATS request-reply:

```
Subject: picoclaw.swarm.node.<targetNodeID>.msg
Payload: JSON {action, message, channel, chat_id, sender_id, trace_id}
Reply:   JSON {response} or {error}
```

Context parameters (`channel`, `chat_id`, `sender_id`, `trace_id`) enable cross-node audit trails and conversation continuity.

## System Architecture

### Component Overview

```mermaid
graph TB
    subgraph "Node 1"
        D1[Discovery Service] --> M1[Membership Manager]
        H1[Handoff Coordinator] --> M1
        L1[Load Monitor] --> H1
        LE1[Leader Election] --> D1
        A1[Agent Loop] --> L1
    end

    subgraph "Node 2"
        D2[Discovery Service] --> M2[Membership Manager]
        H2[Handoff Coordinator] --> M2
        L2[Load Monitor] --> H2
        LE2[Leader Election] --> D2
        A2[Agent Loop] --> L2
    end

    subgraph NATS["NATS Server (JetStream)"]
        KV1[KV: swarm_members]
        KV2[KV: swarm_status]
        KV3[KV: swarm_leader]
        SUB[Subjects: picoclaw.swarm.*]
    end

    D1 <-->|KV Watch + Put| KV1
    D2 <-->|KV Watch + Put| KV1
    H1 <-->|Request-Reply| SUB
    H2 <-->|Request-Reply| SUB
    LE1 <-->|CAS Lock| KV3
    LE2 <-->|CAS Lock| KV3
```

### Communication Channels

| Channel | Protocol | Purpose |
|---------|----------|---------|
| Discovery | NATS KV (JetStream) | Membership liveness via TTL |
| Status Cache | NATS KV (JetStream) | Detailed node status (CPU, mem, tasks) |
| Leader Election | NATS KV CAS (JetStream) | Distributed leader lock |
| Handoff | NATS request-reply | Session transfer coordination |
| Node Messaging | NATS request-reply | Direct inter-node messages |
| Metrics | NATS publish | Observability data |

## Configuration

### Example Configuration

```json
{
  "swarm": {
    "enabled": true,
    "node_id": "picoclaw-node-1",

    "discovery": {
      "nats_url": "nats://nats.example.com:4222",
      "nats_creds_file": "/etc/picoclaw/nats.creds",
      "nats_tls_cert": "/etc/picoclaw/client-cert.pem",
      "nats_tls_key": "/etc/picoclaw/client-key.pem",
      "nats_tls_ca_cert": "/etc/picoclaw/ca.pem",
      "heartbeat_interval": "5s",
      "member_ttl": "15s",
      "node_timeout": "5s",
      "dead_node_timeout": "30s"
    },

    "handoff": {
      "enabled": true,
      "load_threshold": 0.8,
      "timeout": "30s",
      "max_retries": 3,
      "retry_delay": "5s",
      "request_timeout": "10s"
    },

    "load_monitor": {
      "enabled": true,
      "interval": "5s",
      "sample_size": 60,
      "cpu_weight": 0.3,
      "memory_weight": 0.3,
      "session_weight": 0.4
    },

    "leader_election": {
      "enabled": false,
      "lock_ttl": "10s",
      "renewal_interval": "3s"
    }
  }
}
```

### Minimal Two-Node Setup

**1. Start NATS with JetStream:**

```bash
docker run -d --name nats \
  -p 4222:4222 \
  nats:latest -js
```

**2. Node 1 config (`node1.json`):**

```json
{
  "swarm": {
    "enabled": true,
    "node_id": "node-1",
    "discovery": {
      "nats_url": "nats://localhost:4222"
    }
  }
}
```

**3. Node 2 config (`node2.json`):**

```json
{
  "swarm": {
    "enabled": true,
    "node_id": "node-2",
    "discovery": {
      "nats_url": "nats://localhost:4222"
    }
  }
}
```

**4. Verify cluster:**

Once both nodes start, each should discover the other via KV watch within one `heartbeat_interval` (5s). Use the `swarm_nodes` tool or `/nodes` command to verify membership.

### Deployment Modes

**Single Entry Point:**

```mermaid
graph LR
    TG[Telegram Gateway] --> N1[Node 1: Leader]
    N1 <-->|NATS| N2[Node 2: Worker]
    N1 <-->|NATS| N3[Node 3: Worker]
```

**Multi-Entry Point (with load balancer):**

```mermaid
graph LR
    LB[Load Balancer] --> N1[Node 1]
    LB --> N2[Node 2]
    N1 <-->|NATS Mesh| N2
    N1 <-->|NATS Mesh| N3[Node 3]
    N2 <-->|NATS Mesh| N3
```

## Event System

The swarm publishes events for monitoring and integration:

```mermaid
graph LR
    A[Node Joined] --> ED[Event Dispatcher]
    B[Node Left] --> ED
    C[Node Suspect] --> ED
    D[Leader Changed] --> ED
    E[Handoff Started] --> ED
    F[Handoff Completed] --> ED

    ED --> H[Handlers]
    H --> L[Logging]
    H --> M[Metrics]
    H --> U[Custom Actions]
```

**Event Types:**

| Event | Description | Payload |
|-------|-------------|---------|
| `NodeJoined` | New node discovered via KV watch | NodeInfo |
| `NodeLeft` | Node KV entry expired or deleted | NodeID |
| `NodeSuspect` | Node heartbeat overdue | NodeID |
| `NodeAlive` | Node recovered (KV entry renewed) | NodeInfo |
| `LeaderChanged` | Leader election result changed | LeaderID |
| `HandoffStarted` | Handoff initiated | HandoffOperation |
| `HandoffCompleted` | Handoff finished | HandoffResult |
| `HandoffFailed` | Handoff error | Error |

## Error Handling

```mermaid
flowchart TD
    A[Operation Failed] --> B{Retryable?}
    B -->|Yes| C[Increment retry count]
    B -->|No| D[Return error immediately]

    C --> E{Max retries reached?}
    E -->|No| F[Wait retry_delay]
    F --> G[Retry operation]

    E -->|Yes| H[Mark node suspect]
    H --> I[Select alternative node]

    G --> J{Success?}
    J -->|Yes| K[Continue]
    J -->|No| C
```

## Security

### Threat Model

PicoClaw Swarm assumes nodes are deployed in a **trusted or semi-trusted network**. The NATS server is the single trust boundary.

| Concern | Mitigation |
|---------|------------|
| **Transport encryption** | NATS TLS (configure `nats_tls_*` options) |
| **Node authentication** | NATS credentials file (NKeys/JWT) or mTLS client certs |
| **Subject authorization** | NATS ACL: restrict `picoclaw.swarm.>` to swarm node identities |
| **Inter-node message integrity** | NATS connection-level TLS ensures no tampering |
| **Unauthorized KV access** | JetStream permissions: only swarm nodes can read/write swarm KV buckets |

### Production Recommendations

1. **Always enable TLS** — set `nats_tls_cert`, `nats_tls_key`, `nats_tls_ca_cert`
2. **Use NKeys or JWT credentials** — set `nats_creds_file` for per-node identity
3. **Configure NATS ACLs** — restrict `picoclaw.swarm.>` publish/subscribe to swarm accounts
4. **Network isolation** — NATS port (4222) should not be exposed to public internet
5. **Rotate credentials** — use short-lived JWTs with NATS account server for production

### Minimal Secure NATS Configuration

```conf
# nats-server.conf
listen: 0.0.0.0:4222
jetstream: enabled

tls {
  cert_file: "/etc/nats/server-cert.pem"
  key_file:  "/etc/nats/server-key.pem"
  ca_file:   "/etc/nats/ca.pem"
  verify:    true  # require client certs (mTLS)
}

authorization {
  swarm_user = {
    publish = ["picoclaw.swarm.>", "$JS.API.>"]
    subscribe = ["picoclaw.swarm.>", "_INBOX.>"]
  }
  users = [
    { nkey: "UABC...", permissions: $swarm_user }
  ]
}
```

## Future Enhancements

1. **Consistent Hashing**: Route requests to stable node assignments for session affinity
2. **Multi-Region**: Geo-distributed clusters with region-aware subject prefixes
3. **Graceful Draining**: Leader-coordinated node shutdown with session migration
4. **Observability**: Prometheus metrics endpoint for swarm health (connection count, handoff latency, leader changes)
