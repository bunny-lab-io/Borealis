# Engine Staging & Parity Guide

This guide supports Step 12 of the migration plan by walking operators through
standing up the Engine alongside the legacy server, validating core workflows,
and documenting any behavioural gaps before switching the production entrypoint
to `Data/Engine/bootstrapper.py`.

## 1. Prerequisites

- Python 3.11 or later available on the host.
- A clone of the Borealis repository with the Engine tree checked out.
- Access to the legacy runtime assets (certificates, TLS bundle, etc.).
- Optional: a staging agent install for end-to-end WebSocket validation.

Ensure the SQLite database lives at `<project_root>/database.db` and that the
Engine migrations have already run (they execute automatically when the
`BOREALIS_ENGINE_AUTO_MIGRATE` environment variable is left at its default
`true`).

## 2. Launching the Engine in staging mode

1. Open a terminal at the project root.
2. Set any environment overrides required for the test scenario (for example,
   `BOREALIS_DEBUG=true` to surface verbose logging, or
   `BOREALIS_CORS_ALLOWED_ORIGINS=https://localhost:3000` when pairing with the
   React UI).
3. Run the Engine entrypoint:

   ```bash
   python Data/Engine/bootstrapper.py
   ```

4. Verify `Logs/Server/engine.log` is created and that the startup entries are
   timestamped `<timestamp>-engine-<message>`.

Keep the legacy server running in a separate process if comparative testing is
required; they do not share global state.

## 3. Feature validation checklist

Work through the following areas and tick each box once verified. Capture any
issues in the log table in §4.

### Authentication and tokens

- [ ] `POST /api/agent/token/refresh` returns a new access token when supplied a
      valid refresh token + DPoP proof.
- [ ] Invalid DPoP proofs or revoked refresh tokens yield the expected HTTP 401
      responses and structured error payloads.
- [ ] Device last-seen metadata updates inside the database after a successful
      refresh.

### Enrollment

- [ ] `POST /api/agent/enroll/request` produces an enrollment ticket with the
      correct expiration and retry counters.
- [ ] `POST /api/agent/enroll/poll` transitions an approved device into an
      authenticated state and returns the TLS bundle.
- [ ] Audit logging for approvals lands in `Logs/Server/engine.log`.

### Job management

- [ ] `POST /api/jobs` (or UI equivalent) creates a scheduled job and returns a
      manifest identifier.
- [ ] `GET /api/jobs/<id>` surfaces the stored manifest with normalized
      schedules and environment variables.
- [ ] Job lifecycle events arrive over the `job_management` Socket.IO namespace
      when a job transitions between `pending`, `running`, and `completed`.

### Real-time agents

- [ ] Agents connecting to the `agents` namespace appear in the realtime roster
      with accurate hostname, username, and fingerprint details.
- [ ] Screenshot broadcasts relay from agents to the UI without residual cache
      bleed-through after disconnects.
- [ ] Macro execution responses round-trip through Socket.IO and reach the
      initiating client.

### GitHub integration

- [ ] `GET /api/repo/current_hash` reflects the latest branch head and caches
      repeated calls.
- [ ] `POST /api/github/token` persists a new token and survives Engine restarts
      (confirm via database inspection).
- [ ] The background refresher logs rate-limit warnings instead of raising
      uncaught exceptions when the GitHub API throttles requests.

## 4. Recording divergences

Use the table below to document behavioural differences or bugs uncovered during
staging. This artifact should accompany the staging run summary so follow-up
fixes can be triaged quickly.

| Area | Legacy Behaviour | Engine Behaviour | Notes / Links |
| --- | --- | --- | --- |
| Authentication |  |  |  |
| Enrollment |  |  |  |
| Scheduler |  |  |  |
| Realtime |  |  |  |
| GitHub |  |  |  |
| Other |  |  |  |

## 5. Cut-over readiness

Once every checklist item passes and no critical divergences remain:

1. Update `Data/Engine/CURRENT_STAGE.md` with the completion date for Step 12.
2. Coordinate with the operator to switch deployment scripts to
   `Data/Engine/bootstrapper.py`.
3. Plan a rollback strategy (typically re-launching the legacy server) should
   issues appear immediately after the cut-over.
4. Archive the filled divergence table alongside Engine logs for historical
   traceability.

Document the results in project tracking tools before moving on to deprecating
`Data/Server`.
