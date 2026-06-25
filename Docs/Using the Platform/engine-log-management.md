# Engine Log Management

Engine Log Management provides WebUI access to Engine log domains, log files, parsed entries, raw text, retention policy, and safe deletion.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Log_Management.png" alt="Borealis Log Management" loading="lazy">
  <figcaption>Log Management provides filtered access to Engine and service logs from the WebUI.</figcaption>
</figure>

## Browse Logs

1. Open `Admin Settings > Log Management`.
2. Select log domain.
3. Select log file.
4. Use parsed grid view for timestamp, level, scope, service, and message.
5. Switch to `Raw` when formatting or traceback detail matters.

## Manage Retention

Set retention days per log domain. Blank value inherits the default. Saving retention also prunes expired rotated files when applicable.

## Delete Logs

Use `Delete File` for one file. Use domain/family deletion only when you intentionally want all rotated files for that log domain removed.

!!! warning

    Do not delete logs mid-investigation. Download, copy, or inspect relevant evidence first.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/server/logs` - list logs and retention metadata.
    - `GET /api/server/logs/<log_name>/entries` - tail parsed entries.
    - `PUT /api/server/logs/retention` - update retention policy.
    - `DELETE /api/server/logs/<log_name>` - delete log file or family.

    ### Related documentation

    - [Server Info](server-info.md)
    - [Remote Shell](remote-shell.md)
    - [Remote Desktop](remote-desktop.md)
    - [Watchdogs](watchdogs.md)
    - [Engine Runtime](../Reference/Core%20Runtimes/engine-runtime.md)

    ### Source map

    - Log API: `Data/Engine/Containers/api-backend/cmd/api-backend/server_logs.go`
    - Log UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Admin/Log_Management.jsx`
    - Service logging helper: `Data/Engine/Containers/api-backend/data/services/API/__init__.py`

    ### Runtime behavior

    - Primary API logs live under `Engine/Services/api-backend/logs/`.
    - Traefik logs live under `Engine/Services/traefik-edge/logs/`.
    - guacd logs live under `Engine/Services/remote-desktop-guacd/logs/`.
    - WireGuard tunnel logs live under `Engine/Services/wireguard-tunnel/logs/` and `Engine/Services/api-backend/logs/VPN_Tunnel/`.
    - Retention overrides live in `Engine/Services/api-backend/logs/retention_policy.json`.
