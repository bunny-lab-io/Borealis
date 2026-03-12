# Reverse Proxy Functionality

## Summary
This document defines the remaining work required to make Borealis fully reverse-proxy friendly, specifically when deployed behind Traefik.

There are two different meanings of "works behind a reverse proxy" in the current codebase:
- Browser/operator traffic works through a reverse proxy.
- Agent traffic, enrollment, Socket.IO, and WireGuard all work through a reverse proxy without relying on TCP passthrough or direct Engine exposure.

The first goal is mostly achievable today with configuration. The second goal is not fully complete in the current implementation and requires code changes.

This plan is intended to be decision-complete so a future Codex agent can implement it directly.

## Direct Answer
As of this document:
- Web frontend, frontend API calls, and frontend Socket.IO can be made to work behind Traefik.
- VNC can also be made proxy-friendly, but this requires Borealis code changes because the Engine currently generates a direct `:4823` websocket URL.
- Agents are not fully reverse-proxy friendly when Traefik terminates HTTPS and presents a public certificate to the agent.
- Agents can still work today if Traefik is used in TCP passthrough mode for the agent-facing Engine port, because that preserves the Engine certificate the agent expects.

In other words:
- "Browser behind Traefik" is feasible now.
- "Everything behind Traefik with TLS termination at Traefik" is not yet fully implemented.

## Goals
- Make the operator browser experience fully proxy-safe behind Traefik.
- Allow the browser, API, Socket.IO, and noVNC to share one public origin.
- Allow agents to enroll and reconnect through a public reverse-proxy hostname without certificate pinning mismatches.
- Allow WireGuard UDP traffic to be routed through Traefik UDP entrypoints when the operator wants all public exposure to land on Traefik first.
- Preserve direct-Engine deployment as a supported mode.
- Keep the trust model explicit rather than silently downgrading TLS verification.

## Non-Goals
- This does not replace the current Borealis-managed internal TLS mode for direct deployments.
- This does not remove WireGuard or redesign the VPN architecture.
- This does not require a single public port for every transport. Different public ports remain acceptable if the trust model is correct.
- This does not introduce a hard dependency on Traefik-specific APIs inside Borealis. Borealis should remain proxy-friendly in a generic way, with Traefik as the main tested target.

## Current State

### Browser traffic
The browser-facing UI is already mostly same-origin friendly:
- most frontend API calls use relative `/api/...` paths
- frontend Socket.IO uses `window.location.origin`

Relevant file:
- `Data/Engine/web-interface/src/App.jsx`

This means the frontend, API, and frontend Socket.IO can already function behind a proxy as long as:
- the proxy preserves Host and scheme
- `/socket.io` websocket upgrades are forwarded

### Engine proxy awareness
The Engine already applies `ProxyFix`, but only for forwarded host and proto:
- `Data/Engine/server.py`

This is enough for some schemes and host reconstruction, but not comprehensive for forwarded port, forwarded prefix, or chained proxy behavior.

### VNC
The VNC session bootstrap currently returns a websocket URL that points directly to the Engine's standalone VNC websocket listener on port `4823`.

Relevant file:
- `Data/Engine/services/API/devices/vnc.py`

Current behavior:
- browser calls `/api/vnc/establish`
- Engine returns `ws(s)://<engine_host>:4823/vnc?...`
- browser then bypasses the reverse proxy unless `4823` is also exposed

This is the main gap for browser-side reverse proxy support.

### Agent enrollment trust
The largest gap is agent trust behavior.

During enrollment, the Engine always returns its own TLS bundle:
- `Data/Engine/services/API/enrollment/routes.py`

The agent then saves that certificate bundle as the trust material it should use for future Engine HTTPS requests:
- `Data/Agent/agent.py`

This is correct for direct-to-Engine deployments, but it is not correct when:
- the agent talks to `https://public-proxy-host`
- Traefik terminates TLS
- the certificate presented on the wire is Traefik's public certificate, not the Engine certificate

As implemented now, the Engine is telling the agent to trust one certificate chain while the network connection presents another.

### Agent TLS recovery behavior
The agent has websocket-side TLS failure recovery logic that can probe the current certificate and save it:
- `Data/Agent/agent.py`

However:
- this is websocket-specific recovery behavior
- REST enrollment and refresh flows are still designed around the Engine bundle returned by the enrollment API
- this does not constitute a clean or intentionally designed reverse-proxy trust model

### WireGuard endpoint generation
WireGuard endpoint host selection is already derived from the request host:
- `Data/Engine/services/VPN/vpn_tunnel_service.py`
- `Data/Engine/services/API/devices/tunnel.py`
- `Data/Engine/services/API/devices/shell.py`
- `Data/Engine/services/API/devices/vnc.py`

This part is closer to proxy-friendly already. If agents connect through a public proxy hostname, that hostname can be propagated into the WireGuard endpoint generation path.

## What Works Today Without Code Changes

### Supported no-code deployment pattern
The following deployment pattern is considered workable today:
- Browser/UI/API/Socket.IO through Traefik HTTPS on `443`
- Agents through Traefik TCP passthrough on `5000`
- WireGuard through Traefik UDP on `30000`
- VNC either exposed directly on `4823` or separately proxied at Traefik with a dedicated route, but the browser will still receive a direct `:4823` URL until Borealis is changed

This is not full reverse-proxy friendliness, but it is a practical deployment mode.

### Why passthrough works for agents
When Traefik uses TCP passthrough for the agent-facing Engine port:
- the Engine still presents the certificate chain the agent expects
- the enrollment API and the on-wire certificate agree
- existing Borealis trust assumptions remain intact

## Desired End State
The target feature is:

### Browser path
- Operator browses to one public origin, for example `https://borealis.example.com`
- Frontend API calls remain same-origin
- Frontend Socket.IO remains same-origin
- noVNC websocket is reachable through that same origin, for example `/vnc`

### Agent path
- Agent can use a public proxy URL, for example `https://borealis.example.com` or `https://borealis.example.com:5000`
- Traefik may terminate TLS
- Agent enrollment succeeds
- Agent refresh succeeds
- Agent Socket.IO succeeds
- Future public certificate rotation does not silently break agents

### VPN path
- Agent WireGuard endpoint can still use the public hostname and UDP port published at Traefik
- No direct public exposure of the Engine is required if the operator chooses Traefik for all ingress

## Required Product Decisions

### Decision 1: Browser-facing public URL strategy
Add explicit support for public URL generation rather than reconstructing everything from the inbound request.

Required config:
- `BOREALIS_PUBLIC_BASE_URL`
  - canonical public base URL for browser-visible links and absolute URL generation
- `BOREALIS_VNC_WS_PUBLIC_URL`
  - websocket URL or path used for browser VNC connections
  - may be absolute, scheme-relative, or same-origin path

Rationale:
- request-derived reconstruction is useful as a fallback
- explicit public URL config is more predictable and testable

### Decision 2: Agent trust mode
Introduce an explicit agent trust mode instead of always returning the Engine's internal TLS bundle.

Required logical modes:
- `engine_bundle`
  - current behavior
  - use when agents talk directly to the Engine or through TCP passthrough
- `public_bundle`
  - Engine sends a configured public CA bundle that matches the proxy-presented certificate chain
  - use when the proxy cert is not publicly trusted by the OS
- `system`
  - Engine does not send a custom bundle
  - agent uses OS/system trust for the public proxy certificate
  - preferred when Traefik uses a publicly trusted certificate

This should be an Engine-side deployment choice exposed via configuration and returned to the agent during enrollment.

### Decision 3: Agent public URL
The Engine should be able to tell agents what canonical public URL they should use.

Required config:
- `BOREALIS_AGENT_PUBLIC_BASE_URL`

Rationale:
- browser and agent public URLs may differ
- browser may use `443`
- agent may use `5000`
- deployment should not depend on the agent preserving a bootstrap URL forever if the Engine wants to advertise a canonical one

## Implementation Plan

### Phase 1: Centralize public endpoint configuration

#### Engine config additions
Extend `Data/Engine/config.py` and `EngineSettings` with:
- `public_base_url`
- `agent_public_base_url`
- `vnc_ws_public_url`
- `agent_trust_mode`
- `agent_public_ca_bundle_path`
- `proxy_hops`

Suggested environment variables:
- `BOREALIS_PUBLIC_BASE_URL`
- `BOREALIS_AGENT_PUBLIC_BASE_URL`
- `BOREALIS_VNC_WS_PUBLIC_URL`
- `BOREALIS_AGENT_TRUST_MODE`
- `BOREALIS_AGENT_PUBLIC_CA_BUNDLE_PATH`
- `BOREALIS_PROXY_HOPS`

`BOREALIS_AGENT_TRUST_MODE` values:
- `engine_bundle`
- `public_bundle`
- `system`

#### Engine context plumbing
Thread these values through:
- `Data/Engine/server.py`

Do not hide these values in raw config only. Add typed fields to `EngineContext`.

### Phase 2: Improve request proxy awareness

#### ProxyFix
Update `Data/Engine/server.py` to support:
- forwarded IP
- forwarded scheme
- forwarded host
- forwarded port
- forwarded prefix

Use `proxy_hops` rather than hardcoding `1` if practical.

This should support deployments where:
- Traefik is the only proxy
- a second upstream load balancer exists in front of Traefik

### Phase 3: Make browser VNC proxy-friendly

#### VNC URL generation
Update:
- `Data/Engine/services/API/devices/vnc.py`

Required behavior:
- if `BOREALIS_VNC_WS_PUBLIC_URL` is set to a path like `/vnc`, return `wss://<public-host>/vnc?...`
- if it is an absolute websocket URL, return it as-is with the correct query string added
- if it is an absolute http/https URL, translate scheme to ws/wss
- if it is unset, preserve the current fallback behavior using host + `:4823`

Do not strip the `/vnc` path in the proxy layer. Borealis expects the websocket path to remain `/vnc`.

#### Documentation target
After code changes, Traefik should be able to route:
- `Host(borealis.example.com)` -> Engine `:5000`
- `Host(borealis.example.com) && PathPrefix(/vnc)` -> Engine `:4823`

### Phase 4: Make agent enrollment trust proxy-aware

This is the core feature gap.

#### Current behavior to replace
Today enrollment always returns:
- `server_certificate = Engine TLS bundle`

Files:
- `Data/Engine/services/API/enrollment/routes.py`
- `Data/Agent/agent.py`

This must become conditional.

#### Required enrollment response behavior
`/api/agent/enroll/request` and `/api/agent/enroll/poll` should return:
- `server_trust_mode`
- `server_certificate` only when needed
- optionally `server_url` if the Engine wants to advertise a canonical public URL

Required rules:
- `engine_bundle`
  - return Engine bundle exactly as today
- `public_bundle`
  - return the configured public CA bundle
- `system`
  - omit `server_certificate`
  - tell the agent to use system trust

#### Public CA bundle loading
Add a helper on the Engine side to load:
- Engine internal bundle
- configured public CA bundle

Do not overload the existing internal TLS bundle path for this. The public CA trust material may be unrelated to the Engine's own certificate files.

### Phase 5: Teach the agent explicit trust modes

#### Current behavior to replace
Today the agent does this:
- if a certificate bundle exists locally, use it
- otherwise set `requests.Session.verify = False`

This is not suitable for public reverse-proxy deployments.

Required agent behavior:
- persist a trust mode from enrollment
- persist an optional custom CA bundle when provided
- use system trust when `server_trust_mode = system`
- use a provided bundle when `server_trust_mode = public_bundle`
- preserve current pinned Engine bundle behavior when `server_trust_mode = engine_bundle`

Files to modify:
- `Data/Agent/agent.py`

#### Agent verification rules
Implement a clean internal abstraction, for example:
- `trust_mode = "bundle"` with stored PEM bundle path
- `trust_mode = "system"`

Behavior:
- `bundle`
  - `requests.Session.verify = <bundle path>`
  - Socket.IO uses an SSL context seeded with that bundle
- `system`
  - `requests.Session.verify = True`
  - Socket.IO uses the default SSL verification path

Do not use `verify = False` for public proxy mode.

#### Agent certificate refresh behavior
The current websocket TLS self-heal path should be reviewed:
- if trust mode is `system`, probing and saving the presented leaf certificate is not desirable
- if trust mode is `public_bundle`, runtime refresh may be acceptable only if it preserves bundle semantics
- if trust mode is `engine_bundle`, current behavior can remain

Required rule:
- do not replace a stable trust mode with ad hoc leaf-certificate pinning when the deployment expects system-trusted public certificates

### Phase 6: Canonical public URL support for agents

If `BOREALIS_AGENT_PUBLIC_BASE_URL` is set, the Engine should return it during enrollment.

The agent should:
- persist the canonical server URL
- use it for REST and Socket.IO after enrollment

This avoids drift when the bootstrap URL differs from the intended long-term public URL.

### Phase 7: Token refresh compatibility

Review the token refresh path and decide whether it should also be allowed to update:
- trust mode
- CA bundle
- canonical server URL

This is recommended so the Engine can:
- migrate an agent from direct Engine access to proxy access
- rotate from one public hostname to another
- switch from `engine_bundle` to `system`

At minimum, do not make enrollment the only moment when proxy trust settings can be corrected.

### Phase 8: Docs and examples

Update these docs once the code is complete:
- `Docs/getting-started.md`
- `Docs/vpn-and-remote-access.md`
- `Docs/security-and-trust.md`
- `Docs/agent-runtime.md`
- `Docs/engine-runtime.md`

Required documentation outcomes:
- exact Traefik HTTP config for UI/API/Socket.IO
- exact Traefik route for VNC websocket
- exact Traefik TCP passthrough example for agents as a compatibility mode
- exact Traefik UDP example for WireGuard
- explicit statement of which deployment modes are supported

## Recommended Deployment Modes After Implementation

### Mode A: Direct Engine
- Browser and agents talk directly to the Engine certificate
- trust mode: `engine_bundle`

### Mode B: Traefik browser proxy + agent passthrough
- Browser through Traefik `443`
- VNC through Traefik `443` path
- Agents through Traefik TCP passthrough on `5000`
- trust mode: `engine_bundle`

This should remain supported because it is operationally useful and low-risk.

### Mode C: Full reverse-proxy termination
- Browser through Traefik `443`
- VNC through Traefik `443` path
- Agents through Traefik TLS termination on public hostname and port
- trust mode: `system` or `public_bundle`

This is the actual feature to implement.

## Codex Agent Instructions

### Work order
Implement in this order:
1. Engine config and context plumbing.
2. VNC websocket public URL generation.
3. Enrollment response trust mode and bundle selection.
4. Agent trust mode persistence and HTTPS/Socket.IO verification behavior.
5. Canonical agent public URL support.
6. Token refresh compatibility for trust data.
7. Documentation updates.
8. Tests and manual validation.

Do not merge partial work that only fixes browser traffic while leaving agent trust ambiguous unless the PR is explicitly scoped as a compatibility-only subset.

### Files that will require code changes
- `Data/Engine/config.py`
- `Data/Engine/server.py`
- `Data/Engine/services/API/devices/vnc.py`
- `Data/Engine/services/API/enrollment/routes.py`
- `Data/Engine/services/API/tokens/routes.py` if refresh payloads are extended
- `Data/Agent/agent.py`
- possibly `Docs/security-and-trust.md`
- possibly `Docs/vpn-and-remote-access.md`

### Files to inspect while implementing
- `Data/Engine/services/API/devices/tunnel.py`
- `Data/Engine/services/API/devices/shell.py`
- `Data/Engine/services/VPN/vpn_tunnel_service.py`
- `Data/Engine/services/API/server/info.py`

### Things not to do
- Do not disable TLS verification as the reverse-proxy solution.
- Do not hardcode Traefik-specific header names outside generic forwarded-header handling.
- Do not force browser VNC to direct-connect to `:4823` when a public websocket URL is configured.
- Do not make the agent trust a probed leaf certificate in `system` trust mode.
- Do not edit `Engine/` or `Agent/` runtime mirrors directly.

## Acceptance Criteria

### Browser acceptance
- Login page loads through Traefik.
- Authenticated frontend API calls succeed through Traefik.
- Frontend Socket.IO connects through Traefik.
- noVNC works through Traefik without exposing public `:4823`.

### Agent acceptance
- New agent enrollment succeeds through a public proxy hostname with TLS termination at Traefik.
- Existing agent refresh succeeds through that same proxy hostname.
- Agent Socket.IO connects through the proxy hostname.
- Agent survives a public certificate renewal without manual repair when using `system` trust mode.

### VPN acceptance
- Agent receives the correct public hostname in its WireGuard endpoint.
- WireGuard UDP reaches the Engine through Traefik UDP routing.

### Compatibility acceptance
- Direct-to-Engine deployments continue to work.
- TCP passthrough agent mode continues to work.

## Test Plan

### Engine unit tests
Add tests for:
- public VNC websocket URL generation from a same-origin path
- public VNC websocket URL generation from an absolute URL
- enrollment response trust mode selection for:
  - `engine_bundle`
  - `public_bundle`
  - `system`

### Agent unit tests
Add tests for:
- `bundle` trust mode sets `requests.Session.verify` to the bundle path
- `system` trust mode sets `requests.Session.verify = True`
- Socket.IO configuration uses:
  - bundle-backed SSL context in `bundle` mode
  - default verification in `system` mode
- websocket TLS self-heal does not overwrite trust material in `system` mode

### Integration or runtime tests
At minimum, manually validate:
- browser login
- `/api/auth/me`
- frontend Socket.IO
- `/api/vnc/establish`
- noVNC websocket connect through Traefik
- agent enrollment through the public hostname
- agent heartbeat and refresh through the public hostname
- agent Socket.IO through the public hostname
- WireGuard tunnel establishment through Traefik UDP

## Manual Validation Checklist

### Browser
- open the public Borealis URL
- log in
- confirm `/api/auth/me` succeeds
- confirm live UI updates still work

### VNC
- establish a VNC session from the UI
- inspect the returned `ws_url`
- confirm it uses the configured public websocket URL or path
- confirm the browser does not attempt to connect directly to public `:4823`

### Agent
- bootstrap a fresh agent against the public proxy URL
- confirm enrollment completes
- confirm heartbeat appears in the Engine
- confirm Socket.IO registration succeeds
- restart the agent and confirm reconnect succeeds

### Certificate rotation
- simulate or perform a proxy certificate change
- confirm the agent still works when configured for `system` trust mode

## Operational Notes

### Traefik routing model to support
The final Borealis implementation should support at least these Traefik patterns:
- HTTP router for browser traffic on `443`
- HTTP router for VNC websocket path on `443`
- TCP router with passthrough for agent compatibility mode on `5000`
- UDP router for WireGuard on `30000`

### Public port ownership
If Traefik and the Engine run on the same machine and both want the same public port, the Engine will need to bind a different backend port and Traefik will front it. The implementation should not assume the Engine always owns public `:5000`.

## Final Recommendation
Treat "full reverse proxy functionality" as incomplete until the agent trust model is explicitly implemented.

If a future implementation only adds browser/VNC proxy support but leaves agent enrollment tied to the Engine certificate bundle, the feature is still partial and should be documented as such.
