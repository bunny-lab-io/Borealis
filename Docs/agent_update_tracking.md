# Agent Update Tracking Options

## Current Flow (Summary)
- A scheduled job triggers the **Remote Agent Update [WIN]** PowerShell script, which resolves the agent's working directory and invokes `Borealis.ps1 -SilentUpdate` from the repository root.【F:Assemblies/Scripts/Borealis/Remote_Agent_Update_WIN.json†L1-L46】
- `Borealis.ps1 -SilentUpdate` reads the service agent ID from `Agent\Borealis\Settings\agent_settings_svc.json`, queries the server's `/api/agent/update_check` endpoint, and only proceeds when the server reports that the agent hash differs from the cached GitHub head.【F:Borealis.ps1†L840-L931】【F:Data/Server/server.py†L127-L221】
- When an update runs, the script finishes by reading the local `.git` HEAD to determine the commit it just deployed and POSTs that value (along with the agent ID) to `/api/agent/agent_hash`. The server stores the commit in `device_details.agent_hash` and updates any in-memory registry entries.【F:Borealis.ps1†L904-L931】【F:Data/Server/server.py†L221-L311】
- Agents no longer track hashes on disk; they simply send routine device details. The dashboard uses the server's stored hash (surfaced via `/api/agents` and device details) and compares it against the server-side cached GitHub head resolved through `/api/agent/repo_hash`.【F:Data/Agent/Roles/role_DeviceAudit.py†L1-L200】【F:Data/Server/server.py†L285-L382】【F:Data/Server/WebUI/src/Devices/Device_List.jsx†L168-L236】

This pipeline works but depends on GitHub API availability and manual file comparisons. Below are alternative approaches that keep things simple while avoiding the clunky text-file hash matching.

## Option 1 — Server-Side HEAD Tracking
1. Let the server treat its own checkout as the source of truth: read `.git/HEAD` (using the same helper that `role_DeviceAudit` already ships) on boot and whenever `/api/agent/repo_hash` is queried.
2. Cache the value in memory/SQLite to avoid repeated disk reads.
3. Agents continue to report the hash they are running; the dashboard compares against the locally computed HEAD instead of calling GitHub.

**Pros**
- Eliminates all external GitHub API calls and their rate limits.
- Reuses the existing hash wiring (agents keep posting hashes, server keeps storing them).

**Trade-offs**
- Only works when the server itself is always updated to the commit you consider “latest.” If you sometimes stage updates without updating the server repo, you need a flag to override the expected HEAD.

## Option 2 — Version Manifest File
1. Add a tracked file such as `Data/Agent/VERSION.json` that contains a semantic version or build number (`{"agent": "1.4.2"}`).
2. Update the manifest whenever you cut a release or land breaking changes. (It can be part of the release checklist or automated by CI.)
3. Agents read the manifest alongside `agent.py`, post the version string, and optionally persist it under `Settings/`.
4. The server or UI reads the same manifest from disk (or embeds it during packaging) and simply compares version strings.

**Pros**
- Very clear to operators: the UI can show “Agent 1.4.2” without exposing opaque hashes.
- Works even if you bundle Borealis without a `.git` folder (e.g., PyInstaller builds).

**Trade-offs**
- Requires someone to bump the version file. If that step is forgotten, the UI will not reflect new changes even though code moved.
- Agents need a tiny change to read and report the version string, but no new endpoints are required.

## Option 3 — Server-Published Release Manifest
1. Teach the server to store a `latest_agent_release` row (either in SQLite or a JSON file). The record would contain fields such as `commit`, `version`, and `released_at`.
2. Expose it via a lightweight endpoint (e.g., `GET /api/agent/release`) that agents can poll or receive during config bootstrap.
3. When `Borealis.ps1 -SilentUpdate` succeeds, have it `POST` the new release metadata back to the server (commit hash + timestamp). This keeps the server’s expectation in sync automatically.
4. Agents compare their local hash/version to the manifest and report both values in their heartbeat so the dashboard can show `Up-to-Date`, `Behind`, or `Ahead` statuses without calling GitHub.

**Pros**
- Single source of truth that survives restarts (stored in SQLite) and can be manually overridden from the server UI if needed.
- Works for fleets that are offline from GitHub but can still reach the Borealis server.

**Trade-offs**
- Requires one new endpoint and a small update to the silent update script so it reports success.
- Still relies on the update workflow to report back; if an operator copies files manually, the manifest will not update until someone tells the server.

## Option 4 — Timestamp-Based Drift Detection
1. When `Borealis.ps1 -SilentUpdate` completes, record the completion timestamp in a simple file (`Agent/Borealis/Settings/last_update.txt`) and include it in the device details payload.
2. The server keeps the timestamp from the last time it downloaded a release (or last server update) as the expected baseline.
3. The UI compares timestamps to show whether a device has consumed the most recent update batch.

**Pros**
- No hashes at all; just lightweight timestamps that are easy to understand.
- Useful when you only care about “updated since Tuesday’s rollout” rather than specific commits.

**Trade-offs**
- Detects relative freshness, not exact build parity. If two different commits are released on the same day without updating the expected timestamp, the UI may report agents as “current” even when they aren’t at the newest commit.

Each option keeps the wiring simple while avoiding the clunky GitHub polling + text-file comparisons. We can mix-and-match (e.g., manifest + timestamp) depending on how much fidelity vs. operational simplicity you want.
