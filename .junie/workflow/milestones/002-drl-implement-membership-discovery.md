# 002-drl-implement-membership-discovery

## Goal

Transform the "empty" DRL app into a clustered system where instances automatically find each other using Hashicorp
`memberlist` and gossip protocols.

## Requirements

### 1. Memberlist Integration

* **Implementation:** Embed `github.com/hashicorp/memberlist` using the `DefaultLANConfig`.
* **Dynamic Join:** On startup, the node must:

1. Wait a few seconds for networking to stabilize.
2. Resolve the hostname `drl` (the docker-compose service name).
3. Attempt to `Join()` all returned IP addresses except its own.


* **Logging:** Use structured logging to show:
1. `local_node_name`: The unique ID/IP of the current node.
2. `discovered_peers`: The list of IPs found via DNS.
3. `cluster_size`: The total number of healthy members known.

### 2. Metrics (Prometheus)

* Implement a `/metrics` endpoint (port 9091).
* Export at least:
1. `drl_membership_cluster_size`: Gauge of currently active members.
2. `drl_membership_events_total`: Counter for `Join/Leave/Fail` events.

### 3. Docker Compose Scaling

* Update `docker-compose.yaml` to start the DRL service with `deploy: replicas: 2`.
* Ensure each DRL instance gets a unique `NODE_NAME` (you can use the container hostname).

## Implementation Guidelines

* **Healthchecks:** Add a Docker healthcheck to DRL that returns "Healthy" only after the cluster has at least one other
  member (or after a timeout if it's the first node).
* **Environment Variables:**
* `BIND_ADDR`: The IP the node listens on (default to `0.0.0.0`).
* `DISCOVERY_SERVICE_NAME`: Set to `drl` for the DNS lookup.

## Validation Criteria

1. **Logs Verification**: Startup logs must show: `[DEBUG] drl: Found 2 peers via DNS: [172.x.x.x, 172.x.x.y]`.
2. **Cluster Convergence**: Both replicas should eventually log that the cluster size is `2`.
3. **Resilience**: Running `docker compose scale drl=3` should result in all 3 nodes seeing each other within seconds
   without a restart.
