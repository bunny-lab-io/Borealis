# Reverse VPN Tunnel Deployment Plan (WireGuard/UDP) – Windows First

Use this checklist to rebuild Borealis reverse tunnels as a WireGuard-based, host-only, single-tunnel-per-agent system. This is written for a Codex agent who will implement the migration; the operator expects milestone checkpoints and commits. Read `AGENTS.md` and `Docs/Codex/REVERSE_TUNNELS.md` first to understand the current stack you are replacing. **Implement Windows first. Do not implement Linux yet; see the separate Linux section for later execution.**

## Context: Why this change
- Current tunnels: WebSocket/TLS framing, domain lanes (2/1/2), per-protocol handlers, custom leases, idle/grace timers.
- Desired state: one outbound WireGuard/UDP tunnel per agent, host-only reachability, multiplex any protocol (RDP, WinRM/PS, SSH, VNC/WebRTC, etc.) over a single VPN session. No legacy domains/limits, no fallback to WebSocket tunnels.
- Constraints: UDP is available (operators can open firewall). Use UDP port **30000** for the VPN server (not 443). Outbound-only from agents, idle timeout 15 minutes, no grace period, immediate teardown on operator exit/stop. Client-to-client disallowed; only engine↔agent virtual /32.
- Packaging: Admin rights available. Standardize on WireGuard with the official Windows driver/client. The adapter installs at agent bootstrap and persists; sessions are ephemeral and started on demand.
- Keys/Certs: Prefer reusing existing Engine/Agent certificate infrastructure for orchestration token signing/validation. WireGuard still needs its own keypairs; if reuse paths are impossible, store VPN server keys under `Engine/Certificates/VPN_Server` and client keys under `Agent/Borealis/Certificates/VPN_Client`.

## High-Level Outcomes (Windows first)
- Engine runs a WireGuard listener on UDP port 30000 (dedicated).
- One live VPN tunnel per agent enforced server-side; multiple operators piggyback on the same tunnel.
- Engine issues short-lived session material (token + client config + ephemeral or pre-provisioned keys) per connect request; server rejects clients without a fresh orchestration token.
- Host-only routing: assign per-agent /32; AllowedIPs limited to the agent /32; no LAN routes. Engine firewall/ACL blocks client-to-client and can restrict engine→agent ports per device defaults and operator overrides.
- APIs: `/api/tunnel/connect`, `/api/tunnel/status`, `/api/tunnel/disconnect`. Agent receives start/stop signals analogous to current `reverse_tunnel_start/stop`.
- Logging and audit stay in place (use `reverse_tunnel.log` or a renamed equivalent consistently on Engine/Agent).
- UI: `Data/Engine/web-interface/src/Devices/Device_Details.jsx` gets an “Advanced Config” tab for per-agent allowed ports; `Data/Engine/web-interface/src/Devices/ReverseTunnel/Powershell.jsx` is reused for a live PowerShell MVP wired to the new APIs.

## Milestone Checkpoints (commit names, Windows first)
- Milestone: Dependencies & Bootstrap (Windows)
- Milestone: Engine VPN Server & ACLs (Windows)
- Milestone: Agent VPN Client & Lifecycle (Windows)
- Milestone: API & Service Orchestration (Windows)
- Milestone: UI Advanced Config & Operator Flow (Windows, PowerShell MVP)
- Milestone: Legacy Tunnel Removal & Cleanup (Windows)
- Milestone: End-to-End Validation (Windows)

At each milestone: pause, run the listed checks, talk to the operator, and commit with the milestone name.

## Detailed Steps — Windows Implementation

### 1) Dependencies & Bootstrap — Milestone: Dependencies & Bootstrap (Windows)
- Agents editing this document should mark tasks they complete with `[x]` (leave `[ ]` otherwise).
- WireGuard packaging:
  - [x] Bundle official WireGuard for Windows (driver + client).
  - [x] Download installers into `Dependencies/VPN_Tunnel_Adapter/` and keep them there (no deletion) for ad-hoc reinstalls.
- Update `Borealis.ps1`:
  - [x] Install/verify WireGuard driver/client idempotently with admin rights.
  - [x] Log to `Agent/Logs/install.log`.
  - [x] Do not start any tunnel yet.
- Linux: do nothing yet (see later section).
- Checkpoint tests:
  - [x] WireGuard binaries available in agent runtime.
  - [x] WireGuard driver installed and visible.

### 2) Engine VPN Server & ACLs — Milestone: Engine VPN Server & ACLs (Windows)
- Agents editing this document should mark tasks they complete with `[x]` (leave `[ ]` otherwise).
- Configure WireGuard listener on UDP port 30000; bind only on engine host. [x]
- Server config:
  - [x] Assign per-agent virtual IP (/32). Use AllowedIPs to restrict each peer to its /32.
  - [x] Disable client-to-client by not including other peers’ networks in AllowedIPs.
  - [x] Do not push DNS or LAN routes; host-only reachability engine IP ↔ agent virtual /32.
- ACL layer:
  - [x] Default allowlist per agent derived from OS (Windows: RDP 3389, WinRM 5985/5986, PS remoting ports; include VNC/WebRTC defaults as desired).
  - [x] Allow operator overrides per agent; enforce at engine firewall layer. (rule plans produced; application wiring pending)
- Keys/Certs:
  - [x] Prefer reusing existing Engine cert infrastructure for signing orchestration tokens. Generate WireGuard server key and store it; if reuse paths are impossible, place under `Engine/Certificates/VPN_Server`.
  - [x] Session token binding: require fresh orchestration token (tunnel_id/agent_id/expiry) validated before accepting a peer (e.g., via pre-shared keys or control-plane validation before adding peer).
- Logging: server logs to `Engine/Logs/reverse_tunnel.log` (or renamed consistently). [x]
- Checkpoint tests:
  - [x] Engine starts WireGuard listener locally on 30000.
  - [x] Only engine IP reachable; client-to-client blocked.
  - [x] Peers without valid token/key are rejected.

### 3) Agent VPN Client & Lifecycle — Milestone: Agent VPN Client & Lifecycle (Windows)
- Agent config template:
  - Outbound UDP to engine:30000.
  - No DNS/routing changes beyond the /32 to engine.
  - Adapter persists; sessions start/stop on demand.
- Lifecycle in agent role (replace legacy reverse tunnel role):
  - Receive connect request, fetch session token + WG peer config (keys, endpoint, allowed IPs), start WireGuard.
  - Enforce single session per agent; reject/dismiss concurrent starts.
  - Idle timeout: 15 minutes of no operator activity triggers disconnect. No grace period; operator disconnect triggers immediate stop.
  - Stop path: remove peer/bring interface down cleanly; adapter remains installed.
- Keys/Certs:
  - Prefer reusing existing Agent cert infrastructure for token validation; generate WG client key per agent. If reuse paths are impossible, store under `Agent/Borealis/Certificates/VPN_Client`.
- Logging: `Agent/Logs/reverse_tunnel.log` captures connect/disconnect/errors/idle timeouts.
- Checkpoint tests:
  - Manual connect/disconnect against engine test server.
  - Idle timeout fires at ~15 minutes of inactivity.

### 4) API & Service Orchestration — Milestone: API & Service Orchestration (Windows)
- Replace legacy tunnel APIs with:
  - `POST /api/tunnel/connect` → tunnel_id, token, WG client config (keys, endpoint, allowed IPs), virtual IP, idle_seconds (900).
  - `GET /api/tunnel/status` → up/down, virtual IP, connected operators.
  - `DELETE /api/tunnel/disconnect` → immediate teardown and lease release.
- Engine orchestrator:
  - Manages single tunnel per agent; tracks tunnel_id, virtual IP, token expiry.
  - Emits start/stop signals to agent (rename events as needed).
  - Cleans peer/routing state on stop.
- Token issuance: short-lived, binds agent_id/tunnel_id/port/expiry; validated before adding peer.
- Remove domain limits; remove channel/protocol handler registry for tunnels.
- Checkpoint tests:
  - API happy path: connect → status → disconnect.
  - Reject stale/second connect for same agent while active.

### 5) UI Advanced Config & Operator Flow (PowerShell MVP) — Milestone: UI Advanced Config & Operator Flow (Windows, PowerShell MVP)
- In `Data/Engine/web-interface/src/Devices/Device_Details.jsx`, add “Advanced Config” tab:
  - “Reverse VPN Tunnel - Allowed Ports” with toggles per protocol.
  - Defaults by OS (Windows: RDP/WinRM/PS; All: VNC/WebRTC; allow operator overrides).
- PowerShell MVP:
  - Reuse `Data/Engine/web-interface/src/Devices/ReverseTunnel/Powershell.jsx` as the base UI.
  - Rewire to new APIs and virtual IP flow.
  - Keep live web terminal behavior (WebSocket or equivalent) so operator input streams to remote PowerShell and outputs stream back in real time over the VPN tunnel.
  - Ensure tunnel is up via `/api/tunnel/connect/status` before opening the terminal; call `/api/tunnel/disconnect` on exit/tab close.
- Later protocols (RDP/SSH/etc.) can follow once MVP is proven, but do not block on them for this milestone.
- Checkpoint tests:
  - UI can start a tunnel, launch PowerShell terminal, send commands, receive live output, and tear down.
  - Toggles change ACL behavior (engine→agent reachability) as expected.

### 6) Legacy Tunnel Removal & Cleanup — Milestone: Legacy Tunnel Removal & Cleanup (Windows)
- Remove/retire:
  - Engine `reverse_tunnel_orchestrator` and domain handlers under `Data/Engine/services/WebSocket/Agent/Reverse_Tunnels/`.
  - Agent `role_ReverseTunnel.py` and protocol handlers.
  - WebUI components tied to the old Socket.IO tunnel namespace.
- Update docs and references to point to the new WireGuard VPN flow; keep change log entries.
- Ensure no lingering domain limits/config knobs remain.
- Checkpoint tests:
  - Codebase builds/starts without references to legacy tunnel modules.
  - UI no longer calls old APIs or Socket.IO tunnel namespace.

### 7) End-to-End Validation — Milestone: End-to-End Validation (Windows)
- Functional:
  - Windows agent: WireGuard connect on port 30000; PowerShell MVP fully live in the web terminal; RDP/WinRM reachable over tunnel as configured.
  - Idle timeout at 15 minutes; operator disconnect stops tunnel immediately.
- Security:
  - Client-to-client blocked.
  - Only engine IP reachable; per-agent ACL enforces allowed ports.
  - Token enforcement blocks stale/unauthorized sessions.
- Resilience:
  - Restart engine: WireGuard server starts; no orphaned routes.
  - Restart agent: adapter persists; tunnel stays down until requested.
- Logging/audit:
  - Connect/disconnect/idle/stop reasons recorded in reverse_tunnel.log (Engine/Agent) and Device Activity.
- Checkpoint tests:
  - Run the above matrix; gather logs for operator review before final commit.

## Linux (Deferred) — Do Not Implement Yet
- When greenlit, mirror the structure above for Linux:
  - WireGuard (kernel module preferred) on UDP 30000; userspace fallback if needed.
  - Per-agent keys; reuse cert infrastructure for token signing/validation if possible; otherwise dedicated `Engine/Certificates/VPN_Server` and `Agent/Borealis/Certificates/VPN_Client`.
  - Same APIs/UI, same idle/teardown semantics.
  - Validate SSH/Bash over tunnel for Linux devices.
- Add new milestones for Linux when the operator approves.

## Cautions and Gotchas
- Use UDP 30000 for WireGuard; do not use 443.
- Ensure WireGuard driver install is robust and idempotent; keep installers in `Dependencies/VPN_Tunnel_Adapter/`.
- Idle enforcement must be tied to operator activity, not just socket liveness—ensure operator-side clients signal activity.
- Keep adapters installed but sessions ephemeral; stop path must tear down the tunnel without removing the driver.
- Preserve logging paths and headers per domain docs.
- Do not leave any legacy domain-limit logic or protocol-channel framing in the new stack.
- Be explicit about token validation before adding peers to the WireGuard interface.

## Operator Check-Ins
- After each milestone, present: what changed, tests run/results, any open risks. If green, commit with the milestone name as specified.
- If unexpected existing changes appear in git status, pause and ask the operator before proceeding.
