# Codex Prompt
Read `Docs/Codex/FEATURE_IMPLEMENTATION_TRACKING/Agent_Reverse_Tunneling.md` and follow the checklist. Preserve existing Engine/Agent behavior, reuse TLS/identity, and implement the dedicated reverse tunnel system (WebSocket-over-TLS) with ephemeral Engine ports and browser-based operator access. Update this ledger with progress, deviations, and next steps before and after changes.

# Context & Goals
- Build a high-performance reverse tunnel between Engine and Agent to carry interactive protocols (first target: interactive PowerShell) while keeping the existing control Socket.IO untouched.
- Agents use a single fixed outbound port to reach the Engine’s tunnel listener; Engine allocates ephemeral ports from 30000–40000 per session.
- Tunnels are operator-triggered, ephemeral, and torn down after inactivity (1h idle) or if the agent stays offline beyond the grace window (1h).
- WebUI-only operator experience: Engine exposes a WebSocket endpoint that bridges browser sessions to agent channels.
- Security: reuse pinned TLS bundle + Ed25519 device identity and short-lived signed tokens per tunnel/channel.
- Concurrency: per-domain caps (one PowerShell session per agent; RDP/VNC/WebRTC grouped to one concurrent session across that domain; WinRM/SSH can scale higher later).
- Logging: Device Activity entries for session start/stop (with operator identity when available) plus `reverse_tunnel.log` on both Agent and Engine.
- Transport: WebSocket-over-TLS for the tunnel socket to keep proxy-friendliness and reuse libraries.
- Motivation: Provide NAT-friendly, high-throughput, operator-initiated remote access (PowerShell first) without touching the existing lightweight control Socket.IO; agents remain outbound-only, Engine leases high ports on demand.

# Background for a New Codex Agent (assumes you read AGENTS.md first)
- AGENTS.md already points you to `BOREALIS_ENGINE.md`, `BOREALIS_AGENT.md`, and shared UI docs; follow their logging paths, runtime locations, and UI rules.
- Keep the existing Socket.IO control channel untouched; this tunnel is a new dedicated listener/port.
- Reuse security: pinned TLS bundle + Ed25519 identity + existing token signing. Agent stays outbound-only; no inbound openings on devices.
- UI reminders specific to this feature: PowerShell page should mirror `Assemblies/Assembly_Editor.jsx` syntax highlighting and general layout from `Admin/Page_Template.jsx` per UI doc.
- Licensing: project is AGPL; pywinpty (MIT) is acceptable but must be attributed in Credits dialog.
- Non-destructive: new code must be gated/dormant until invoked; avoid regressions to existing roles/pages.

# Non-Destructive Expectations
- Do not break existing Agent/Engine comms. New code should be additive and dormant until wired.
- WebUI additions must not impact current pages; new routes/components should be isolated.
- Note any temporary breakage during development here before committing.

# Architecture Plan (High Level)
- Transport: Dedicated WebSocket-over-TLS listener (Engine) on fixed port; Agents dial outbound only.
- Handshake: API on port 443 negotiates an ephemeral tunnel port + token/lease; Agent opens tunnel socket to that port; Engine maps operator channels to agent channels.
- Framing: Binary frames `version | msg_type | channel_id | flags | length | payload`; supports heartbeat, back-pressure, close codes, and resize events for terminals.
- Lease/idle: 1h idle timeout; 1h grace if agent drops mid-session before freeing port.
- PowerShell v1: Agent spawns ConPTY via pywinpty; Engine provides browser terminal bridge with syntax highlighting like `Assembly_Editor.jsx`.

# Terminology & IDs
- agent_id: existing composed ID (hostname + GUID + scope).
- tunnel_id: UUID per tunnel lease.
- channel_id: uint32 per logical stream inside a tunnel (PowerShell uses one channel for stdio + control subframes for resize).
- lease: mapping of tunnel_id -> agent_id -> assigned_port -> expiry/idle timers -> domain/protocol metadata -> token.

# Handshake / Flow (end-to-end)
1) Operator in WebUI clicks “Remote PowerShell” on a device.
2) WebUI calls Engine API (port 443) `POST /api/tunnel/request` with agent_id, protocol=ps, domain=ps, operator_id/context. API:
   - Consults lease manager (port pool 30000–40000). Enforce domain concurrency per agent (PowerShell max 1).
   - Allocates port, tunnel_id, expiry (idle 1h, grace 1h), signs token (binds agent_id, tunnel_id, port, protocol, expires_at).
   - Persists lease (in-memory + db/kv if available) and logs Device Activity (pending).
   - Returns {tunnel_id, port, token, expires_at, idle_seconds, protocol, domain}.
   - Spins up/keeps alive tunnel listener for that port in the async service (engine-side).
3) Engine notifies agent over existing control channel (Socket.IO or REST push) with the lease payload.
4) Agent ReverseTunnel role receives the task, validates token signature (Engine public key) + expiry, opens WebSocket-over-TLS to engine_host:port, sends CONNECT frame {agent_id, tunnel_id, token, protocol, domain, client_version}.
5) Engine listener validates token, binds the WebSocket to the lease, marks agent_connected_at, starts idle timer.
6) Operator browser opens Engine WebUI bridge WebSocket `/ws/tunnel/<tunnel_id>` (port 443) with operator auth. Engine maps browser session to lease and assigns channel 1 for PowerShell.
7) Engine sends CHANNEL_OPEN to agent for channel 1 (protocol ps). Agent starts PowerShell ConPTY, streams stdout/stderr as DATA frames, accepts stdin from browser via Engine bridge.
8) Heartbeats: ping/pong every 20s; idle timer resets on traffic; if idle > 1h, Engine and Agent send CLOSE (code=idle) and free lease/port.
9) If agent disconnects mid-session, lease is held for 1h grace; if reconnects within grace, Engine allows resume, else frees port.
10) On session end (operator closes or process exits), Engine/Agent exchange CLOSE, teardown channel, release port, write Device Activity (stop) and service logs.

# Framing (binary over WebSocket)
- Fixed header (little endian): version(1) | msg_type(1) | flags(1) | reserved(1) | channel_id(4) | length(4) | payload(length).
- msg_types:
  - 0x01 CONNECT (payload: JSON or CBOR {agent_id, tunnel_id, token, protocol, domain, version})
  - 0x02 CONNECT_ACK / CONNECT_ERR (payload: code, message)
  - 0x03 CHANNEL_OPEN (payload: protocol, metadata)
  - 0x04 CHANNEL_ACK / CHANNEL_ERR
  - 0x05 DATA (payload: raw bytes)
  - 0x06 WINDOW_UPDATE (payload: uint32 credits) for back-pressure if needed
  - 0x07 HEARTBEAT (ping/pong)
  - 0x08 CLOSE (payload: code, reason)
  - 0x09 CONTROL (payload: JSON for resize: {cols, rows} or future control)
- Back-pressure: default to simple socket pausing; WINDOW_UPDATE optional for high-rate protocols. Start with pause/resume read on buffers > threshold.
- Close codes: 0=ok, 1=idle_timeout, 2=grace_expired, 3=protocol_error, 4=auth_failed, 5=server_shutdown, 6=agent_shutdown, 7=domain_limit, 8=unexpected_disconnect.

# Port Lease / Domain Policy
- Port pool: 30000–40000 inclusive. Persist active leases (memory + lightweight state file/db row).
- Lease fields: tunnel_id, agent_id, assigned_port, protocol, domain, operator_id, created_at, expires_at, idle_timeout=3600s, grace_timeout=3600s, state {pending, active, closing, expired}, last_activity_ts.
- Allocation: first free port in pool (wrap). Refuse if pool exhausted; emit error to operator.
- Domain concurrency per agent:
  - ps domain: max 1 active tunnel.
  - rdp/vnc/webrtc domain: max 1 active (future).
  - ssh/winrm domain: allow >1 (configurable).
- Idle: reset on any DATA/CONTROL. After 1h idle, send CLOSE, free lease.
- Grace: if agent disconnects, hold lease for 1h; allow reconnect to same tunnel_id/port with valid token; else free.

# Engine Components (detailed)
- Async service module `Data/Engine/services/WebSocket/Agent/ReverseTunnel.py`:
  - Runs asyncio/uvloop TCP/TLS listener factory for the fixed port (or on-demand per allocated port).
  - Manages per-port WebSocket acceptors bound to leases.
  - Validates tokens (Ed25519 or existing JWT signer); ensures agent_id/tunnel_id/port/protocol match.
  - Maintains lease manager (in-memory map + persistence hook).
  - Provides API helpers to allocate/release leases and to push control messages to Agent via existing Socket.IO.
  - Logging to `Engine/Logs/reverse_tunnel.log`.
  - Emits Device Activity entries on session start/stop (with operator id if present).
- API endpoint (port 443) `POST /api/tunnel/request`:
  - Inputs: agent_id, protocol, domain, operator_id (from auth), metadata (e.g., hostname).
  - Output: tunnel_id, port, token, idle_seconds, grace_seconds, expires_at.
  - Checks domain limits, pool availability.
- Browser bridge endpoint `/ws/tunnel/<tunnel_id>`:
  - Auth via existing operator session/JWT.
  - Binds to lease, opens channel 1 to agent, relays DATA/CONTROL, handles CLOSE/idle.
  - Enforces per-operator attach (one active browser per tunnel unless multi-view is allowed; start with one).
- WebUI wiring (later): PowerShell page uses this bridge; status toasts on errors/idle.

# Agent Components (detailed)
- Role file `Data/Agent/Roles/role_ReverseTunnel.py`:
  - Registers control event handler to receive tunnel instructions (payload from Engine API push).
  - Validates token signature/expiry and domain limits.
  - Opens WebSocket-over-TLS to engine_host:assigned_port using existing TLS bundle/identity.
  - Implements framing (CONNECT, CHANNEL_OPEN, DATA, HEARTBEAT, CLOSE).
  - Manages sub-role registry for protocols (PowerShell first).
  - Enforces per-domain concurrency; refuses new tunnel if violation (sends error).
  - Heartbeat + idle tracking; stop_all closes active tunnels cleanly.
  - Logging to `Agent/Logs/reverse_tunnel.log`.
- Submodules under `Data/Agent/Roles/ReverseTunnel/`:
  - `tunnel_Powershell.py`: ConPTY/pywinpty, map stdin/out, handle resize control frames, exit codes.
  - Common helpers: channel dispatcher, back-pressure (pause ConPTY reads if outbound buffer high).

# PowerShell v1 (end-to-end)
- Engine:
  - `Data/Engine/services/WebSocket/Agent/ReverseTunnel/Powershell.py` handles protocol-specific channel setup, translates browser resize/control messages to CONTROL frames, and passes stdin/out via DATA.
  - Integrates with Device Activity logging (start/stop, operator id, agent id, tunnel_id).
- Agent:
  - Spawn PowerShell in ConPTY with configurable shell path if needed; set environment minimal; capture stdout/stderr; forward to channel.
  - Handle EXIT -> send CLOSE with exit code; tear down channel and release lease.
- WebUI:
  - `Data/Engine/web-interface/src/ReverseTunnel/Powershell.jsx` page/modal with terminal UI, syntax highlighting like `Assemblies/Assembly_Editor.jsx`, copy support, status toasts, idle timeout banner.
  - Uses `/ws/tunnel/<tunnel_id>` bridge; shows reconnect/spinner while agent connects; shows errors for domain limit or auth failure.

# Logging & Auditing
- Service logs: `Engine/Logs/reverse_tunnel.log`, `Agent/Logs/reverse_tunnel.log` with tunnel_id/channel_id/agent_id/operator_id.
- Device Activity: add entries on session start/stop with operator info if available, reason (idle timeout, exit, error).
- Metrics (optional later): count active tunnels, per-domain usage, port pool pressure.

# Config Knobs (defaults)
- fixed_tunnel_port: 8443 (or reuse 443 listener with path split if desired).
- port_range: 30000-40000.
- idle_timeout_seconds: 3600.
- grace_timeout_seconds: 3600.
- heartbeat_interval_seconds: 20.
- domain concurrency limits: ps=1, rdp/vnc/webrtc=1 shared, ssh=unbounded/configurable, winrm=unbounded/configurable.
- enable_compression: false (initially).

# Testing & Validation (detailed)
- Engine unit tests: lease manager allocations, domain limit enforcement, idle/grace expiry, token validation, port pool exhaustion.
- Engine integration: simulated agent CONNECT, channel open, data echo, idle timeout, reconnect within grace.
- Agent unit tests: role lifecycle start/stop, token rejection, domain enforcement, heartbeat handling.
- Manual plan: start Engine and Agent in dev; request tunnel via API; agent receives task; tunnel connects; run PowerShell commands; test resize; test idle timeout; test agent drop and reconnect within 1h; test domain limit error when a second PS session is requested.
- WebUI manual: open PS page, run commands, copy output, observe Device Activity entries, see idle timeout banner.

# Credits & Attribution
- Add pywinpty attribution to `Data/Engine/web-interface/src/Dialogs.jsx` CreditsDialog under “Code Shared in this Project.”

# Risks / Watchpoints
- Eventlet vs asyncio coexistence: ensure tunnel service uses dedicated loop/thread/process to avoid blocking existing Socket.IO handlers.
- Port exhaustion: detect and return meaningful errors; ensure cleanup on process exit.
- Buffer growth: enforce back-pressure; pause reads when output queue high.
- Packaging: pywinpty wheels availability for supported Python versions; note in doc if build needed.
- Security: strict token binding (agent_id, tunnel_id, port, protocol, expiry); reject mismatches; always TLS.

# Next Actions (on approval)
- Document handshake and framing (done above) — refine in code comments.
- Scaffold Engine tunnel service + lease manager + logging (no wiring to main app yet).
- Scaffold Agent role + PowerShell submodule (dormant until enabled).
- Add WebUI PowerShell page and CreditsDialog attribution.
- Wire negotiation API to lease manager; push control payload to Agent; wire browser bridge to tunnel service.

# Implementation Sequence (follow in order)
1) Read project docs (`Docs/Codex/BOREALIS_ENGINE.md`, `Docs/Codex/BOREALIS_AGENT.md`, `Docs/Codex/SHARED.md`, `Docs/Codex/USER_INTERFACE.md`).
2) Add config defaults (fixed tunnel port, port range, timeouts) without enabling by default.
3) Build Engine lease manager (allocations, domain rules, idle/grace timers, persistence hook) with unit tests.
4) Implement framing helper (encode/decode headers, heartbeats, close codes, back-pressure hooks).
5) Stand up Engine async WebSocket-over-TLS listener (fixed port), token validation, and per-lease bindings.
6) Implement negotiation API `/api/tunnel/request`; sign tokens; push control payload to Agent via existing channel.
7) Add Engine browser bridge `/ws/tunnel/<tunnel_id>`; relay to agent channel; enforce auth and single attachment.
8) Scaffold Agent role `role_ReverseTunnel.py` (token verify, connect, channel dispatch, heartbeat, stop_all, domain limits).
9) Implement Agent PowerShell submodule with ConPTY/pywinpty, resize, stdout/stderr piping, exit handling.
10) Implement Engine PowerShell handler to translate browser events and route frames.
11) Build WebUI PowerShell page `ReverseTunnel/Powershell.jsx` with terminal UI, syntax highlighting, status/idle handling.
12) Wire Device Activity logging for session start/stop; surface in Device Activity tab.
13) Add Credits dialog attribution for pywinpty.
14) Run tests (unit + manual end-to-end); verify idle/grace, domain limits, resize, reconnect.
15) Gate feature (config off by default), clean up logs, update this ledger with status and deviations.

# Handoff Notes for the Next Codex Agent
- Treat this file as the single source of truth for the tunnel feature; document deviations and progress here.
- Keep changes additive; do not modify unrelated Socket.IO handlers or existing roles/pages.
- Maintain outbound-only design for Agents; Engine listens on fixed + leased ports.
- Performance matters: use asyncio/uvloop for tunnel service; avoid blocking eventlet paths; add basic back-pressure.
- Security is mandatory: TLS + signed tokens bound to agent_id/tunnel_id/port/protocol/expiry; close on mismatch or framing errors.

# Execution Protocol for the Codex Agent
- Work one checklist item at a time.
- After finishing an item, mark it in the Detailed Checklist and briefly summarize what changed.
- Before starting the next item, ask the operator for permission to proceed. Pause if permission is not granted.
- If you must reorder items (e.g., dependency), note the rationale here before proceeding.
- Keep the codebase functional at all times. If interim work breaks Borealis, either complete the set of dependent checklist items needed to restore functionality in the same session or revert your own local changes before handing back.
- Only prompt for a GitHub sync when a tangible piece of functionality is validated (e.g., API call works, tunnel connects, UI interaction tested). Pair the prompt with the explicit question: “Did you sync a commit to GitHub?” after validation or operator testing.
# Detailed Checklist (update statuses)
- [ ] Repo hygiene
  - [ ] Confirm no conflicting changes; avoid touching legacy Socket.IO handlers.
  - [ ] Add pywinpty (MIT) to Agent deps (note potential packaging/test impact).
- [ ] Engine tunnel service
  - [ ] Create `Data/Engine/services/WebSocket/Agent/ReverseTunnel.py` (async/uvloop listener, port pool 30000–40000).
  - [ ] Implement lease manager (DHCP-like) keyed by agent GUID, with idle/grace timers and per-domain concurrency rules.
  - [ ] Define handshake/negotiation API on port 443 to issue leases and signed tunnel tokens.
  - [ ] Implement channel framing, flow control, heartbeats, close semantics.
  - [ ] Logging: `Engine/Logs/reverse_tunnel.log`; audit into Device Activity (session start/stop, operator id, agent id, tunnel_id, port).
  - [ ] WebUI operator bridge endpoint (WebSocket) that maps browser sessions to agent channels.
- [ ] Agent tunnel role
  - [ ] Add `Data/Agent/Roles/role_ReverseTunnel.py` (manages tunnel socket, reconnect, heartbeats, channel dispatch).
  - [ ] Per-protocol submodules under `Data/Agent/Roles/ReverseTunnel/` (first: `tunnel_Powershell.py`).
  - [ ] Enforce per-domain concurrency (one PowerShell; prevent multiple RDP/VNC/WebRTC; allow extensible policies).
  - [ ] Logging: `Agent/Logs/reverse_tunnel.log`; include tunnel_id/channel_id.
  - [ ] Integrate token validation, TLS reuse, idle teardown, and graceful stop_all.
- [ ] PowerShell v1 (feature target)
  - [ ] Engine side `Data/Engine/services/WebSocket/Agent/ReverseTunnel/Powershell.py` (channel server, resize handling, translate browser events).
  - [ ] Agent side `Data/Agent/Roles/ReverseTunnel/tunnel_Powershell.py` using ConPTY/pywinpty; map stdin/stdout to frames; handle resize and exit codes.
  - [ ] WebUI: `Data/Engine/web-interface/src/ReverseTunnel/Powershell.jsx` with terminal UI, syntax highlighting matching `Assemblies/Assembly_Editor.jsx`, copy support, status toasts.
  - [ ] Device Activity entries and UI surface in `Devices/Device_List.jsx` Device Activity tab.
- [ ] Credits & attribution
  - [ ] If third-party libs used (e.g., pywinpty), add attribution in `Data/Engine/web-interface/src/Dialogs.jsx` CreditsDialog under “Code Shared in this Project”.
- [ ] Testing & validation
  - [ ] Unit/behavioral tests for lease manager, framing, and idle teardown (Engine side).
  - [ ] Agent role lifecycle tests (start/stop, reconnect, single-session enforcement).
  - [ ] Manual test plan: request port, start PowerShell session, send commands, resize, idle timeout, offline grace recovery, concurrent domain policy.
- [ ] Operational notes
  - [ ] Document config knobs: fixed tunnel port, port range, idle/grace durations, domain concurrency limits.
  - [ ] Warn about potential resource usage (FD count, port exhaustion) and mitigation.
