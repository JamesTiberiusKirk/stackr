# ADR 0004: Multi-Node Deployment

## Status

Proposed (future work)

## Context

Stackr currently manages stacks on a single server. As infrastructure grows, users need to manage stacks across multiple servers/VPSes from a single control plane. This includes:

1. **Production scaling**: Distributing workloads across multiple nodes
2. **Environment separation**: Running staging on a different server than production
3. **Dedicated build nodes**: Offloading CI/image building to a separate node to avoid impacting production
4. **Resilience**: If a node goes down, stateless stacks can be restarted elsewhere

## Decision

### Architecture: Hub and Spoke

One stackrd instance acts as the **hub** (control plane). Other nodes run stackrd in **agent mode** — same binary, different role.

```
                         ┌── agent (vps-prod-1)
stackrd (hub) ───────────┤── agent (vps-prod-2)
  UI, auth, API,         ├── agent (vps-staging)
  state aggregation      └── agent (vps-builder)
```

The hub:
- Serves the web UI, API, and auth (all from ADR-0002)
- Knows about all nodes and their capabilities
- Proxies deploy/cron/status commands to the appropriate agent
- Aggregates state from agents for a unified view

Agents:
- Run a full stackrd with their own SQLite (independently functional)
- Execute Docker commands locally
- Expose an internal API for the hub to call
- Keep running if the hub goes down (cron still fires, stacks stay up)

### Configuration

Nodes declared in `.stackr.yaml`:

```yaml
nodes:
  vps-prod-1:
    address: https://vps-prod-1.company.dev:9000
    labels: [prod, eu, high-mem]
  vps-prod-2:
    address: https://vps-prod-2.company.dev:9000
    labels: [prod, eu]
  vps-staging:
    address: https://vps-staging.company.dev:9000
    labels: [staging]
  vps-builder:
    address: https://vps-builder.company.dev:9000
    labels: [builder]
```

### Stack Placement

Stacks declare where they should run:

```yaml
# stacks/api/stackr/config.yaml
placement:
  server: vps-prod-1          # pin to specific node
```

```yaml
# stacks/worker/stackr/config.yaml
placement:
  labels: [prod, eu]          # any node matching these labels
```

```yaml
# stacks/postgres/stackr/config.yaml
placement:
  server: vps-prod-1          # stateful, pinned
  stateful: true              # cannot be automatically moved
```

If no `placement` is defined, the stack runs on the hub node (backward compatible with single-node setup).

### Dedicated Builder Node

A node labeled `builder` can be used for:

- **Image builds**: Stacks with a `build:` section in their compose file can offload builds to the builder node instead of building on the production server
- **CI tasks**: Cron jobs that run tests, linting, or other CI tasks can target the builder
- **PR environments**: Ephemeral environments (ADR-0003) run on the builder by default to isolate them from production

```yaml
# stacks/api/stackr/config.yaml
build:
  node: builder               # build images on the builder node
  push: true                  # push to registry after build
placement:
  labels: [prod]              # run on prod nodes
```

The flow:
1. Hub tells builder node to build the image
2. Builder builds and pushes to a registry (GHCR, local registry, etc.)
3. Hub tells target node(s) to pull and deploy

This keeps production nodes lean — they only pull pre-built images, never run `docker build`.

### State Model

**Each node has its own SQLite** — deployments, cron executions, events for stacks running on that node.

**The hub aggregates** — when the UI requests "show all deployments," the hub queries its own DB plus all agent APIs. This is the same pattern as querying Docker live for container status (ADR-0002) — runtime state is derived, not replicated.

**Auth is centralized on the hub** — agents authenticate incoming requests from the hub using a shared node token. Users never talk to agents directly; they go through the hub's API/UI.

```yaml
# Agent's .env
STACKR_HUB_TOKEN=<shared-secret>
STACKR_MODE=agent
```

### Node Health

The hub periodically checks agent health:
- Agent reachable? (heartbeat)
- Docker daemon running?
- Disk/memory usage (optional, for placement decisions)
- Running stacks and their status

If a node goes unreachable:
- Hub marks it as unhealthy in the UI
- Stateless stacks with label-based placement CAN be restarted on another matching node (manual or automatic depending on config)
- Stateful stacks are flagged as degraded — admin intervention required

### Storage (❓ UNDER DISCUSSION)

Moving stacks between nodes requires moving their data. This is the hardest problem in multi-node.

#### Stateless stacks
No volumes, no bind mounts — can move freely between nodes. No storage concern.

#### Stateful stacks (❓)

Several options, none fully decided:

**Option A: Pin and don't move** — stateful stacks are pinned to a node. Migration is manual: stop, backup, transfer, restore, start. Stackr can assist (it already has backup/archive infrastructure) but doesn't automate.

**Option B: Shared storage (NFS/GlusterFS/Ceph)** — all nodes mount the same storage backend. Volumes accessible from any node. Adds infrastructure dependency. Performance concerns for databases.

**Option C: Volume replication (Longhorn, DRBD, Litestream)** — data replicated across nodes at the block or application level. Complex but no shared infrastructure needed.

**Option D: Backup-and-restore on move** — before moving a stack, archive volumes, transfer to target node, restore. Uses existing stackr archive infrastructure. Has downtime during transfer.

**Current recommendation**: Option A for v1. Stateful stacks are pinned. Stateless stacks can float. Revisit storage mobility when the multi-node foundation is proven. This avoids solving distributed storage — which is what Kubernetes, Nomad, and their ecosystem of CSI drivers exist to do.

### API Impact

New endpoints on the hub:

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/nodes` | List all nodes with health status |
| GET | `/api/v1/nodes/{name}` | Node detail (labels, stacks, resources) |
| GET | `/api/v1/nodes/{name}/stacks` | Stacks running on a specific node |

Existing endpoints become node-aware:
- `GET /api/v1/stacks` returns stacks across all nodes with a `node` field
- `POST /api/v1/stacks/{name}/deploy` routes to the correct node based on placement
- `GET /api/v1/deployments` aggregates history from all nodes

Agent-to-hub internal API:
- Hub authenticates to agents via node token
- Agents expose the same `/api/v1/` surface but only for their local stacks

## Consequences

### What becomes easier

- **Scale out**: Add a new VPS, register it as a node, deploy stacks to it
- **Environment isolation**: Staging on one server, production on another
- **Build isolation**: CI/builds on a dedicated node, production stays clean
- **Unified management**: One UI/API to manage everything across all nodes

### What becomes harder

- **Networking**: Stacks on different nodes can't communicate over Docker networks. Need overlay networking or external service discovery.
- **Storage**: Stateful stack migration is unsolved for v1.
- **Debugging**: Issues could be hub-side, agent-side, or network-related. More failure modes.
- **Configuration**: Node registration, placement rules, and build routing add config complexity.
- **State aggregation**: Querying multiple agents for a unified view adds latency and failure handling.

## Alternatives Considered

- **Single control plane, remote Docker sockets**: One stackrd talks to multiple Docker daemons over TCP/SSH. Simpler (no agent) but requires Docker socket exposed over network. No independent operation if hub dies.
- **Peer federation**: Every node is a full stackrd, they share auth. No hub. More resilient but harder to coordinate and present a unified view.
- **Docker Swarm**: Built-in multi-node orchestration for Docker. But Swarm is effectively abandoned, and stackr's value is the file-driven declarative model on top of Compose.
- **Just use Kubernetes/Nomad**: Solves all of this but abandons the simplicity of Docker Compose and the "files as source of truth" philosophy that defines stackr.

## Implementation Notes

This feature depends on:
- ADR-0002 (state store, API, auth) being fully implemented
- ADR-0003 (multi-environment) being at least designed, since environments and nodes interact (PR envs on builder, staging on staging node)

Key design constraint: the single-node setup must remain the default and require zero multi-node configuration. A stackrd with no `nodes:` config behaves exactly as it does today.
