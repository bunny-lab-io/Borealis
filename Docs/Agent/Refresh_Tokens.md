# Borealis Agent Refresh Tokens

This page explains what an agent refresh token is, how it is issued, where it is stored, how long it lives, how sliding expiry works, and how the agent uses it to stay authenticated.

## What a Refresh Token Is
- A long-lived credential the agent gets during enrollment; it represents device trust and is bound to the agent’s key/certificate fingerprint.
- Stored locally under the agent settings directory as an encrypted blob (`refresh.token`) alongside token metadata (`access.meta.json`) and the agent GUID.
- Not presented to normal APIs; it is only sent to the Engine to mint new short-lived access tokens.

## How the Agent Obtains It
1) Enrollment (`/api/agent/enroll/request` → `/api/agent/enroll/poll`):
   - The agent proves possession of its Ed25519 identity and an operator-approved enrollment code.
   - The Engine issues:
     - `guid` (device identity),
     - `access_token` (EdDSA JWT, ~15 minutes),
     - `refresh_token` (random 48-byte urlsafe string),
     - Engine TLS bundle and signing key.
   - The agent persists the GUID, access token, refresh token, and expiry metadata via `AgentKeyStore` (`Data/Agent/security.py`).

## How Long It Lasts (Sliding Expiry)
- Base TTL: 90 days. Enrollment stores `expires_at = now + 90 days` in the Engine DB (`Data/Engine/services/API/enrollment/routes.py`).
- Sliding refresh: every successful call to `/api/agent/token/refresh` resets `expires_at` to `now + 90 days` and updates `last_used_at` (`Data/Engine/services/API/tokens/routes.py`). This favors recent activity rather than absolute age.
- If the Engine is offline, the agent simply keeps the stored refresh token; it will retry when connectivity returns. Expiry is enforced by the Engine clock, not the agent.

## Access Tokens vs. Refresh Tokens
- Access tokens: EdDSA JWTs with a ~15 minute lifetime (`expires_in: 900`). Used for all authenticated REST/WebSocket calls and renewed proactively.
- Refresh tokens: never presented to device APIs; only used to obtain a new access token. If absent/expired/invalid, the agent falls back to re-enrollment.

## How the Agent Uses It
- Every authenticated call runs through `AgentHttpClient.ensure_authenticated()` (`Data/Agent/agent.py`):
  - Reloads tokens from disk.
  - If no GUID/refresh token, performs enrollment.
  - If no access token or it is about to expire, posts `{guid, refresh_token}` to `/api/agent/token/refresh`.
- On refresh:
  - Success: stores the new access token, updates expiry metadata, and continues.
  - 401/403 with specific errors (e.g., expired, fingerprint mismatch, token_version bump) trigger token clear + re-enrollment.
- Token storage:
  - Refresh token: DPAPI-protected on Windows (or stored locally with restricted permissions elsewhere) at `refresh.token`.
  - Access token: `access.jwt` plus expiry in `access.meta.json`.
  - GUID: `Agent_GUID.txt`.

## When It Stops Working
- Engine-side expiry: if `expires_at` (in Engine DB) is older than “now,” refresh attempts return `refresh_token_expired` (401) and the agent re-enrolls.
- Revocation: device status `revoked/decommissioned` or token_version bumps invalidate the refresh token and force re-enrollment.
- Certificate/key changes: mismatched fingerprint bindings also force a re-enrollment path.

## Operational Notes
- Short outages (days/weeks) are tolerated: the 90-day sliding window resets on the first successful refresh after the Engine is back.
- Very long inactivity (>90 days without refresh) will require re-enrollment; the agent will reuse the last installer code if available, otherwise operator action is needed.
- Logs for token activity live under `Agent/Logs/` (`agent.log`, `agent.error.log`). Engine-side changes are recorded in the Engine DB `refresh_tokens` table with `last_used_at` and `expires_at`.

## Relevant Files (relative paths)
- `Docs/Agent/Refresh_Tokens.md` (this document)
- `Data/Agent/agent.py` (agent token lifecycle: ensure/authenticate/refresh/enroll)
- `Data/Agent/security.py` (token persistence: `access.jwt`, `refresh.token`, `access.meta.json`, GUID)
- `Data/Engine/services/API/enrollment/routes.py` (issues initial access/refresh tokens; sets refresh `expires_at`)
- `Data/Engine/services/API/tokens/routes.py` (refresh endpoint; sliding 90-day extension)
- `Data/Engine/auth/jwt_service.py` (access token issuance; 15-minute `expires_in`)
- `Data/Engine/database_migrations.py` (defines `refresh_tokens` table schema and indexes)
- `Data/Engine/Unit_Tests/test_tokens_api.py` (coverage for refresh behavior and expiry updates)
