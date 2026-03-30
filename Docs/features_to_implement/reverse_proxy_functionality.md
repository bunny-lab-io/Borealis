# Reverse Proxy Functionality
[Back to Docs Index](../index.md) | [Index (HTML)](../index.html)

## Purpose
Define the Borealis target architecture for public HTTPS, Let's Encrypt, reverse proxy passthrough, split-horizon DNS, and same-origin remote desktop access.

## Status
- Planned target state. This document captures the intended implementation direction as of 2026-03-29.

## Goals
- Use one public FQDN for the Borealis control plane.
- Use Let's Encrypt issued public certificates for all engine-facing browser and agent HTTPS traffic.
- Remove the Borealis private CA flow for the Engine WebUI and VNC browser path.
- Support split-horizon DNS so the same FQDN can resolve differently on public and internal networks.
- Keep external reverse proxies optional and transparent by using port-forwarding or TCP passthrough rather than owning Borealis TLS identity.
- Move browser VNC to a same-origin path instead of exposing a public `:4823` port.
- Support certificate renewal without restarting the Borealis Engine process.
- Keep WireGuard as a direct UDP service on the engine host.

## Non-Goals
- Wildcard certificate support in the first implementation.
- Manual certificate management or operator-provided PEM workflows.
- A legacy self-signed Engine fallback mode.
- Moving all agent control-plane traffic onto WireGuard in the same phase as the TLS redesign.

## Locked Decisions
- Borealis uses Let's Encrypt only for the Engine public surface.
- The public control plane uses one FQDN, such as `https://borealis.example.com`.
- External reverse proxies are on separate hosts and act as transparent forwarders, not as the primary TLS authority for Borealis.
- Split-horizon DNS is a supported deployment pattern.
- WireGuard remains directly exposed on the engine host at UDP 30000.
- Browser VNC should be exposed on a same-origin path such as `/remote-desktop/vnc`.
- Agents should trust the public CA chain and hostname rather than pinning the public Engine leaf certificate.

## Target Topology
### Public path
1. Public DNS resolves the Borealis FQDN to an external reverse proxy or firewall edge.
2. Port 80 is forwarded to Traefik on the engine host so HTTP-01 ACME challenges can succeed.
3. Port 443 is TCP-passthrough forwarded to Traefik on the engine host.
4. Traefik on the engine host terminates HTTPS using Let's Encrypt managed certificates.
5. Traefik forwards HTTP/WebSocket traffic to loopback services on the engine host.

### Internal path
1. Split-horizon DNS resolves the same Borealis FQDN directly to the engine host.
2. Internal operators and agents connect directly to Traefik on the engine host.
3. Browsers and agents still see the same public certificate and hostname.

### Engine host services
- Traefik listens on public TCP 80 and 443.
- Borealis Engine listens on loopback only, for example `127.0.0.1:5000`.
- Borealis VNC proxy listens on loopback only, for example `127.0.0.1:4823`.
- WireGuard listens directly on UDP 30000.

## Why Traefik Instead of Certbot
- Traefik already combines HTTP-01 challenge handling, certificate storage, automatic renewal, HTTP routing, WebSocket proxying, and hot-reload behavior.
- The current Borealis Engine runtime loads TLS material at startup and does not expose a hot certificate reload path.
- If the Python Engine process remains the public TLS terminator, Borealis must either restart the Engine on renewal or grow a custom TLS reload mechanism.
- If Traefik is the Borealis-managed public TLS endpoint, Borealis can meet the hot-reload requirement without forcing the Engine itself to rebind TLS listeners.

## Borealis-Owned Runtime State
- `Engine/LetsEncrypt/Settings.json`
- `Engine/LetsEncrypt/acme.json`
- `Engine/Traefik/traefik.yml`
- `Engine/Traefik/dynamic/`
- `Engine/Logs/Traefik/`

These paths live inside the Borealis runtime tree so `Borealis.sh` and `Update.sh` can preserve them during staging and upgrades.

## Proposed `Settings.json` Shape
```json
{
  "enabled": true,
  "fqdn": "borealis.example.com",
  "email": "ops@example.com",
  "use_staging_ca": false,
  "split_horizon_enabled": true,
  "external_reverse_proxy_expected": true,
  "allow_direct_internal_access": true,
  "http_entrypoint_port": 80,
  "https_entrypoint_port": 443,
  "wireguard_public_port": 30000,
  "engine_upstream": "http://127.0.0.1:5000",
  "vnc_upstream": "http://127.0.0.1:4823",
  "public_base_url": "https://borealis.example.com",
  "public_vnc_path": "/remote-desktop/vnc",
  "traefik_acme_storage_path": "/opt/Borealis/Engine/LetsEncrypt/acme.json"
}
```

## Traefik Responsibilities
- Own the public TLS certificate lifecycle for the Borealis FQDN.
- Answer HTTP-01 challenges on port 80.
- Redirect non-challenge HTTP traffic to HTTPS.
- Route `/`, static assets, `/api`, and `/socket.io` to the Engine upstream.
- Route `/remote-desktop/vnc` to the internal VNC WebSocket upstream.
- Reload configuration and renewed certificates without restarting the Engine.

## Borealis Engine Responsibilities
- Bind only to loopback in Let's Encrypt mode.
- Stop generating or expecting a Borealis private CA for Engine browser trust.
- Stop emitting public `wss://host:4823/...` URLs for browser VNC.
- Prefer relative or same-origin URLs for browser-facing WebSocket paths.
- Expose explicit public endpoint configuration for WireGuard instead of inferring it only from forwarded headers.

## Agent Trust Model
### Target state
- Agents use the configured FQDN in `server_url.txt`.
- Agents trust the public CA chain and perform normal hostname validation.
- Agents do not store or refresh a pinned public Engine certificate in Let's Encrypt mode.

### Implications
- Enrollment no longer needs to return `server_certificate` for the public Engine path.
- Token refresh and Socket.IO connections use the platform CA store plus hostname validation.
- Direct IP-based Engine URLs should be treated as unsupported in Let's Encrypt mode unless the public certificate explicitly covers the IP address.

## VNC Changes
### Current issues to address
- The browser is told to connect to `ws(s)://<engine_host>:4823/vnc`.
- The UI contains operator guidance for downloading and trusting a Borealis private CA.
- The VNC proxy accepts `agent_id` fallback instead of requiring a one-time session token.

### Planned target
- Browser VNC uses the same Borealis FQDN and a relative path such as `/remote-desktop/vnc`.
- Traefik proxies that path to the loopback VNC proxy service.
- The Engine returns a one-time VNC session token, not a raw `agent_id` query fallback.
- The CA trust warning panel and root CA download workflow are removed.

## Reverse Proxy Behavior
### Supported patterns
- Public edge firewall or reverse proxy on a different host forwards port 80 to engine-host Traefik.
- Public edge firewall or reverse proxy on a different host TCP-passthrough forwards port 443 to engine-host Traefik.
- Internal DNS resolves the Borealis FQDN directly to the engine host.

### Unsupported target behavior
- Public reverse proxies becoming the primary TLS authority for Borealis.
- A separate public VNC certificate path or extra browser-visible VNC port.

## WireGuard Behavior
- WireGuard remains a direct UDP listener on the engine host.
- The Borealis public endpoint model should treat WireGuard separately from HTTP routing.
- Traefik UDP forwarding is technically possible, but Borealis should not require it for the supported topology.

## Migration Plan
### Phase 1: Runtime and settings scaffolding
1. Add `Engine/LetsEncrypt/Settings.json` parsing and validation helpers.
2. Add runtime preservation rules for `Engine/LetsEncrypt` and `Engine/Traefik`.
3. Add `Borealis.sh` logic to install and configure Traefik per distro.
4. Add a Borealis-managed `borealis-traefik.service` systemd unit.

### Phase 2: Public edge routing
1. Generate Traefik static and dynamic configuration from `Settings.json`.
2. Enable HTTP-01 challenge handling and HTTP-to-HTTPS redirect behavior.
3. Bind the Engine service to loopback only.
4. Update health checks and logging to include Traefik state.

### Phase 3: Browser VNC redesign
1. Move browser VNC to `/remote-desktop/vnc`.
2. Use one-time VNC session tokens instead of `agent_id` fallback.
3. Remove public VNC port assumptions from the UI and API.
4. Remove browser trust-the-CA guidance.

### Phase 4: Agent trust simplification
1. Stop sending the public Engine certificate bundle during enrollment in Let's Encrypt mode.
2. Remove public Engine certificate pinning logic from the agent.
3. Require FQDN-based Engine URLs for Let's Encrypt mode.
4. Validate refresh, Socket.IO, updater, and enrollment flows against standard public trust.

### Phase 5: Cleanup and documentation
1. Remove the Engine private CA download endpoint.
2. Retire Engine private-CA wording from the docs.
3. Update `engine-runtime.md`, `vpn-and-remote-access.md`, `security-and-trust.md`, and `api-reference.md`.
4. Update `SBOM.md` if Traefik becomes a managed runtime dependency.

## Rollout Guardrails
- Use the ACME staging CA during first-run validation or while iterating on route generation.
- Do not reissue production certificates on every `Borealis.sh` run.
- Treat `Settings.json` as the source of truth for whether the ACME runtime is already configured.
- Keep public TLS entirely on Traefik so certificate renewal does not force Engine restarts.

## Verification Checklist
- Port 80 challenge path succeeds through the external forwarder and via direct internal access.
- Port 443 passthrough reaches engine-host Traefik and presents the expected public certificate.
- Borealis WebUI loads successfully from the public FQDN both externally and internally.
- Socket.IO connects successfully through Traefik using the public FQDN.
- VNC opens successfully on the same FQDN path without a public `:4823` connection.
- Agent enrollment, token refresh, updater calls, and Socket.IO all succeed without pinned Engine cert artifacts.
- WireGuard continues to connect to the engine host on UDP 30000.

## Related Documentation
- [Engine Runtime](../engine-runtime.md)
- [VPN and Remote Access](../vpn-and-remote-access.md)
- [Security and Trust](../security-and-trust.md)
- [Logging and Operations](../logging-and-operations.md)

## Codex Agent (Detailed)
### Planning assumptions
- This document describes the target design, not the current runtime.
- The current repository still contains self-signed Engine TLS generation and a browser-visible VNC TLS port.
- Use this plan when sequencing implementation work; do not assume the existing docs already describe the target state.

### Expected code areas to touch
- `Borealis.sh`
- `Update.sh`
- `Data/Engine/bootstrapper.py`
- `Data/Engine/config.py`
- `Data/Engine/security/certificates.py`
- `Data/Engine/services/API/devices/vnc.py`
- `Data/Engine/services/RemoteDesktop/vnc_proxy.py`
- `Data/Engine/services/API/server/info.py`
- `Data/Agent/agent.py`
- `Data/Agent/security.py`
- `Data/Agent/update_helper.py`
- `Data/Engine/web-interface/src/Devices/Tabs/VNC.jsx`

### Important implementation notes
- Do not try to make Traefik ACME storage and the Python Engine co-own the same public certificate lifecycle.
- If Traefik is the Borealis-managed TLS endpoint, keep the Engine behind loopback and simplify the Engine runtime.
- Preserve explicit WireGuard endpoint configuration because HTTP forwarded-host inference is not sufficient for all topologies.
