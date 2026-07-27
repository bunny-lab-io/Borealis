# Backup and Restore

Use Backup/Restore to move Engine configuration, trust, user-created content, and protected secrets to another Engine instance without copying logs or historical execution data.

Backups are encrypted JSON files. Borealis uses the Aegis Cipher as the backup master key, so imports require the same cipher that protected the source Engine.

!!! warning
    Restoring a backup sterilizes current Engine configuration, secrets, and trust before importing the encrypted backup. Use it only when the selected file and Aegis Cipher are correct.

## Export a Backup

1. Open **Admin Settings > Backup/Restore**.
2. Select **Export**.
3. Store the downloaded encrypted JSON file where operators with access understand that the Aegis Cipher is still required to decrypt it.

The export contains Engine configuration, sites, agents, identities, LDAP/directory settings, credentials, Aegis state, saved automation content, scheduled job definitions, watchdog definitions, device filters, patch policies, current patch inventory, patch catalog cache, metadata definitions and values, and Engine trust/key material. Device trust exports include only the latest active device key and refresh token per agent.

It does not contain logs, saved views, pending device approvals, device activity history, scheduled job run history, patch policy run history, patch policy enforcement state, workflow run history, watchdog incident history, scheduler queues, or worker runtime state.

## Restore During First Setup

On a fresh Engine, the Aegis setup screen shows **Restore Engine Config Backup**. Select it to open the restore-only page before any normal Engine navigation is available.

1. Select the encrypted JSON backup file.
2. Enter the Aegis Cipher used by the source Engine.
3. Select **Analyze** and review the import counts.
4. Type `RESTORE ENGINE CONFIG BACKUP`.
5. Select **Import**.
6. Wait for the Engine runtime refresh to finish, then unlock Aegis when prompted.

Agents keep trust when the restored Engine remains reachable at the same FQDN they already trust. Internal-Only restores also keep the Borealis local CA and leaf key material, so do not change the Engine FQDN during a clean migration unless you plan to reinstall or reconfigure agents and browser trust.

## Restore From an Existing Engine

1. Sign in as an administrator.
2. Open **Admin Settings > Backup/Restore**.
3. Select the encrypted JSON backup file.
4. Enter the Aegis Cipher used by the source Engine.
5. Select **Analyze** and review the import counts.
6. Type `RESTORE ENGINE CONFIG BACKUP`.
7. Select **Import**.
8. Wait for the Engine runtime refresh to finish, then unlock Aegis when prompted.

!!! danger
    Import does not merge. Current users, sites, directory settings, credentials, agents, trust keys, filters, scheduled job definitions, watchdog definitions, and automation content are cleared before the backup is imported.

## K3s Restore Notes

K3s Engines use the same encrypted Backup/Restore workflow. Export, Analyze, and Import run through the K3s `api-backend` pod and the active `BOREALIS_DATABASE_URL`, which points at the ClusterIP `postgres-db.borealis.svc:5432` after K3s PostgreSQL cutover.

Use **Analyze** first when validating a backup on a running production Engine. Analyze decrypts the backup, verifies the Aegis metadata, checks supported table/file identifiers, and compares backup columns against the current PostgreSQL schema without deleting current data or writing imported rows.

!!! danger
    **Import** is destructive on K3s too. It clears allow-listed Engine configuration and trust tables in the active K3s PostgreSQL database, replaces allow-listed secret/config files on mounted Engine paths, clears mounted Engine service log roots, clears the in-memory Aegis key, and starts an automatic runtime refresh. Run full Import validation on a fresh or disposable Engine unless production data replacement is intentional.

After a K3s import completes, Borealis asks the in-cluster operator to retire stale site-workers and restart the API, WireGuard, Traefik, and scheduler workloads against the restored state. The web session may briefly disconnect while those pods restart. Wait for the refresh to settle before reinstalling or re-enrolling agents.

If the restore response says a manual restart is required, redeploy the Engine in the same network mode:

```sh
cd /opt/Borealis
sudo bash Engine.sh --network-mode public deploy prod
```

Use `--network-mode local` instead when the restored Engine is an Internal-Only deployment.

!!! warning
    Wait for the automatic refresh or fallback redeploy to finish before reinstalling or re-enrolling agents. If an agent receives a new access token from an API pod that started before restore replaced the Engine auth files, the site-worker may reject the management socket as `invalid_token` until the runtime refresh completes and the agent reconnects.

## Validate a K3s Restore Target

Validate full Import on a fresh or disposable Engine, not on the production Engine that exported the backup. The target should use the same network mode and FQDN plan as the Engine you intend to recover.

1. Deploy a fresh K3s-backed Engine.
2. Open the Aegis setup screen and select **Restore Engine Config Backup**.
3. Select the encrypted JSON backup file.
4. Enter the Aegis Cipher from the source Engine.
5. Select **Analyze** and confirm the reported table and file counts match the expected backup contents.
6. Type `RESTORE ENGINE CONFIG BACKUP`.
7. Select **Import**.
8. Wait for the automatic runtime refresh to settle. If the restore response says manual restart is required, redeploy the Engine in the same network mode and wait for the rollout checks to pass before reconnecting agents.
9. Sign in, unlock Aegis, and run the normal post-restore smoke checks.

!!! warning
    Full Import validation replaces the clean target's Engine configuration and trust state with the backup. Do not point a disposable restore target at production agents unless you intentionally want those agents to trust and report to that target.

Post-restore checks:

```sh
cd /opt/Borealis
sudo bash Engine.sh --network-mode public deploy prod
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status statefulset/postgres-db
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/api-backend
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/job-scheduler
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis rollout status deployment/traefik-edge
sudo k3s kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml -n borealis exec deployment/api-backend -- borealis-api-backend-go api-healthcheck
```

Use `--network-mode local` for Internal-Only restore validation. After the cluster checks pass, confirm WebUI login, Aegis unlock, Sites, Device Inventory, Server Info, Backup Export, and one remote operation against a disposable or intentionally migrated agent.

??? example "Detailed Codex Breakdown"

    ### API endpoints
    - `GET /api/server/backup/export` (Admin) - returns an encrypted JSON attachment. Requires Aegis configured and unlocked.
    - `POST /api/server/backup/analyze` (Admin) - decrypts and validates backup JSON, then returns high-level import counts without modifying Engine state.
    - `POST /api/server/backup/restore` (Admin) - restores an encrypted backup from normal Engine operation.
    - `POST /api/bootstrap/backup/analyze` (No Authentication, bootstrap only) - decrypts and validates backup JSON before normal login is enabled, then returns high-level import counts without modifying Engine state.
    - `POST /api/bootstrap/backup/restore` (No Authentication, bootstrap only) - restores an encrypted backup before normal login is enabled.

    ### Source map
    - API routes and backup implementation: `Data/Engine/Containers/api-backend/cmd/api-backend/server_backup.go`
    - Route registration: `Data/Engine/Containers/api-backend/cmd/api-backend/main.go`
    - WebUI page: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Backup_Restore.jsx`
    - WebUI route wiring: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/router.jsx`
    - Aegis setup entry point: `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/routes/BootstrapEntry.jsx`

    ### Runtime behavior
    - Outer backup JSON contains `kind`, `schema_version`, `kdf_params`, `nonce_b64`, and `ciphertext_b64`.
    - Inner payload uses the fixed Borealis backup encryption path: AES-256-GCM with the Aegis-derived key.
    - `engine.aegis_cipher_state` is included inside the encrypted payload and restored unchanged, so the Aegis Cipher does not rotate through backup/restore.
    - K3s Engines run backup routes through the K3s `api-backend` pod. After Stage 9, `BOREALIS_DATABASE_URL` points at `postgres-db.borealis.svc:5432`, so WebUI imports target K3s PostgreSQL, not retired Compose PostgreSQL.
    - Traefik ACME state must remain `0600` and readable by the `api-backend` runtime user. `Engine.sh` repairs ownership to the Borealis runtime user during deploy so export can read the file without loosening group/world permissions.
    - Analyze uses the same decrypt and validation path as restore, but does not clear current state or import rows.
    - Clean K3s restore-target validation must use a fresh or disposable Engine because the restore path deletes allow-listed configuration/trust tables before importing rows.
    - Restore rejects malformed backups, wrong ciphers, unsupported table IDs, unsupported file IDs, and target columns not present in the running Engine schema.
    - Restore deletes allow-listed configuration/trust tables plus runtime/history-adjacent tables, imports backup rows, resets serial sequences where applicable, replaces allow-listed key/config files, clears mounted Engine service log roots on a best-effort basis, clears pending device approvals and saved views, clears the in-memory Aegis key, clears operator cookies, and asks `borealis-operator` to run the post-restore runtime refresh.
    - The post-restore runtime refresh retires existing site-worker pods, then restarts `api-backend`, `wireguard-tunnel`, `traefik-edge`, and `job-scheduler` so restored auth keys, Engine secret material, WireGuard state, and Traefik TLS state are loaded before agents reconnect. If `borealis-operator` is unavailable, restore returns `restart_required: true` and operators should run the documented deploy command.

    ### Included state
    - LDAP/directory providers, server URLs, host overrides, TLS/LDAPS settings, PEM trust anchors, encrypted bind/keytab secrets, group-role mappings, group-site mappings, and cached directory users.
    - Sites, enrollment codes, auto-approval settings, device-site assignments, and operator site assignments.
    - Agents, latest active device key per agent, latest active refresh token per agent, purge barriers, VPN config/leases/key leases, agent service account rows, Agent JWT key, script signing keys, WireGuard server keys, Engine secret, Traefik ACME/settings state, Internal-Only local CA and leaf certificate/key files, release/settings JSON, software override JSON, and software blocklist JSON.
    - Users, roles, MFA/passkey data, credentials, GitHub token, Aegis state, assemblies, workflows, workflow webhooks, scheduled job definitions, watchdog definitions, device filters, patch policies, patch allow/block rules, patch targets/exclusions, patch catalog cache, metadata definitions/values, and current software and patch inventory.

    ### Excluded state
    - Engine logs and rotated logs.
    - Engine service log files are removed from mounted log roots during restore so the restored Engine starts from a clean log surface after restart.
    - Saved views.
    - Pending device approvals.
    - Device activity history.
    - Scheduled job runs, run targets, onboarding run events, and run activity links.
    - Patch policy runs, patch policy device enforcement state, and patch policy audit rows.
    - Workflow runs, node runs, and child job rows.
    - Watchdog runtime state and incident history.
    - Scheduler queues, worker rows, worker routes, and service snapshots.
