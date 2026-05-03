# Borealis Docker Stack Breakdown
[Back to Docs Index](../index.md) | [Index (HTML)](../website/index.html)

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
Engine/Deploy/runtime.env
Engine/Deploy/webui-frontend.env
Engine/Deploy/image-manifest.json
Engine/Deploy/deploy-manifest.json
Engine/Deploy/build.log
```

Service runtime state is intentionally sparse:
```text
Engine/Services/api-backend/config
Engine/Services/api-backend/logs
Engine/Services/api-backend/secrets
Engine/Services/api-backend/cache/Ansible
Engine/Services/api-backend/cache/Aurora
Engine/Services/postgres-db/state
Engine/Services/postgres-db/logs
Engine/Services/postgres-db/run
Engine/Services/traefik-edge/config
Engine/Services/traefik-edge/env
Engine/Services/traefik-edge/logs
Engine/Services/traefik-edge/state
Engine/Services/webui-frontend/data/web-interface
Engine/Services/remote-desktop-guacd/logs
Engine/Services/wireguard-tunnel/config
Engine/Services/wireguard-tunnel/logs
Engine/Services/wireguard-tunnel/secrets
Engine/Services/wireguard-tunnel/run
```

Build cache, when Docker Buildx is available, lives under:
```text
Engine/Deploy/cache/buildkit/<service>/
```

Operators should treat `Engine/` as generated runtime state. Edit committed source under `Data/Engine/Containers/`, then redeploy through `Engine.sh`. For live WebUI dev/HMR work, edit the seeded runtime WebUI source under `Engine/Services/webui-frontend/data/web-interface/`.

## Stack Services
| Service | Container | Main responsibility | Host network endpoint |
| --- | --- | --- | --- |
| `postgres-db` | `borealis-engine-postgres-db` | PostgreSQL database and persisted DB state | `127.0.0.1:5432` |
| `wireguard-tunnel` | `borealis-engine-wireguard-tunnel` | Privileged WireGuard interface, peer config, firewall/routing, control socket | UDP `30000`, interface `borealis-wg` |
| `remote-desktop-guacd` | `borealis-engine-remote-desktop-guacd` | VNC-only Apache Guacamole guacd runtime | `127.0.0.1:4822` |
| `webui-frontend` | `borealis-engine-webui-frontend` | Production static WebUI or dev Vite HMR | `127.0.0.1:8000` |
| `api-backend` | `borealis-engine-api-backend` | Flask API, Socket.IO, scheduler, workflows, VNC WebSocket proxy, Ansible control-node logic | `127.0.0.1:5000`, VNC WS `127.0.0.1:4823` |
| `traefik-edge` | `borealis-engine-traefik-edge` | Public HTTP/HTTPS edge, ACME, UI/API/Socket.IO/VNC routing | `80`, `443`, health `127.0.0.1:8082` |

All Engine containers use `network_mode: host`. Loopback assumptions are intentional.

## Reverse Proxy Client IP Preservation
When another reverse proxy sits in front of `traefik-edge`, Borealis must trust only that proxy IP or CIDR. Otherwise all API requests look like they originate from the proxy, and IP-scoped enrollment rate limits can block every agent behind it.

Set these Engine env values before deploy or `traefik-edge` reload:
```text
BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS=192.168.5.29/32
BOREALIS_TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS=
BOREALIS_TRAEFIK_PROXY_PROTOCOL_TRUSTED_IPS=
```

`BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS` is the fallback list for both forwarded headers and PROXY protocol. Use the specific override variables only when HTTP and HTTPS have different upstream proxy paths. Keep the list narrow. Do not use `0.0.0.0/0` or clients can spoof `X-Forwarded-For`.

For HTTP `:80`, an outer HTTP reverse proxy should pass or append `X-Forwarded-For`; embedded Traefik trusts it only when the outer proxy address matches `forwardedHeaders.trustedIPs`.

For HTTPS with TLS passthrough, an outer TCP reverse proxy cannot add HTTP headers. Configure the outer TCP service to send PROXY protocol and configure Borealis embedded Traefik to trust that outer proxy IP:
```yaml
tcp:
  services:
    borealis-websecure:
      loadBalancer:
        proxyProtocol:
          version: 2
        servers:
          - address: "192.168.3.252:443"
```

If the outer proxy is itself behind another load balancer or proxy, configure that outer proxy to trust its upstream client-IP source first. Borealis can preserve only the client IP that reaches the outer proxy.

Deploy examples:
```sh
# Rebuild when the traefik-edge image source changed.
BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS=192.168.5.29/32 bash Engine.sh --service traefik-edge rebuild prod

# Reload is enough for later env-only trust list changes.
BOREALIS_TRAEFIK_TRUSTED_PROXY_IPS=192.168.5.29/32 bash Engine.sh --service traefik-edge reload prod
```

Validate with:
```sh
rg "POST /api/agent/enroll/request" Engine/Services/api-backend/logs/api.log
rg "enrollment rate limited key=ip" Engine/Services/api-backend/logs/device_enrollment.log
```

## Volume Bindings
`api-backend`:
```text
Engine/Services/api-backend -> /opt/Borealis/Engine/Services/api-backend
Engine/Services/traefik-edge/config -> /opt/Borealis/Engine/Services/traefik-edge/config
Engine/Services/traefik-edge/env    -> /opt/Borealis/Engine/Services/traefik-edge/env
Engine/Services/traefik-edge/logs   -> /opt/Borealis/Engine/Services/traefik-edge/logs
Engine/Services/traefik-edge/state  -> /opt/Borealis/Engine/Services/traefik-edge/state
Engine/Services/wireguard-tunnel/config  -> /opt/Borealis/Engine/Services/wireguard-tunnel/config
Engine/Services/wireguard-tunnel/run     -> /opt/Borealis/Engine/Services/wireguard-tunnel/run
Engine/Services/wireguard-tunnel/secrets -> /opt/Borealis/Engine/Services/wireguard-tunnel/secrets
/var/run/docker.sock -> /var/run/docker.sock
```

`api-backend` does not mount the whole `Engine/Services` tree. It receives its own runtime plus specific Traefik and WireGuard paths needed for edge settings and tunnel control. It also receives the Docker socket so the Server Info admin page can read Docker health/state and launch detached helper containers for `Engine.sh --service ...` actions.

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

`webui-frontend`:
```text
Engine/Services/webui-frontend/data/web-interface/src        -> /opt/Borealis/Data/Engine/web-interface/src
Engine/Services/webui-frontend/data/web-interface/public     -> /opt/Borealis/Data/Engine/web-interface/public
Engine/Services/webui-frontend/data/web-interface/Unit_Tests -> /opt/Borealis/Data/Engine/web-interface/Unit_Tests
Engine/Services/webui-frontend/data/web-interface/index.html -> /opt/Borealis/Data/Engine/web-interface/index.html
Engine/Services/webui-frontend/data/web-interface/package.json -> /opt/Borealis/Data/Engine/web-interface/package.json
Engine/Services/webui-frontend/data/web-interface/tsconfig.json -> /opt/Borealis/Data/Engine/web-interface/tsconfig.json
Engine/Services/webui-frontend/data/web-interface/vite.config.mts -> /opt/Borealis/Data/Engine/web-interface/vite.config.mts
```

`Engine.sh` seeds `Engine/Services/webui-frontend/data/web-interface/` from committed WebUI source when the runtime copy is missing. It does not overwrite an existing runtime copy during normal deploys, so dev-mode Vite HMR edits survive rebuilds. Set `BOREALIS_REFRESH_WEBUI_RUNTIME_SOURCE=1` before deploy to discard and reseed the runtime WebUI source from committed source.

## Deploy Order
`Engine.sh deploy [prod|dev]` performs these phases:

1. Parse launch options.
2. If repo/release/branch options were supplied, sync the repository and re-exec installed `Engine.sh`.
3. Install or verify Engine dependencies.
4. Check for host PostgreSQL conflict on `127.0.0.1:5432`.
5. Create service runtime tree under `Engine/Services/`.
6. Seed runtime WebUI source under `Engine/Services/webui-frontend/data/web-interface/` when missing.
7. Prune empty legacy runtime paths.
8. Resolve public hostname and ACME email.
9. Render `Engine/Deploy/runtime.env` for shared container runtime settings, mode-scoped `webui-frontend.env`, and `Engine/Deploy/compose.env` for Compose interpolation.
10. Compute service input hashes from source, Dockerfile, build context, target mode, and dependency inputs.
11. Build changed local images as `borealis-engine/<service>:sha-<hash>`.
12. Write `Engine/Deploy/image-manifest.json`.
13. Re-render `compose.env` with resolved image tags while keeping service runtime env files free of image tag variables.
14. Compare compose/env/image hashes against `Engine/Deploy/deploy-manifest.json`.
15. Skip Compose if nothing changed and all containers are running.
15. Run scoped Compose `up -d --no-deps <service...>` when only service images changed or when switching prod/dev WebUI mode.
16. Run full Compose `up -d` when compose config, shared runtime env, or container state requires it.
17. Write `Engine/Deploy/deploy-manifest.json`.

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

## Local Build Behavior
Engine images are always local in this pass. No registry pull, push, or GHCR workflow is used.

Image naming:
```text
borealis-engine/<service>:sha-<inputhash12>
```

Build cache:
- Docker Buildx uses `Engine/Deploy/cache/buildkit/<service>/` when available.
- Hosts without usable Buildx fall back to `DOCKER_BUILDKIT=1 docker build`.
- `api-backend` keeps repo-root build context because it packages `Data/Agent` and `Borealis.ps1`.
- `webui-frontend`, `traefik-edge`, `postgres-db`, `remote-desktop-guacd`, and `wireguard-tunnel` use service-local build contexts.
- Service-local build contexts carry their own `.dockerignore` files so `node_modules`, WebUI build output, Python bytecode, pytest caches, logs, and local test output stay out of image contexts.
- Deploy mode is part of the image hash only for services with explicit mode targets, currently `webui-frontend`. Switching between prod and dev should not make PostgreSQL, guacd, WireGuard, Traefik, or the API image appear changed unless their own inputs changed.
- `compose.env` carries image tags and stable env-file paths for Compose interpolation.
- `runtime.env` is shared by API, PostgreSQL, guacd, and WireGuard. It intentionally excludes image tag variables and keeps stable production WebUI defaults so one image or mode change does not mutate every container's environment.
- `webui-frontend.env` overrides shared runtime settings with the requested `BOREALIS_WEBUI_MODE`. Switching `prod`/`dev` should recreate only `webui-frontend` when all containers are already running.
- Traefik always routes the WebUI service to `127.0.0.1:8000`; production preview and Vite HMR both bind that same loopback port.

Deploy output:
- Terminal output uses compact service status lines such as `<timestamp> <service>: [Already Up-to-Date]` or `<timestamp> <service>: [(Re)Building]`.
- Compose uses `Applying Stack` when env/Compose metadata changed without image rebuilds.
- Color is enabled only for interactive terminals. Set `NO_COLOR=1` to disable it.
- Successful deploys print `WebUI Accessible @ <public-base-url>`.
- Full Docker build detail remains in `Engine/Deploy/build.log`.

WebUI targets:
- Production builds Docker target `prod`, which runs `npm run build`.
- Dev builds Docker target `dev`, which keeps Vite HMR available and skips production static build work.
- Dev HMR source edits should happen under `Engine/Services/webui-frontend/data/web-interface/`; Compose bind-mounts that runtime copy into the WebUI container.

## Runtime Start Order
Compose dependency order:

1. `postgres-db`, `wireguard-tunnel`, `remote-desktop-guacd`, and `webui-frontend` can start independently.
2. `postgres-db` must pass healthcheck:
```text
pg_isready -h 127.0.0.1 -p 5432 -U "$POSTGRES_USER" -d "$POSTGRES_DB"
```
3. `wireguard-tunnel` must create the Unix control socket.
4. `remote-desktop-guacd` must accept loopback TCP connections on `127.0.0.1:4822`.
5. `webui-frontend` must serve `/` on `127.0.0.1:8000` in prod and dev.
6. `api-backend` waits for:
```text
postgres-db: service_healthy
wireguard-tunnel: service_healthy
remote-desktop-guacd: service_healthy
```
7. `api-backend` must return HTTP `200` from `http://127.0.0.1:5000/health`.
8. `traefik-edge` waits for:
```text
api-backend: service_healthy
webui-frontend: service_healthy
```
9. `traefik-edge` must pass Traefik ping healthcheck on the loopback `borealis-health` entrypoint.

Traefik is the public edge. API and WebUI stay on loopback behind Traefik.

## Production vs Dev Mode
Production mode:
```sh
bash Engine.sh deploy prod
```

Production behavior:
- `BOREALIS_WEBUI_MODE=prod` is scoped to WebUI.
- WebUI frontend serves built static UI.
- Traefik routes public HTTPS to stable loopback services.

Dev mode:
```sh
bash Engine.sh deploy dev
```

Dev behavior:
- `BOREALIS_WEBUI_MODE=dev` is scoped to WebUI.
- WebUI frontend runs Vite HMR.
- Vite listens on loopback `127.0.0.1:8000`.
- Traefik still owns public HTTP/HTTPS and routes UI/API/WebSocket paths without changing its own upstream config.
- API, PostgreSQL, Traefik, guacd, and WireGuard stay running during a prod/dev mode flip unless their own image or shared runtime inputs changed.

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

Server Info service actions use the same command surface. The API backend queues those actions by launching a short-lived helper container from the current `api-backend` image with `/opt/Borealis` and the Docker socket mounted, then returns immediately while the helper runs `Engine.sh --service ...`.

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

WebUI liveness:
```sh
curl -fsS http://127.0.0.1:8000/
```

PostgreSQL readiness:
```sh
pg_isready -h 127.0.0.1 -p 5432 -U borealis -d borealis
```

guacd readiness:
```sh
docker exec borealis-engine-remote-desktop-guacd borealis-guacd-healthcheck
```

Traefik ping:
```sh
docker exec borealis-engine-traefik-edge traefik healthcheck --ping=true --ping.entryPoint=borealis-health --entryPoints.borealis-health.address=127.0.0.1:8082
```

Public edge reachability:
```sh
curl -Ik https://<engine-fqdn>/
```

WireGuard control socket and listener:
```sh
docker exec borealis-engine-wireguard-tunnel borealis-wireguard-healthcheck
docker exec borealis-engine-wireguard-tunnel borealis-wireguard-control-client ping
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
Engine/Services/remote-desktop-guacd/logs/guacd.log
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
- Compose file hash
- env file
- env file hash
- env settings hash excluding image tag and mode-scoped lines
- service image tags and input hashes
- changed services for the last deploy action
- Compose action (`up`, `up-scoped`, or `skipped`)
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
- `Engine.sh deploy` is idempotent for unchanged inputs and skips Compose when deploy manifest, env, image hashes, and running containers already match.
- Unchanged image hashes skip Docker builds.
- Service image changes can use scoped Compose `up -d --no-deps <service...>` when compose config and non-image env settings are unchanged.
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
2. Check WebUI listener on `127.0.0.1:8000`.
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
bash -n Update.sh
docker compose -f Data/Engine/Containers/compose.yaml config
```
- Update this page when adding a service, port, volume, service action, or load-order dependency.

## Related Documentation
- [Getting Started](../Start%20Here/getting-started.md)
- [Engine Runtime](engine-runtime.md)
- [Logging and Operations](../Operations%20and%20Remote%20Access/logging-and-operations.md)
- [VPN and Remote Access](../Operations%20and%20Remote%20Access/vpn-and-remote-access.md)
