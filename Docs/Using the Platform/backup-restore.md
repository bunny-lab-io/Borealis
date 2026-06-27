# Backup and Restore

Use Backup/Restore to move Engine configuration, trust, user-created content, and protected secrets to another Engine instance without copying logs or historical execution data.

Backups are encrypted JSON files. Borealis uses the Aegis Cipher as the backup master key, so imports require the same cipher that protected the source Engine.

!!! warning
    Restoring a backup sterilizes current Engine configuration, secrets, and trust before importing the encrypted backup. Use it only when the selected file and Aegis Cipher are correct.

## Export a Backup

1. Open **Admin Settings > Backup/Restore**.
2. Select **Export**.
3. Store the downloaded encrypted JSON file where operators with access understand that the Aegis Cipher is still required to decrypt it.

The export contains Engine configuration, sites, agents, identities, LDAP/directory settings, credentials, Aegis state, saved automation content, scheduled job definitions, watchdog definitions, filters, saved views, metadata definitions and values, and Engine trust/key material.

It does not contain logs, device activity history, scheduled job run history, workflow run history, watchdog incident history, scheduler queues, or worker runtime state.

## Restore During First Setup

On a fresh Engine, the Aegis setup screen shows **Restore Engine Config Backup**. Select it to open the restore-only page before any normal Engine navigation is available.

1. Select the encrypted JSON backup file.
2. Enter the Aegis Cipher used by the source Engine.
3. Type `RESTORE ENGINE CONFIG BACKUP`.
4. Select **Import**.
5. Restart the API service, then unlock Aegis when prompted.

Agents keep trust when the restored Engine remains reachable at the same public hostname or FQDN they already trust.

## Restore From an Existing Engine

1. Sign in as an administrator.
2. Open **Admin Settings > Backup/Restore**.
3. Select the encrypted JSON backup file.
4. Enter the Aegis Cipher used by the source Engine.
5. Type `RESTORE ENGINE CONFIG BACKUP`.
6. Select **Import**.
7. Restart the API service, then unlock Aegis when prompted.

!!! danger
    Import does not merge. Current users, sites, directory settings, credentials, agents, trust keys, filters, scheduled job definitions, watchdog definitions, and automation content are cleared before the backup is imported.

??? example "Detailed Codex Breakdown"

    ### API endpoints
    - `GET /api/server/backup/export` (Admin) - returns an encrypted JSON attachment. Requires Aegis configured and unlocked.
    - `POST /api/server/backup/restore` (Admin) - restores an encrypted backup from normal Engine operation.
    - `POST /api/bootstrap/backup/restore` (No Authentication, bootstrap only) - restores an encrypted backup before normal login is enabled.

    ### Source map
    - API routes and backup implementation: `Data/Engine/Containers/api-backend/cmd/api-backend/server_backup.go`
    - Route registration: `Data/Engine/Containers/api-backend/cmd/api-backend/main.go`
    - WebUI page: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Backup_Restore.jsx`
    - WebUI route wiring: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/router.jsx`
    - Aegis setup entry point: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/BootstrapEntry.jsx`

    ### Runtime behavior
    - Outer backup JSON contains `kind`, `schema_version`, `created_at`, `kdf_params`, `nonce_b64`, and `ciphertext_b64`.
    - Inner payload uses the fixed Borealis backup encryption path: AES-256-GCM with the Aegis-derived key.
    - `engine.aegis_cipher_state` is included inside the encrypted payload and restored unchanged, so the Aegis Cipher does not rotate through backup/restore.
    - Restore rejects malformed backups, wrong ciphers, unsupported table IDs, unsupported file IDs, and target columns not present in the running Engine schema.
    - Restore deletes allow-listed configuration/trust tables plus runtime/history-adjacent tables, imports backup rows, resets serial sequences where applicable, replaces allow-listed key/config files, clears the in-memory Aegis key, clears operator cookies, and returns `restart_required: true`.

    ### Included state
    - LDAP/directory providers, server URLs, host overrides, TLS/LDAPS settings, PEM trust anchors, encrypted bind/keytab secrets, group-role mappings, group-site mappings, and cached directory users.
    - Sites, enrollment codes, auto-approval settings, device-site assignments, and operator site assignments.
    - Agents, device keys, refresh tokens, purge barriers, VPN config/leases/key leases, agent service account rows, Agent JWT key, script signing keys, WireGuard server keys, Engine secret, Traefik ACME/settings state, release/settings JSON, software override JSON, and software blocklist JSON.
    - Users, roles, MFA/passkey data, credentials, GitHub token, Aegis state, assemblies, workflows, workflow webhooks, scheduled job definitions, watchdog definitions, filters, saved views, metadata definitions/values, and current software inventory.

    ### Excluded state
    - Engine logs and rotated logs.
    - Device activity history.
    - Scheduled job runs, run targets, onboarding run events, and run activity links.
    - Workflow runs, node runs, and child job rows.
    - Watchdog runtime state and incident history.
    - Scheduler queues, worker rows, worker routes, and service snapshots.
