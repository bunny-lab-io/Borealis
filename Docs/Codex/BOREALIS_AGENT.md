# Codex Guide: Borealis Agent

Use this doc for agent-only work (Borealis agent runtime under `Data/Agent` → `/Agent`). For shared guidance, see `Docs/Codex/SHARED.md`.

## Scope & Runtime Paths
- Purpose: outbound-only connectivity, device telemetry, scripting, UI helpers.
- Bootstrap: `Borealis.ps1` preps dependencies, activates the agent venv, and co-launches the Engine (legacy server boot is still available for parity checks).
- Edit in `Data/Agent`, not `/Agent`; runtime copies are ephemeral and wiped regularly.

## Logging
- Primary log: `Agent/Logs/agent.log` with daily rotation to `agent.log.YYYY-MM-DD` (never auto-delete rotated files).
- Subsystems: log to `Agent/Logs/<service>.log` with the same rotation policy.
- Install/diagnostics: `Agent/Logs/install.log`; keep ad-hoc traces (e.g., `system_last.ps1`, ansible) under `Agent/Logs/` to keep runtime state self-contained.
- Troubleshooting: prefix lines with `<timestamp>-<service-name>-<log-data>`; ask operators whether verbose logging should stay after resolution.

## Security
- Generates device-wide Ed25519 keys on first launch (`Certificates/Agent/Identity/`; DPAPI on Windows, `chmod 600` elsewhere).
- Refresh/access tokens are encrypted and pinned to the Engine certificate fingerprint; mismatches force re-enrollment.
- Uses dedicated `ssl.SSLContext` seeded with the Engine TLS bundle for REST + Socket.IO traffic.
- Validates script payloads with backend-issued Ed25519 signatures before execution.
- Outbound-only; API/WebSocket calls flow through `AgentHttpClient.ensure_authenticated` for proactive refresh. Logs bootstrap, enrollment, token refresh, and signature events in `Agent/Logs/`.

## Execution Contexts & Roles
- Auto-discovers roles from `Data/Agent/Roles/`; no loader changes needed.
- Naming: `role_<Purpose>.py` with `ROLE_NAME`, `ROLE_CONTEXTS`, and optional hooks (`register_events`, `on_config`, `stop_all`).
- Standard roles: `role_DeviceInventory.py`, `role_Screenshot.py`, `role_ScriptExec_CURRENTUSER.py`, `role_ScriptExec_SYSTEM.py`, `role_Macro.py`.
- SYSTEM tasks depend on scheduled-task creation rights; failures should surface through Engine logging.

## Platform Parity
- Windows is the reference. Linux (`Borealis.sh`) lags in venv setup, supervision, and role loading; align Linux before macOS work continues.

## Ansible Support (Unfinished)
- Agent + Engine scaffolding exists but is unreliable: expect stalled/silent failures, inconsistent recap, missing collections.
- Windows blockers: `ansible.windows.*` usually needs PSRP/WinRM; SYSTEM context lacks loopback remoting guarantees; interpreter paths vary.
- Treat Ansible features as disabled until packaging/controller story is complete. Future direction: credential mgmt, selectable connections, reliable live output/cancel, packaged collections.
