# Borealis Docker Stack Breakdown
[Back to Docs Index](../index.md) | [Index (HTML)](../index.html)

## Purpose
Explain the Borealis Engine Docker Compose stack, service ownership, startup order, runtime paths, and common operator commands.

## Scope
- Linux Engine only.
- Docker Engine plus Docker Compose plugin.
- No Docker Desktop.
- Compose project name: `borealis-engine`.
- Compose source of truth: `Data/Engine/Containers/compose.yaml`.
- Runtime state: `Engine/`.

## Source And Runtime Layout
Committed source lives under:
```text
Data/Engine/Containers/
```

Runtime output lives under:
```text
Engine/
```

Deploy state:
```text
Engine/Deploy/compose.env
Engine/Deploy/image-manifest.json
Engine/Deploy/deploy-manifest.json
Engine/Deploy/build.log
```

Service state:
```text
Engine/Services/<role>/config
Engine/Services/<role>/env
Engine/Services/<role>/logs
Engine/Services/<role>/state
Engine/Services/<role>/secrets
Engine/Services/<role>/cache
Engine/Services/<role>/run
```

Operators should treat `Engine/` as generated runtime state. Edit source under `Data/Engine/Containers/`, then redeploy through `Engine.sh`.

## Stack Services
| Service | Container | Main responsibility | Host network endpoint |
| --- | --- | --- | --- |
| `postgres-db` | `borealis-engine-postgres-db` | PostgreSQL database and persisted DB state | `127.0.0.1:5432` |
| `wireguard-tunnel` | `borealis-engine-wireguard-tunnel` | Privileged WireGuard interface, peer config, firewall/routing, control socket | UDP `30000`, interface `borealis-wg` |
| `remote-desktop-guacd` | `borealis-engine-remote-desktop-guacd` | VNC-only Apache Guacamole guacd runtime | `127.0.0.1:4822` |
| `webui-frontend` | `borealis-engine-webui-frontend` | Production static WebUI or dev Vite HMR | prod `127.0.0.1:8080`, dev `127.0.0.1:5173` |
| `api-backend` | `borealis-engine-api-backend` | Flask API, Socket.IO, scheduler, workflows, VNC WebSocket proxy, Ansible control-node logic | `127.0.0.1:5000`, VNC WS `127.0.0.1:4823` |
| `traefik-edge` | `borealis-engine-traefik-edge` | Public HTTP/HTTPS edge, ACME, UI/API/Socket.IO/VNC routing | `80`, `443` |

All Engine containers use `network_mode: host`. Loopback assumptions are intentional.

## Volume Bindings
`api-backend`:
```text
Engine/Services -> /opt/Borealis/Engine/Services
```

`postgres-db`:
```text
Engine/Services/postgres-db/state -> /var/lib/postgresql/data
Engine/Services/postgres-db/logs  -> /var/log/postgresql
Engine/Services/postgres-db/run   -> /var/run/borealis
```

`traefik-edge`:
```text
Engine/Services/traefik-edge -> /opt/Borealis/Engine/Services/traefik-edge
```

`remote-desktop-guacd`:
```text
Engine/Services/remote-desktop-guacd/logs -> /opt/borealis/logs
```

`wireguard-tunnel`:
```text
Engine/Services/wireguard-tunnel -> /opt/Borealis/Engine/Services/wireguard-tunnel
```

`webui-frontend` does not currently bind persistent state directly. It reads runtime env and serves its packaged/static or dev-mode frontend runtime.

## Deploy Order
`Engine.sh deploy [prod|dev]` performs these phases:

1. Parse launch options.
2. If repo/release/branch options were supplied, sync the repository and re-exec installed `Engine.sh`.
3. Install or verify Engine dependencies.
4. Check for host PostgreSQL conflict on `127.0.0.1:5432`.
5. Create service runtime tree under `Engine/Services/`.
6. Prune empty legacy runtime paths.
7. Resolve public hostname and ACME email.
8. Render `Engine/Deploy/compose.env`.
9. Compute service input hashes.
10. Build changed local images as `borealis-engine/<service>:sha-<hash>`.
11. Write `Engine/Deploy/image-manifest.json`.
12. Re-render `compose.env` with resolved image tags.
13. Run Compose `up -d`.
14. Write `Engine/Deploy/deploy-manifest.json`.

Build order follows `Engine.sh` service order:
```text
api-backend
webui-frontend
traefik-edge
postgres-db
remote-desktop-guacd
wireguard-tunnel
```

Build order is not the same as runtime dependency order.

## Runtime Start Order
Compose dependency order:

1. `postgres-db`, `wireguard-tunnel`, `remote-desktop-guacd`, and `webui-frontend` can start independently.
2. `postgres-db` must pass healthcheck:
```text
pg_isready -h 127.0.0.1 -p 5432 -U "$POSTGRES_USER" -d "$POSTGRES_DB"
```
3. `api-backend` waits for:
```text
postgres-db: service_healthy
wireguard-tunnel: service_started
remote-desktop-guacd: service_started
```
4. `traefik-edge` waits for:
```text
api-backend: service_started
webui-frontend: service_started
```

Traefik is the public edge. API and WebUI stay on loopback behind Traefik.

## Production vs Dev Mode
Production mode:
```sh
bash Engine.sh deploy prod
```

Production behavior:
- `BOREALIS_ENGINE_MODE=production`
- `BOREALIS_WEBUI_MODE=prod`
- WebUI frontend serves built static UI.
- Traefik routes public HTTPS to loopback services.

Dev mode:
```sh
bash Engine.sh deploy dev
```

Dev behavior:
- WebUI frontend runs Vite HMR.
- Vite listens on loopback `127.0.0.1:5173`.
- Traefik still owns public HTTP/HTTPS and routes UI/API/WebSocket paths.

Default deploy mode:
```sh
bash Engine.sh deploy
```

Equivalent to:
```sh
bash Engine.sh deploy prod
```

## Main Operator Commands
Deploy or redeploy production:
```sh
cd /opt/Borealis
bash Engine.sh deploy prod
```

Deploy or redeploy dev:
```sh
cd /opt/Borealis
bash Engine.sh deploy dev
```

Branch install or redeploy from raw launcher:
```sh
curl -fsSL https://raw.githubusercontent.com/bunny-lab-io/Borealis/refs/heads/main/Engine.sh | sudo bash -s -- --repo-branch feature/containerize-all-borealis-services deploy prod
```

Compatibility router:
```sh
bash Borealis.sh --EngineProduction
bash Borealis.sh --EngineDev
```

Updater:
```sh
bash Update.sh -Engine
```

`Update.sh -Engine` updates the current codebase, then runs `Engine.sh deploy`.

## Service Commands
Restart API backend:
```sh
bash Engine.sh --service api-backend restart
```

Rebuild WebUI frontend in production mode:
```sh
bash Engine.sh --service webui-frontend rebuild prod
```

Rebuild WebUI frontend in dev mode:
```sh
bash Engine.sh --service webui-frontend rebuild dev
```

Reload Traefik edge:
```sh
bash Engine.sh --service traefik-edge reload
```

Restart PostgreSQL:
```sh
bash Engine.sh --service postgres-db restart
```

Restart guacd:
```sh
bash Engine.sh --service remote-desktop-guacd restart
```

Reconcile WireGuard tunnel state:
```sh
bash Engine.sh --service wireguard-tunnel reconcile
```

Generic service syntax:
```sh
bash Engine.sh --service <api-backend|webui-frontend|traefik-edge|postgres-db|remote-desktop-guacd|wireguard-tunnel> <restart|rebuild|reload|reconcile> [prod|dev]
```

Action support:
| Action | Supported services | Effect |
| --- | --- | --- |
| `restart` | any Engine service | Runs `docker compose restart <service>` after refreshing runtime env. |
| `rebuild` | any Engine service | Rebuilds selected image, updates image manifest/env, recreates service with `up -d --no-deps`. |
| `reload` | `traefik-edge` only | Restarts Traefik after config/env changes. |
| `reconcile` | `wireguard-tunnel` only | Runs `borealis-wireguard-control-client reconcile` inside tunnel container. |

## Direct Compose Commands
Use `Engine.sh` when possible. Direct Compose commands are useful for read-only inspection or emergency operations.

Base Compose command:
```sh
docker compose \
  --project-name borealis-engine \
  --env-file /opt/Borealis/Engine/Deploy/compose.env \
  -f /opt/Borealis/Data/Engine/Containers/compose.yaml \
  ps
```

List containers:
```sh
docker compose \
  --project-name borealis-engine \
  --env-file /opt/Borealis/Engine/Deploy/compose.env \
  -f /opt/Borealis/Data/Engine/Containers/compose.yaml \
  ps
```

Tail service logs:
```sh
docker compose \
  --project-name borealis-engine \
  --env-file /opt/Borealis/Engine/Deploy/compose.env \
  -f /opt/Borealis/Data/Engine/Containers/compose.yaml \
  logs -f api-backend
```

Restart one service directly:
```sh
docker compose \
  --project-name borealis-engine \
  --env-file /opt/Borealis/Engine/Deploy/compose.env \
  -f /opt/Borealis/Data/Engine/Containers/compose.yaml \
  restart api-backend
```

Avoid direct `docker compose down` during normal operations. It stops all Engine services, including PostgreSQL and WireGuard.

## Health Checks
Compose health/status:
```sh
docker compose \
  --project-name borealis-engine \
  --env-file /opt/Borealis/Engine/Deploy/compose.env \
  -f /opt/Borealis/Data/Engine/Containers/compose.yaml \
  ps
```

API liveness:
```sh
curl -fsS http://127.0.0.1:5000/health
```

PostgreSQL readiness:
```sh
pg_isready -h 127.0.0.1 -p 5432 -U borealis -d borealis
```

Public edge reachability:
```sh
curl -Ik https://<engine-fqdn>/
```

WireGuard listener:
```sh
sudo ss -lunp | grep ':30000'
sudo wg show borealis-wg
```

## Logs
Container build log:
```text
Engine/Deploy/build.log
```

API backend logs:
```text
Engine/Services/api-backend/logs/engine.log
Engine/Services/api-backend/logs/error.log
Engine/Services/api-backend/logs/api.log
Engine/Services/api-backend/logs/<service>.log
```

Traefik logs:
```text
Engine/Services/traefik-edge/logs/
```

PostgreSQL logs:
```text
Engine/Services/postgres-db/logs/
```

WireGuard tunnel logs:
```text
Engine/Services/wireguard-tunnel/logs/
Engine/Services/api-backend/logs/VPN_Tunnel/tunnel.log
```

Guacd logs:
```text
Engine/Services/remote-desktop-guacd/logs/
```

## Manifest Files
`Engine/Deploy/image-manifest.json` records:
- image tag
- input hash
- Dockerfile path
- build context
- mode
- timestamp

`Engine/Deploy/deploy-manifest.json` records:
- Compose project name
- deploy mode
- Compose file
- env file
- service list
- deploy timestamp

Use these files to confirm whether source changes are actually deployed.

## Common Scenarios
API code changed:
```sh
bash Engine.sh --service api-backend rebuild prod
```

WebUI code changed, production:
```sh
bash Engine.sh --service webui-frontend rebuild prod
```

WebUI code changed, dev/HMR:
```sh
bash Engine.sh --service webui-frontend rebuild dev
```

Traefik config changed:
```sh
bash Engine.sh --service traefik-edge reload
```

Database stuck or unhealthy:
```sh
bash Engine.sh --service postgres-db restart
docker compose --project-name borealis-engine --env-file /opt/Borealis/Engine/Deploy/compose.env -f /opt/Borealis/Data/Engine/Containers/compose.yaml ps postgres-db
```

WireGuard peers look stale:
```sh
bash Engine.sh --service wireguard-tunnel reconcile
```

Full safe redeploy:
```sh
bash Engine.sh deploy prod
```

## Operational Notes
- `Engine.sh deploy` is idempotent for unchanged inputs.
- Unchanged image hashes skip Docker builds.
- Compose should avoid recreating unchanged containers.
- Service-specific `rebuild` uses `--no-deps`, so dependent services are not intentionally restarted.
- `restart` does not rebuild images.
- `reload` is currently a Traefik restart.
- `reconcile` is currently WireGuard-only.
- PostgreSQL uses host networking and must not conflict with host PostgreSQL on `127.0.0.1:5432`.
- WireGuard tunnel container is privileged and needs `/dev/net/tun`, `NET_ADMIN`, and `NET_RAW`.

## Troubleshooting Load Order
If `api-backend` does not start:
1. Check `postgres-db` health.
2. Check `wireguard-tunnel` started.
3. Check `remote-desktop-guacd` started.
4. Read `Engine/Services/api-backend/logs/error.log`.
5. Read `Engine/Deploy/build.log` if image build changed.

If `traefik-edge` returns `502`:
1. Check `api-backend` health on `127.0.0.1:5000`.
2. Check WebUI listener on `127.0.0.1:8080` for prod or `127.0.0.1:5173` for dev.
3. Check `Engine/Services/traefik-edge/logs/`.
4. Reload Traefik only after confirming backend listeners.

If WebSocket or Socket.IO fails:
1. Check API backend health.
2. Check Traefik routing and access logs.
3. Confirm browser is using same HTTPS origin.
4. Restart `api-backend` only if backend loop is wedged.

If remote desktop fails:
1. Check `remote-desktop-guacd` container.
2. Check `127.0.0.1:4822`.
3. Check `api-backend` VNC WebSocket proxy on `127.0.0.1:4823`.
4. Check WireGuard readiness for target agent.

If remote shell, Ansible, or tunnel-backed operations fail:
1. Check `wireguard-tunnel`.
2. Run WireGuard reconcile.
3. Check `Engine/Services/api-backend/logs/VPN_Tunnel/tunnel.log`.
4. Check target agent VPN logs.

## Codex Agent
- Edit Docker/Compose source under `Data/Engine/Containers/`.
- Do not edit generated runtime under `Engine/` except when reading logs/manifests.
- Use `Engine.sh deploy prod|dev` for full stack deployment.
- Use `Engine.sh --service ...` for scoped service actions.
- Validate launcher syntax after changing shell scripts:
```sh
bash -n Engine.sh
bash -n Borealis.sh
bash -n Update.sh
docker compose -f Data/Engine/Containers/compose.yaml config
```
- Update this page when adding a service, port, volume, service action, or load-order dependency.

## Related Documentation
- [Getting Started](../getting-started.md)
- [Engine Runtime](../engine-runtime.md)
- [Logging and Operations](../logging-and-operations.md)
- [VPN and Remote Access](../vpn-and-remote-access.md)
