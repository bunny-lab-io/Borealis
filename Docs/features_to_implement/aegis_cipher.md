# Aegis Cipher Integration Plan
[Back to Docs Index](../index.md) | [Index (HTML)](../index.html)

## Status
- Status: implemented in the current working tree.
- Scope: Engine-global protected-secret storage for stored credentials and the GitHub API token.
- Out of scope for v1: `agent_service_account.password_encrypted` and any future per-agent secret material.

## Summary
- Borealis now has an Engine-global Aegis secret manager that protects stored credentials and the GitHub API token at rest using `scrypt` plus `AES-256-GCM`.
- The derived key lives only in Engine memory for the current process lifetime. Restarting the Engine relocks protected secrets until an admin re-enters the cipher.
- Authenticated admins are auto-prompted after login only when Aegis is already configured and currently locked. Canceling keeps them signed in and warns that credential-backed jobs and other protected-secret workflows remain disabled until unlock.
- Operators can still save scheduled job definitions that reference credentials while Aegis is locked, but credential-backed execution is blocked until unlock.

## Implemented Design
### Crypto and storage
- `Data/Engine/crypto/aegis.py` provides the low-level helper module.
- KDF: `scrypt` with a per-install random 16-byte salt, `n=32768`, `r=8`, `p=1`, output length `32`.
- Cipher: `AES-256-GCM` with a random 12-byte nonce for each protected value.
- Envelope format: `aegis:v1:<base64(nonce+ciphertext_and_tag)>`.
- Cipher input is used exactly as entered. The empty string is rejected.
- Verification plaintext constant: `7f5c2a1d-6e8b-4f3b-a0d1-9c3f77b34d52`.
- PostgreSQL state table: `aegis_cipher_state(id=1, kdf_name, kdf_params_json, verification_token, created_at, updated_at)`.

### Protected secret scope
- Existing credential secret columns remain in place and now store ASCII Aegis envelopes in:
- `credentials.password_encrypted`
- `credentials.private_key_encrypted`
- `credentials.private_key_passphrase_encrypted`
- `credentials.become_password_encrypted`
- The GitHub token is stored as an Aegis envelope in `github_token.token`.
- Legacy plaintext credential and GitHub token values are treated as migration input during setup only. After Aegis is configured, any non-null protected value that does not start with `aegis:v1:` is treated as corruption.

### Runtime service and transaction rules
- `Data/Engine/services/aegis_cipher.py` owns setup, unlock, rotation, migration, and decrypt-on-use behavior.
- `EngineContext` and `EngineServiceAdapters` now carry one shared `AegisCipherService` instance so credentials, GitHub integration, and scheduler execution all use the same runtime key state.
- Setup and rotation run inside a single DB transaction. If any decrypt or re-encrypt step fails, all protected-storage changes roll back and the previous in-memory key remains active.
- While locked, `GitHubIntegration.load_token()` behaves as "no token available" so background repo sync can continue unauthenticated instead of crashing.

## Public API and UI Flow
### API endpoints
- `GET /api/aegis/status`: authenticated status endpoint for all logged-in users. Returns `configured`, `locked`, `unlock_scope`, `secret_scope`, and `updated_at`.
- `POST /api/aegis/setup`: admin-only setup endpoint. Encrypts legacy plaintext credentials and GitHub token, stores the verification token, and leaves the Engine unlocked.
- `POST /api/aegis/unlock`: admin-only unlock endpoint. Derives the key from stored params, verifies the stored token, and caches the key in Engine memory.
- `POST /api/aegis/rotate`: admin-only rotation endpoint. Verifies the current cipher, derives a new key, re-encrypts all protected values, and stays unlocked with the new key.
- `POST /api/aegis/force_reset`: admin-only destructive recovery endpoint. Destroys unrecoverable secret material, clears the configured Aegis state, preserves credential records, and marks affected secrets for re-entry.

### Access-management behavior
- `GET /api/credentials` and `GET /api/credentials/<id>` remain metadata-only and keep working while locked.
- Credential `POST`, `PUT`, and `DELETE` are blocked while Aegis is unconfigured or locked.
- `GET /api/github/token` remains available while locked and returns `status: "locked"`, `locked: true`, and a blank token value.
- GitHub token `POST` is blocked while Aegis is unconfigured or locked.

### WebUI behavior
- `Data/Engine/web-interface/src/App.jsx` owns Aegis status, refreshes it with session validation, and auto-prompts admins when configured and locked.
- `Data/Engine/web-interface/src/Access_Management/Aegis_Cipher_Dialog.jsx` provides shared `setup`, `unlock`, `rotate`, and destructive `force_reset` modes with copy that covers both credentials and the GitHub token.
- Canceling the login-time unlock prompt dismisses it for the current SPA session and sends a warning toast through the existing notification path.
- `Data/Engine/web-interface/src/Access_Management/Credential_List.jsx` renders a synthetic `Aegis Cipher` row with `Not configured`, `Locked`, or `Unlocked` status and shows the correct Setup, Enter, Rotate, and Force Reset actions.
- Credentials page header behavior:
- Before setup: `Refresh`, disabled `New Credential`, far-right primary `Setup Aegis Cipher`
- Configured but locked: `Refresh`, secondary `Enter Aegis Cipher`, disabled `New Credential`
- Configured and unlocked: `Refresh`, normal `New Credential`
- `Data/Engine/web-interface/src/Access_Management/Credential_List.jsx` now owns the synthetic GitHub token row, and `Data/Engine/web-interface/src/Access_Management/Credential_Editor.jsx` provides the GitHub-token update flow.
- Force reset keeps credential records visible, marks affected secret fields in metadata, highlights those fields in the credential editor, preserves a GitHub token reset marker, and warns operators that dependent scheduled jobs stay disabled until secrets are restored.
- `Data/Engine/web-interface/src/Scheduling/Create_Job.jsx` continues to show credential metadata while locked and warns that remote jobs can still be saved but will not execute until unlock.

## Scheduler and Secret Use
- The scheduler no longer decodes credential blobs directly as UTF-8 for normal operation.
- `Data/Engine/services/API/scheduled_jobs/management.py` now installs an Aegis-backed credential fetcher that decrypts credentials on demand.
- `Data/Engine/services/API/scheduled_jobs/job_scheduler.py` treats locked credentials as a distinct failure mode.
- Locked credential-backed SSH or WinRM execution now fails with:
- operator-facing error: `Aegis Cipher has not been entered; credential-backed execution is disabled.`
- target resolution reason: `credential_locked`
- Locked credentials are not reported as missing credentials.
- Credentials that were wiped by an Aegis force reset fail with:
- operator-facing error: `The credential associated with this scheduled job can no longer be decrypted due to the Aegis Cipher being reset, please update the credential with the data it is missing.`
- target resolution reason: `credential_reset_required`
- Scheduled jobs linked to reset-required credentials are disabled automatically until the operator restores those secrets and re-enables the job.

## Verification Coverage
- Unit coverage was added for:
- Aegis setup from legacy plaintext credentials and GitHub token
- wrong-cipher unlock failure
- restart relock behavior
- credential CRUD after setup
- blocked credential and GitHub token mutations while unconfigured or locked
- Aegis rotation and invalidation of the old cipher
- rollback on rotation failure
- force reset preserving records while destroying secret material
- reset-marker clearing after secret re-entry
- scheduled-job failure while locked and recovery after unlock
- scheduled-job disablement and `credential_reset_required` handling after force reset
- Manual runtime verification in this workspace is limited because the active environment does not include `flask`, `eventlet`, `pytest`, or Node-based WebUI tooling. Syntax checks were completed with `python3 -m py_compile`, and the remaining runtime/UI verification should be done in the packaged Borealis environment.

## Primary Files
- `Data/Engine/crypto/aegis.py`
- `Data/Engine/services/aegis_cipher.py`
- `Data/Engine/services/API/access_management/aegis.py`
- `Data/Engine/services/API/access_management/credentials.py`
- `Data/Engine/services/API/access_management/github.py`
- `Data/Engine/integrations/github.py`
- `Data/Engine/services/API/scheduled_jobs/management.py`
- `Data/Engine/services/API/scheduled_jobs/job_scheduler.py`
- `Data/Engine/web-interface/src/App.jsx`
- `Data/Engine/web-interface/src/Access_Management/Aegis_Cipher_Dialog.jsx`
- `Data/Engine/web-interface/src/Access_Management/Credential_Editor.jsx`
- `Data/Engine/web-interface/src/Access_Management/Credential_List.jsx`
- `Data/Engine/web-interface/src/Scheduling/Create_Job.jsx`
- `Data/Engine/Unit_Tests/test_access_management_api.py`
- `Data/Engine/Unit_Tests/test_scheduled_jobs_api.py`

## Related Documentation
- [Engine Runtime](../engine-runtime.md)
- [Database Reference](../db-reference.md)
- [Security and Trust](../security-and-trust.md)
- [Technical Debt](../technical-debt.md)
