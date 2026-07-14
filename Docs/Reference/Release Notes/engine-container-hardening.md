# Engine Container Hardening Release Notes

Draft release notes for the Engine least-privilege container hardening work.

## Highlights

- Borealis Engine containers now default to least-privilege runtime settings.
- `Engine.sh` creates and repairs the `borealis-engine` system user/group and writes numeric runtime IDs into `compose.env`.
- Normal Engine services run non-root with read-only root filesystems, dropped capabilities, `no-new-privileges`, tmpfs scratch space, PID limits, and profile-scaled CPU/memory caps.
- Docker socket write access is isolated to `site-worker-orchestrator`; status reads use a read-only Docker proxy.
- Dynamic `site-worker-*` containers run non-root, without Docker socket access, without Traefik config access, and with read-only API config/secrets.
- `remote-desktop-guacd` now runs non-root, binds only to Engine loopback, mounts only its logs, and stays off the Docker socket.
- WebUI runtime source mounts are read-only inside the container.

## Security Changes

### Runtime Identity

`Engine.sh` now prepares the Engine runtime identity during deployment. Most containers use the stable `borealis-engine` UID/GID instead of root. PostgreSQL keeps the official PostgreSQL non-root UID so existing database state remains compatible with the upstream image.

### Container Defaults

Hardened containers use:

- `read_only: true`
- `cap_drop: [ALL]`
- `security_opt: no-new-privileges:true`
- tmpfs-backed writable scratch paths
- PID limits
- profile-scaled CPU and memory limits
- explicit writable bind mounts only where runtime state must persist

Limits are caps, not reservations. Site-worker caps are per active worker, so host sizing still needs to account for aggregate active worker count.

### Root Exceptions

Root is no longer the default container posture. Current root exceptions are:

- `traefik-edge`: binds host-network ports `80` and `443`; adds only `NET_BIND_SERVICE`.
- `wireguard-tunnel`: manages the WireGuard interface; uses `/dev/net/tun`, `NET_ADMIN`, and `NET_RAW`.
- Short-lived service-action helper: runs `Engine.sh --service` for allowlisted restart, rebuild, reload, and reconcile operations using `/opt/Borealis` plus the Docker socket.

These exceptions still use hardening controls such as dropped capabilities where possible, `no-new-privileges`, read-only root filesystems, tmpfs scratch space, and resource limits.

### Docker Boundary

`site-worker-orchestrator` is now the only static service with write access to the Docker socket. It accepts HMAC-authenticated requests over an Engine-local Unix socket and launches only allowlisted site-worker images with fixed hardening flags.

`job-scheduler` no longer mounts the Docker socket. It asks `site-worker-orchestrator` to start, stop, inspect, and remove site-worker containers and to launch allowlisted service-action helpers.

`docker-proxy` keeps read-only Docker socket access for container status and metrics. API and UI status reads go through this proxy or scheduler snapshots.

### Mount Isolation

Cross-service bind mounts were reduced to explicit contracts:

- `job-scheduler` gets API cache/logs read-write, API config/secrets read-only, orchestrator run socket read-write, and Traefik dynamic config read-write.
- `site-worker-orchestrator` gets API cache read-write, site-worker logs read-write, API secrets read-only, orchestrator run path read-write, and Docker socket read-write.
- Dynamic `site-worker-*` containers get API cache/logs read-write and API config/secrets read-only. They no longer mount Traefik config.
- WebUI source mounts are read-only inside the WebUI container.

`job-scheduler` is the single writer for site-worker Traefik route files. Site workers set `BOREALIS_SITE_WORKER_ROUTE_FILE_WRITES=0`, preserving database state updates without route-file writes.

### Remote Desktop Runtime

`remote-desktop-guacd` now runs as the Borealis runtime user, binds guacd to `127.0.0.1`, uses a read-only root filesystem, drops capabilities, and only persists logs under `Engine/Services/remote-desktop-guacd/logs`.

## Operational Impact

- Engine deploys now write profile-managed container resource caps into `Engine/Deploy/compose.env`.
- Operators can override caps before redeploy with environment variables.
- Existing service runtime paths are chowned during deploy while stricter modes are preserved for secrets, WireGuard keys, Traefik ACME state, and PostgreSQL state.
- Existing site workers are relaunched through the hardened orchestrator path during normal scheduler reconciliation.

## Validation Performed

- Static Compose rendering and policy checks.
- Engine shell syntax checks.
- Focused Python syntax checks for changed scheduler helper code.
- Runtime Docker inspect checks for static services and dynamic `site-worker-*` containers.
- Operator validation for Agent auditing, software management, assembly execution, Remote Desktop, Remote Shell, Remote Files, Remote Processes, Remote Registry, and Remote Services.

## Known Boundaries

- The Engine host remains the trust root. Docker daemon access is root-equivalent.
- `site-worker-orchestrator` and the service-action helper must be treated as privileged Engine control-plane paths because they can reach the Docker socket.
- The service-action helper still mounts `/opt/Borealis` and the Docker socket so `Engine.sh --service` can run current service actions. Replacing this would require a dedicated action executor or narrower service-management API.
- Site workers still use host networking because current Engine routing addresses per-worker loopback ports.
